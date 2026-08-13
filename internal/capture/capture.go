// Package capture provides the core TwinCAT Analytics symbol capture logic,
// extracted from cmd/capture for reuse by cmd/runner.
package capture

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/siyka-au/twincat-analytics-go/internal/brokerinfo"
	"github.com/siyka-au/twincat-analytics-go/internal/fixture"
	"github.com/siyka-au/twincat-analytics-go/parser"
)

var logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
	Level: slog.LevelInfo,
}))

// Options configures a single capture run.
type Options struct {
	// MQTT connection
	BrokerURI string
	ClientID  string
	MQTTUser  string
	MQTTPass  string

	// Topics (derived from BaseTopic if not set explicitly)
	SymbolTopic string
	DescTopic   string
	DataTopic   string

	// Capture behaviour
	BaseTopic   string
	Timeout     time.Duration
	OutDir      string
	DataCount   int
	DataTimeout time.Duration
}

// RunResult holds all outputs of a successful Run call.
type RunResult struct {
	// Fixture is the parsed per-message sidecar (nil when parse failed).
	Fixture *fixture.SymbolFixture

	// CaptureDir is the path to the capture-{stamp}/ directory that was created.
	// e.g. "testdata/fixtures/SimpleTypeTest/captures/capture-202602250549"
	CaptureDir string

	// SymbolsYMLPath is the path to the symbols/message-*.yml sidecar that was written.
	SymbolsYMLPath string

	// DescYMLPath is non-empty when a Desc message was captured.
	DescYMLPath string

	// DataDir is non-empty when data messages were captured.
	DataDir string

	// DataCount is the number of Data message files written.
	DataCount int
}

// capturedDataMsg pairs a received Bin/Tx/Data payload with the UTC timestamp
// at which the MQTT handler fired.
type capturedDataMsg struct {
	receivedAt time.Time
	payload    []byte
}

func captureDataMessages(ctx context.Context, client mqtt.Client, topic string, count int, timeout time.Duration) []capturedDataMsg {
	dataCh := make(chan capturedDataMsg, count)
	client.Subscribe(topic, 0, func(_ mqtt.Client, msg mqtt.Message) {
		cp := make([]byte, len(msg.Payload()))
		copy(cp, msg.Payload())
		select {
		case dataCh <- capturedDataMsg{receivedAt: time.Now().UTC(), payload: cp}:
		default:
		}
	})
	defer client.Unsubscribe(topic)

	var timer <-chan time.Time
	if timeout > 0 {
		timer = time.After(timeout)
	}

	results := make([]capturedDataMsg, 0, count)
	for len(results) < count {
		select {
		case cm := <-dataCh:
			results = append(results, cm)
			logger.Info("data message received", "n", len(results), "of", count, "bytes", len(cm.payload))
		case <-timer:
			logger.Info("data capture timeout reached", "collected", len(results), "requested", count)
			return results
		case <-ctx.Done():
			logger.Info("data capture cancelled", "collected", len(results))
			return results
		}
	}
	return results
}

// Run performs a full capture: connects to MQTT, collects symbols (+ optional
// desc and data messages), and writes all files under OutDir. It returns a
// RunResult describing all output paths and the parsed SymbolFixture.
func Run(ctx context.Context, opts Options) (*RunResult, error) {
	if err := os.MkdirAll(opts.OutDir, 0755); err != nil {
		return nil, fmt.Errorf("capture: create out dir: %w", err)
	}

	stamp := time.Now().UTC().Format("200601021504")
	captureDir := filepath.Join(opts.OutDir, fmt.Sprintf("capture-%s", stamp))

	symbolsDir := filepath.Join(captureDir, "symbols")
	descDir := filepath.Join(captureDir, "desc")
	for _, dir := range []string{symbolsDir, descDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("capture: create subdir %s: %w", dir, err)
		}
	}

	logger.Info("connecting to MQTT broker", "uri", opts.BrokerURI, "client_id", opts.ClientID)

	mqttOpts := mqtt.NewClientOptions().
		AddBroker(opts.BrokerURI).
		SetClientID(opts.ClientID).
		SetOrderMatters(false).
		SetAutoReconnect(false)
	if opts.MQTTUser != "" {
		mqttOpts.SetUsername(opts.MQTTUser)
	}
	if opts.MQTTPass != "" {
		mqttOpts.SetPassword(opts.MQTTPass)
	}

	client := mqtt.NewClient(mqttOpts)
	tok := client.Connect()
	if tok.Wait() && tok.Error() != nil {
		return nil, fmt.Errorf("capture: MQTT connect: %w", tok.Error())
	}
	defer client.Disconnect(500)

	logger.Info("connected")

	probeCh := make(chan brokerinfo.BrokerInfo, 1)
	go func() {
		probeCh <- brokerinfo.Probe(client, 5*time.Second)
	}()

	descCh := make(chan mqtt.Message, 1)
	client.Subscribe(opts.DescTopic, 0, func(_ mqtt.Client, msg mqtt.Message) {
		select {
		case descCh <- msg:
		default:
		}
	})

	logger.Info("waiting for Symbols message", "topic", opts.SymbolTopic, "timeout", opts.Timeout)

	msgCh := make(chan mqtt.Message, 1)
	client.Subscribe(opts.SymbolTopic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		select {
		case msgCh <- msg:
		default:
		}
	})

	var symbolMsg mqtt.Message
	select {
	case symbolMsg = <-msgCh:
		logger.Info("received Symbols message", "bytes", len(symbolMsg.Payload()))
	case <-time.After(opts.Timeout):
		return nil, fmt.Errorf("capture: timed out waiting for Symbols message on %s", opts.SymbolTopic)
	case <-ctx.Done():
		return nil, fmt.Errorf("capture: cancelled while waiting for Symbols message")
	}

	var bi brokerinfo.BrokerInfo
	select {
	case bi = <-probeCh:
		if bi.BrokerType != "" {
			logger.Info("broker identified", "type", bi.BrokerType, "version", bi.Version)
		} else {
			logger.Warn("broker $SYS probe returned no result")
		}
	case <-time.After(1 * time.Second):
		logger.Warn("broker probe timed out")
	}

	// Write capture.yml with session context.
	captureYMLPath := filepath.Join(captureDir, "capture.yml")
	captureMeta := &fixture.CaptureMeta{
		CapturedAt:           time.Now().UTC().Format(time.RFC3339),
		BrokerURI:            opts.BrokerURI,
		ClientID:             opts.ClientID,
		BrokerType:           bi.BrokerType,
		BrokerVersion:        bi.Version,
		BrokerBuildTimestamp: bi.BuildTimestamp,
		BaseTopic:            opts.BaseTopic,
	}
	if err := fixture.WriteCaptureMeta(captureYMLPath, captureMeta); err != nil {
		return nil, fmt.Errorf("capture: write capture.yml: %w", err)
	}
	logger.Info("wrote capture.yml", "path", captureYMLPath)

	// Write symbols binary.
	payload := symbolMsg.Payload()
	msgTimestamp := time.Now().UTC().Format("20060102T150405.000000000")
	binPath := filepath.Join(symbolsDir, fmt.Sprintf("message-%s.bin", msgTimestamp))
	if err := os.WriteFile(binPath, payload, 0644); err != nil {
		return nil, fmt.Errorf("capture: write symbols binary: %w", err)
	}
	logger.Info("wrote symbols binary", "path", binPath)

	// Build and write the per-message symbols sidecar YAML.
	f := &fixture.SymbolFixture{
		Topic:         symbolMsg.Topic(),
		QoS:           symbolMsg.Qos(),
		Retained:      symbolMsg.Retained(),
		MQTTMessageID: symbolMsg.MessageID(),
		PayloadSHA256: fixture.SHA256Hex(payload),
	}

	s, parseErr := parser.ParseSymbolStream(payload)
	if parseErr != nil {
		logger.Error("Symbol stream parse failed — writing partial fixture", "err", parseErr)
		f.ParseError = parseErr.Error()
	} else {
		f.MajorVersion = s.Header.MajorVersion
		f.MinorVersion = s.Header.MinorVersion
		f.NumSymbols = s.Header.NumSymbols
		f.CodePage = s.Header.CodePage
		f.Flags = &fixture.StreamFlagsMetadata{
			IsOnlineChange:       s.Header.Flags.IsOnlineChange,
			IsTarget64Bit:        s.Header.Flags.IsTarget64Bit,
			AreBaseTypesIncluded: s.Header.Flags.AreBaseTypesIncluded,
		}
		for _, sym := range s.Symbols {
			f.Entries = append(f.Entries, fixture.SymbolEntry{
				Name:        sym.Name,
				TypeName:    sym.TypeName,
				DataType:    int(sym.DataType),
				IndexOffset: sym.IndexOffset,
				Size:        sym.Size,
			})
		}
		logger.Info("parsed symbols", "count", len(f.Entries))
	}

	symYMLPath := filepath.Join(symbolsDir, fmt.Sprintf("message-%s.yml", msgTimestamp))
	if err := fixture.Write(symYMLPath, f); err != nil {
		return nil, fmt.Errorf("capture: write symbols fixture: %w", err)
	}
	logger.Info("wrote symbols fixture", "path", symYMLPath)

	result := &RunResult{
		Fixture:        f,
		CaptureDir:     captureDir,
		SymbolsYMLPath: symYMLPath,
	}

	// Capture Desc message (best-effort, 2-second window).
	select {
	case descMsg := <-descCh:
		descPayload := descMsg.Payload()
		descTimestamp := time.Now().UTC().Format("20060102T150405.000000000")
		descJSONPath := filepath.Join(descDir, fmt.Sprintf("message-%s.json", descTimestamp))
		if err := os.WriteFile(descJSONPath, descPayload, 0644); err != nil {
			logger.Error("failed to write desc JSON", "path", descJSONPath, "err", err)
		} else {
			logger.Info("wrote desc JSON", "path", descJSONPath)
			descYMLPath := filepath.Join(descDir, fmt.Sprintf("message-%s.yml", descTimestamp))
			d := &fixture.DescFixture{
				Topic:         descMsg.Topic(),
				QoS:           descMsg.Qos(),
				Retained:      descMsg.Retained(),
				MQTTMessageID: descMsg.MessageID(),
				PayloadSHA256: fixture.SHA256Hex(descPayload),
			}
			if err := fixture.WriteDescFixture(descYMLPath, d); err != nil {
				logger.Error("failed to write desc fixture", "path", descYMLPath, "err", err)
			} else {
				logger.Info("wrote desc fixture", "path", descYMLPath)
				result.DescYMLPath = descYMLPath
			}
		}
	case <-time.After(2 * time.Second):
		logger.Warn("no Desc message received", "topic", opts.DescTopic)
	}

	// Capture Data messages when requested.
	if opts.DataCount > 0 {
		dataDir := filepath.Join(captureDir, "data")
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return nil, fmt.Errorf("capture: create data dir: %w", err)
		}
		logger.Info("capturing Data messages", "topic", opts.DataTopic, "count", opts.DataCount, "timeout", opts.DataTimeout)

		captured := captureDataMessages(ctx, client, opts.DataTopic, opts.DataCount, opts.DataTimeout)

		for _, cm := range captured {
			msgPath := filepath.Join(dataDir,
				fmt.Sprintf("message-%s.bin", cm.receivedAt.Format("20060102T150405.000000000")))
			if err := os.WriteFile(msgPath, cm.payload, 0644); err != nil {
				logger.Error("failed to write data message", "path", msgPath, "err", err)
				continue
			}
		}

		if len(captured) > 0 {
			result.DataDir = dataDir
			result.DataCount = len(captured)
			logger.Info("data capture complete", "count", len(captured), "folder", dataDir)
		} else {
			logger.Warn("no Data messages received", "topic", opts.DataTopic)
		}
	}

	return result, nil
}

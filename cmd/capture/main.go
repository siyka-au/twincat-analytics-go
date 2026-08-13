// cmd/capture subscribes to a TwinCAT Analytics Symbols MQTT topic, saves
// the raw binary payload, parses it with the hand-written symbol stream parser,
// and writes a paired human-editable YAML fixture file ready for use with go test.
//
// Usage:
//
//	go run ./cmd/capture [flags]
//
// Required flags (or equivalent TC_ANALYTICS_ environment variables):
//
//	--base-topic   TwinCAT base topic, e.g. "Devices/MyPLC"
//
// Optional flags:
//
//	--timeout        How long to wait for a Symbols message (default 30s)
//	--out-dir        Directory to write capture output (default "testdata/fixtures/misc/captures")
//	--data-count     Number of Bin/Tx/Data messages to capture (default 0 = skip)
//	--data-timeout   Time limit for data collection, e.g. 10s (default 0 = unlimited)
//
// Each run creates a capture-{stamp}/ subdirectory inside --out-dir:
//
//	capture-YYYYMMDDHHMM/
//	  capture.yml           — session metadata
//	  symbols/
//	    message-{ts}.bin    — raw Symbols binary (source of truth)
//	    message-{ts}.yml    — human-editable fixture sidecar
//	  desc/
//	    message-{ts}.json   — raw Desc JSON payload
//	    message-{ts}.yml    — desc fixture sidecar
//	  data/                 — present when --data-count > 0
//	    message-{ts}.bin    — one file per captured Data message
//
// All standard TC_ANALYTICS_ connection flags (--mqtt-uri, --mqtt-user,
// --mqtt-pass, --client-id) are also accepted. See internal/config for details.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/siyka-au/twincat-analytics-go/internal/capture"
	"github.com/siyka-au/twincat-analytics-go/internal/config"
)

var logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
	Level: slog.LevelInfo,
}))

func main() {
	timeout := flag.Duration("timeout", 30*time.Second,
		"How long to wait for a Symbols message before giving up")
	outDir := flag.String("out-dir", "testdata/fixtures/misc/captures",
		"Directory to write captured captures (a capture-{stamp}/ subdir is created here)")
	dataCount := flag.Int("data-count", 0,
		"Number of Bin/Tx/Data messages to capture into data/ (0 = skip data capture)")
	dataTimeout := flag.Duration("data-timeout", 0,
		"Time limit for collecting Data messages; 0 means wait until --data-count is reached")

	cfg := config.Load()

	opts := capture.Options{
		BrokerURI:   cfg.MQTTURI,
		ClientID:    cfg.MQTTClientID,
		MQTTUser:    cfg.MQTTUser,
		MQTTPass:    cfg.MQTTPass,
		SymbolTopic: cfg.SymbolTopic,
		DescTopic:   cfg.DescTopic,
		DataTopic:   cfg.DataTopic,
		BaseTopic:   cfg.TopicBase,
		Timeout:     *timeout,
		OutDir:      *outDir,
		DataCount:   *dataCount,
		DataTimeout: *dataTimeout,
	}

	result, err := capture.Run(context.Background(), opts)
	if err != nil {
		logger.Error("capture failed", "err", err)
		os.Exit(1)
	}

	if result.Fixture.ParseError != "" {
		fmt.Fprintf(os.Stderr,
			"\n⚠️  Parse error recorded in fixture. Fix the parser then re-run cmd/capture.\n"+
				"    Symbols YAML: %s\n\n", result.SymbolsYMLPath)
		os.Exit(1)
	}

	descNote := "    Desc:    not captured (Bin/Tx/Desc not received)\n"
	if result.DescYMLPath != "" {
		descNote = fmt.Sprintf("    Desc:    %s\n", result.DescYMLPath)
	}

	dataNote := ""
	if result.DataDir != "" {
		dataNote = fmt.Sprintf("    Data:    %s/ (%d messages)\n",
			result.DataDir, result.DataCount)
	}

	fmt.Printf("\n✅  Captured %d symbols.\n"+
		"    Capture:      %s\n"+
		"    Symbols YAML: %s\n"+
		"%s"+
		"%s"+
		"\n"+
		"    Edit %s to verify symbol entries.\n\n",
		result.Fixture.NumSymbols, result.CaptureDir, result.SymbolsYMLPath,
		descNote, dataNote, result.SymbolsYMLPath)
}

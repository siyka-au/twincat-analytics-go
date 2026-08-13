// Package fixture provides types and helpers for TwinCAT Analytics test fixtures.
//
// Each named fixture lives under testdata/fixtures/{Name}/ with this layout:
//
//	fixture.yml                       — fixture-level metadata (twincat_project_url)
//	plc/                              — PLC source files (.prg.st, .dut.st)
//	captures/
//	  capture-{stamp}/
//	    capture.yml                   — capture-session context (broker, client, timestamps)
//	    symbols/
//	      message-{timestamp}.bin     — raw Symbols wire payload (source of truth)
//	      message-{timestamp}.yml     — SymbolFixture sidecar (MQTT ctx + parsed content)
//	    desc/
//	      message-{timestamp}.json    — raw Desc JSON payload
//	      message-{timestamp}.yml     — DescFixture sidecar (MQTT ctx + hash)
//	    data/
//	      message-{timestamp}.bin     — raw Data payloads (no YAML sidecar)
//
// The SHA-256 hash of the Symbols binary is recorded in the SymbolFixture YAML
// so that LoadWithVerify can detect accidental file modifications.
package fixture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// CaptureDefaults holds fixture-level default capture parameters used by
// cmd/runner when the corresponding flag was not passed on the command line.
// Duration fields use Go duration syntax (e.g. "60s", "2m").
type CaptureDefaults struct {
	// Timeout overrides --timeout for this fixture.
	Timeout string `yaml:"timeout,omitempty"`
	// DataCount overrides --data-count for this fixture.
	DataCount int `yaml:"data_count,omitempty"`
	// DataTimeout overrides --data-timeout for this fixture.
	DataTimeout string `yaml:"data_timeout,omitempty"`
}

// StreamConfig describes one MQTT stream within a fixture. Every fixture uses
// the streams: list in fixture.yml — single-stream fixtures have one entry,
// multi-stream fixtures (e.g. InterleaveTest) have one entry per PLC.
type StreamConfig struct {
	// PLCLabel is a human-readable label for this PLC, e.g. "PLC 1".
	// Omit for single-stream fixtures.
	PLCLabel string `yaml:"plc_label,omitempty"`
	// BaseTopic is the MQTT base topic for this stream.
	BaseTopic string `yaml:"base_topic"`
	// Sentinel is the ADS symbol name to verify the correct program is running.
	Sentinel string `yaml:"sentinel,omitempty"`
	// Program is the PLC program name (without .prg.st) for display in the wizard.
	Program string `yaml:"program,omitempty"`
}

// FixtureMeta is the fixture-level metadata stored in fixture.yml at the
// fixture root (testdata/fixtures/{Name}/). It records information shared
// across all captures of this fixture.
type FixtureMeta struct {
	// TwinCATProjectURL is the URL of the TwinCAT project used for this fixture.
	// Example: "https://github.com/siyka-au/twincat-analytics-test-projects/tree/master/SimpleTypeTest"
	TwinCATProjectURL string `yaml:"twincat_project_url,omitempty"`

	// Notes is a free-text description of the fixture's purpose.
	Notes string `yaml:"notes,omitempty"`

	// Streams lists per-stream configuration. Single-stream fixtures have one
	// entry; multi-stream fixtures (e.g. InterleaveTest) have one per PLC.
	// The first stream's base_topic can be overridden with --base-topic.
	Streams []StreamConfig `yaml:"streams,omitempty"`

	// CaptureDefaults provides per-fixture default values for cmd/runner flags.
	// Flags passed explicitly on the command line always take precedence.
	CaptureDefaults *CaptureDefaults `yaml:"capture_defaults,omitempty"`
}

// CaptureMeta is the capture-session context stored in capture.yml inside each
// capture-{stamp}/ directory. It records the broker connection details and
// identification for a single capture run.
type CaptureMeta struct {
	// CapturedAt is the UTC capture timestamp in RFC 3339 format.
	CapturedAt string `yaml:"captured_at"`

	// BrokerURI is the MQTT broker URI used during capture.
	// Example: "tcp://127.0.0.1:1883"
	BrokerURI string `yaml:"broker_uri"`

	// ClientID is the MQTT client ID used during capture.
	ClientID string `yaml:"client_id"`

	// --- Broker identification (populated by internal/brokerinfo.Probe) ---
	// See docs/BROKERS.md for broker-specific $SYS topic formats.

	// BrokerType is the detected broker family, e.g. "mosquitto", "emqx".
	BrokerType string `yaml:"broker_type"`

	// BrokerVersion is the raw version string from $SYS/broker/version.
	BrokerVersion string `yaml:"broker_version"`

	// BrokerBuildTimestamp is the build date string from $SYS/broker/timestamp.
	BrokerBuildTimestamp string `yaml:"broker_build_timestamp"`

	// BaseTopic is the MQTT base topic used during capture (e.g. "Devices/SimpleTypeTest").
	BaseTopic string `yaml:"base_topic,omitempty"`
}

// SymbolFixture is the per-message sidecar YAML for one symbols/message-*.bin
// file. It records MQTT message context, the parsed stream header, and the
// expected per-symbol values. The paired binary is the source of truth;
// re-run cmd/capture to regenerate if needed.
//
// TODO: data_types block — will be added once DataType parsing is complete.
type SymbolFixture struct {
	// --- MQTT message context ---

	// Topic is the full MQTT topic on which the Symbols message was received.
	Topic string `yaml:"topic"`

	// QoS is the MQTT QoS level of the received Symbols message (0, 1, or 2).
	QoS byte `yaml:"qos"`

	// Retained indicates whether the broker delivered this as a retained message.
	Retained bool `yaml:"retained"`

	// MQTTMessageID is the MQTT packet identifier from the PUBLISH packet.
	// Only meaningful for QoS > 0; zero for QoS 0 messages.
	MQTTMessageID uint16 `yaml:"mqtt_message_id"`

	// PayloadSHA256 is the hex-encoded SHA-256 hash of the paired .bin payload.
	// LoadWithVerify uses this to detect accidental binary file modifications.
	PayloadSHA256 string `yaml:"payload_sha256"`

	// --- Parse outcome ---

	// ParseError is non-empty if the parser failed on this binary.
	// The .bin file is always preserved — fix the parser and re-run cmd/capture.
	ParseError string `yaml:"parse_error,omitempty"`

	// --- Stream header (populated on successful parse) ---

	MajorVersion uint8                `yaml:"major_version,omitempty"`
	MinorVersion uint8                `yaml:"minor_version,omitempty"`
	NumSymbols   uint32               `yaml:"num_symbols,omitempty"`
	CodePage     uint32               `yaml:"code_page,omitempty"`
	Flags        *StreamFlagsMetadata `yaml:"flags,omitempty"`

	// --- Symbol expectations ---

	// Entries are the expected per-symbol values.
	//
	// DataType is stored as a raw integer (the TwincatIotSymbolStream_AdsDataType
	// enum value) to keep the fixture format independent of the generated parser.
	// Common values:
	//
	//	 0 = Void,    2 = Int16,   3 = Int32,   4 = Real32,  5 = Real64
	//	16 = Int8,   17 = Uint8,  18 = Uint16, 19 = Uint32
	//	20 = Int64,  21 = Uint64, 30 = String, 31 = WString
	//	33 = Bit,    65 = BigType (struct / complex type)
	//
	// TODO: Change DataType to a human-readable string enum once the full mapping is validated.
	Entries []SymbolEntry `yaml:"entries"`
}

// DescFixture is the per-message sidecar YAML for one desc/message-*.json file.
// It records MQTT message context and a hash of the raw JSON payload.
type DescFixture struct {
	// Topic is the full MQTT topic on which the Desc message was received.
	Topic string `yaml:"topic"`

	// QoS is the MQTT QoS level of the received Desc message.
	QoS byte `yaml:"qos"`

	// Retained indicates whether the broker delivered this as a retained message.
	Retained bool `yaml:"retained"`

	// MQTTMessageID is the MQTT packet identifier from the PUBLISH packet.
	MQTTMessageID uint16 `yaml:"mqtt_message_id"`

	// PayloadSHA256 is the hex-encoded SHA-256 hash of the paired .json payload.
	PayloadSHA256 string `yaml:"payload_sha256"`
}

// StreamFlagsMetadata mirrors the stream header flags bitfield.
type StreamFlagsMetadata struct {
	IsOnlineChange       bool `yaml:"is_online_change"`
	IsTarget64Bit        bool `yaml:"is_target_64bit"`
	AreBaseTypesIncluded bool `yaml:"are_base_types_included"`
}

// SymbolEntry is one expected symbol in the fixture.
type SymbolEntry struct {
	Name        string `yaml:"name"`
	TypeName    string `yaml:"type_name"`
	DataType    int    `yaml:"data_type"`
	IndexOffset uint32 `yaml:"index_offset"`
	Size        uint32 `yaml:"size"`
}

const symbolsFileHeader = `# Auto-generated by cmd/capture — edit symbol entries to correct parser output.
# Paired binary: the .bin file with the same base name in this directory.
# The .bin is the source of truth; re-run cmd/capture to regenerate.

`

const descFileHeader = `# Auto-generated by cmd/capture.
# Paired JSON payload: the .json file with the same base name in this directory.

`

const captureFileHeader = `# Auto-generated by cmd/capture — records the capture session context.

`

const fixtureMetaFileHeader = `# Fixture-level metadata shared across all captures.
# Set twincat_project_url to the URL of the TwinCAT project used for this fixture.

`

// Write marshals f to a human-readable YAML file at path (symbols/message-*.yml).
func Write(path string, f *SymbolFixture) error {
	var buf bytes.Buffer
	buf.WriteString(symbolsFileHeader)
	data, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("fixture: yaml marshal: %w", err)
	}
	buf.Write(data)
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// Load reads a SymbolFixture from the YAML file at path without verifying
// the binary payload hash. Use LoadWithVerify for hash-verified loading.
func Load(path string) (*SymbolFixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fixture: read %s: %w", path, err)
	}
	var f SymbolFixture
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("fixture: yaml unmarshal %s: %w", path, err)
	}
	return &f, nil
}

// LoadWithVerify reads a SymbolFixture from the YAML file at path and verifies
// that the sibling .bin file exists and its SHA-256 hash matches PayloadSHA256.
//
// The paired binary has the same base name as the YAML file:
//
//	symbols/message-20260225T054906.000000000.yml
//	  →  symbols/message-20260225T054906.000000000.bin
//
// Returns the parsed fixture and the raw binary payload on success.
func LoadWithVerify(path string) (*SymbolFixture, []byte, error) {
	f, err := Load(path)
	if err != nil {
		return nil, nil, err
	}

	binPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".bin"
	binData, err := os.ReadFile(binPath)
	if err != nil {
		return nil, nil, fmt.Errorf("fixture: paired binary not found at %s: %w", binPath, err)
	}

	if f.PayloadSHA256 != "" {
		sum := sha256.Sum256(binData)
		actual := hex.EncodeToString(sum[:])
		if actual != f.PayloadSHA256 {
			return nil, nil, fmt.Errorf(
				"fixture: SHA-256 mismatch for %s\n  expected: %s\n  actual:   %s\n"+
					"  The .bin file may have been modified. Re-run cmd/capture to regenerate.",
				binPath, f.PayloadSHA256, actual,
			)
		}
	}

	return f, binData, nil
}

// WriteDescFixture marshals d to a human-readable YAML file at path (desc/message-*.yml).
func WriteDescFixture(path string, d *DescFixture) error {
	var buf bytes.Buffer
	buf.WriteString(descFileHeader)
	data, err := yaml.Marshal(d)
	if err != nil {
		return fmt.Errorf("fixture: yaml marshal desc: %w", err)
	}
	buf.Write(data)
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// LoadDescFixture reads a DescFixture from the YAML file at path.
func LoadDescFixture(path string) (*DescFixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fixture: read desc %s: %w", path, err)
	}
	var d DescFixture
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("fixture: yaml unmarshal desc %s: %w", path, err)
	}
	return &d, nil
}

// WriteCaptureMeta marshals m to capture.yml at path.
func WriteCaptureMeta(path string, m *CaptureMeta) error {
	var buf bytes.Buffer
	buf.WriteString(captureFileHeader)
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("fixture: yaml marshal capture meta: %w", err)
	}
	buf.Write(data)
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// LoadCaptureMeta reads a CaptureMeta from the YAML file at path.
func LoadCaptureMeta(path string) (*CaptureMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fixture: read capture meta %s: %w", path, err)
	}
	var m CaptureMeta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("fixture: yaml unmarshal capture meta %s: %w", path, err)
	}
	return &m, nil
}

// WriteFixtureMeta marshals m to fixture.yml at path.
func WriteFixtureMeta(path string, m *FixtureMeta) error {
	var buf bytes.Buffer
	buf.WriteString(fixtureMetaFileHeader)
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("fixture: yaml marshal fixture meta: %w", err)
	}
	buf.Write(data)
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// LoadFixtureMeta reads a FixtureMeta from the YAML file at path.
func LoadFixtureMeta(path string) (*FixtureMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fixture: read fixture meta %s: %w", path, err)
	}
	var m FixtureMeta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("fixture: yaml unmarshal fixture meta %s: %w", path, err)
	}
	return &m, nil
}

// SHA256Hex returns the hex-encoded SHA-256 hash of data.
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

package layout_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
	"time"

	"github.com/siyka-au/twincat-analytics-go/layout"
)

// ── HeadTimestamp ─────────────────────────────────────────────────────────────

func TestParseDataMessage_HeadTimestamp(t *testing.T) {
	const dataLen = 4
	// flags: HeadTimestamp (bit 0 = 0x01); no per-sample timestamp
	hdr := makeDataHeader(1, 0, 32, 0, dataLen, 1000, 0x01, layout.GUID{})

	const headFT uint64 = 132_000_000_000_000_000 // known Windows FILETIME

	sampleRegion := make([]byte, 8+dataLen)
	binary.LittleEndian.PutUint64(sampleRegion[0:8], headFT)
	binary.LittleEndian.PutUint32(sampleRegion[8:12], 99)

	payload := append(hdr, sampleRegion...)
	msg, err := layout.ParseDataMessage(payload)
	if err != nil {
		t.Fatalf("ParseDataMessage: %v", err)
	}
	if msg.HeadTimestamp == nil || msg.HeadTimestamp.IsZero() {
		t.Fatal("HeadTimestamp should be non-nil and non-zero")
	}
	if msg.Samples[0].Timestamp != nil {
		t.Error("sample should not carry its own timestamp when only HeadTimestamp is set")
	}
	if len(msg.Samples) != 1 {
		t.Fatalf("samples: got %d, want 1", len(msg.Samples))
	}
	if v := binary.LittleEndian.Uint32(msg.Samples[0].Raw); v != 99 {
		t.Errorf("sample 0 data: got %d, want 99", v)
	}
}

// ── DC Time ───────────────────────────────────────────────────────────────────

func TestParseDataMessage_DCTime(t *testing.T) {
	const dataLen = 4
	const sampleHdrLen = 8
	// flags: SampleTimestamp (0x02) | DCTime (0x04)
	hdr := makeDataHeader(1, 0, 32, sampleHdrLen, dataLen, 0, 0x06, layout.GUID{})

	// DC time: 1 second after 2000-01-01 = 1,000,000,000 ns.
	const oneSec int64 = 1_000_000_000

	payload := append(hdr, make([]byte, sampleHdrLen+dataLen)...)
	binary.LittleEndian.PutUint64(payload[32:40], uint64(oneSec))

	msg, err := layout.ParseDataMessage(payload)
	if err != nil {
		t.Fatalf("ParseDataMessage: %v", err)
	}
	if len(msg.Samples) != 1 {
		t.Fatalf("samples: got %d, want 1", len(msg.Samples))
	}
	ts := msg.Samples[0].Timestamp
	if ts == nil || ts.IsZero() {
		t.Fatal("expected non-zero per-sample timestamp")
	}
	want := time.Date(2000, 1, 1, 0, 0, 1, 0, time.UTC)
	if !ts.Equal(want) {
		t.Errorf("DC timestamp: got %v, want %v", ts, want)
	}
}

func TestParseDataMessage_DCTimeZero(t *testing.T) {
	const sampleHdrLen = 8
	const dataLen = 4
	// DCTime flag with timestamp value 0 should produce zero time.Time.
	hdr := makeDataHeader(1, 0, 32, sampleHdrLen, dataLen, 0, 0x06, layout.GUID{})

	payload := append(hdr, make([]byte, sampleHdrLen+dataLen)...)
	// timestamp bytes remain zero

	msg, err := layout.ParseDataMessage(payload)
	if err != nil {
		t.Fatalf("ParseDataMessage: %v", err)
	}
	ts := msg.Samples[0].Timestamp
	if ts != nil && !ts.IsZero() {
		t.Errorf("DC timestamp 0 should produce zero time.Time, got %v", ts)
	}
}

// ── CycleTimeNs (v1.2) ────────────────────────────────────────────────────────

func TestParseDataMessage_CycleTimeNs(t *testing.T) {
	hdr := make([]byte, 64)
	hdr[0] = 1
	hdr[1] = 2
	hdr[2] = 64                                  // len_header
	hdr[3] = 0                                   // len_sample_header
	binary.LittleEndian.PutUint32(hdr[4:8], 4)   // len_data
	binary.LittleEndian.PutUint64(hdr[32:40], 1) // sample_count = 1

	const wantNs int64 = 500_000 // 500 µs
	binary.LittleEndian.PutUint64(hdr[56:64], uint64(wantNs))

	payload := append(hdr, make([]byte, 4)...)
	msg, err := layout.ParseDataMessage(payload)
	if err != nil {
		t.Fatalf("ParseDataMessage v1.2: %v", err)
	}
	if msg.Header.CycleTimeNs == nil {
		t.Fatal("CycleTimeNs should not be nil for LenHeader=64")
	}
	if *msg.Header.CycleTimeNs != wantNs {
		t.Errorf("CycleTimeNs: got %d, want %d", *msg.Header.CycleTimeNs, wantNs)
	}
}

// TestParseDataMessage_CycleTimeUnits pins down the CycleTime unit against a
// real capture, resolving a documented contradiction: docs/FORMAT_ANALYSIS.md
// used to claim CycleTime is milliseconds (needing a *10_000 conversion to
// FILETIME/100ns ticks), while this package's own comments always treated it
// as already being in 100ns ticks. Verified 2026-08-12 against 300 captured
// v1.2 messages across 3 fixtures: CycleTimeNs == CycleTime*100 in every one,
// which is only consistent with the 100ns-tick interpretation. This test
// exercises one representative capture so a future regression is caught.
func TestParseDataMessage_CycleTimeUnits(t *testing.T) {
	payload, err := os.ReadFile("../testdata/fixtures/SimpleTypeTest/captures/capture-202602270534/data/message-20260227T053426.108212609.bin")
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	msg, err := layout.ParseDataMessage(payload)
	if err != nil {
		t.Fatalf("ParseDataMessage: %v", err)
	}
	if msg.Header.CycleTimeNs == nil {
		t.Fatal("expected v1.2 capture to carry CycleTimeNs")
	}
	if got, want := *msg.Header.CycleTimeNs, int64(msg.Header.CycleTime)*100; got != want {
		t.Errorf("CycleTimeNs = %d, want CycleTime*100 = %d (CycleTime=%d)",
			got, want, msg.Header.CycleTime)
	}
}

// ── Flags ─────────────────────────────────────────────────────────────────────

func TestParseDataMessage_SupportSampleEventbased_Flags(t *testing.T) {
	// SupportSample = bit 8 (0x100), Eventbased = bit 9 (0x200).
	hdr := makeDataHeader(1, 0, 32, 0, 0, 0, 0x300, layout.GUID{})
	msg, err := layout.ParseDataMessage(hdr)
	if err != nil {
		t.Fatalf("ParseDataMessage: %v", err)
	}
	if !msg.Header.Flags.SupportSample {
		t.Error("SupportSample flag should be true")
	}
	if !msg.Header.Flags.Eventbased {
		t.Error("Eventbased flag should be true")
	}
}

// ── Run-Length Compression ────────────────────────────────────────────────────

// appendMarker appends a little-endian int16 marker to b.
func appendMarker(b []byte, v int16) []byte {
	u := uint16(v)
	return append(b, byte(u), byte(u>>8))
}

// TestParseDataMessage_RLCompression verifies the run-length delta decoder
// end-to-end through ParseDataMessage.
//
// Four samples, dataLen=4:
//
// Sample 0: raw.                                        → [01 02 03 04]
// Sample 1: copy run (marker=+4): all 4 from prev.      → [01 02 03 04]
// Sample 2: literal -2 [AA BB] + copy +2.               → [AA BB 03 04]
// Sample 3: support sample (marker=0) + full snapshot.  → [10 20 30 40]
func TestParseDataMessage_RLCompression(t *testing.T) {
	const dataLen = 4

	// 56-byte header: SampleCount=4, CompressionMethod=1 (flags=0x10).
	hdr := make([]byte, 56)
	hdr[0] = 1
	hdr[1] = 0
	hdr[2] = 56 // len_header
	hdr[3] = 0  // len_sample_header (no per-sample timestamp)
	binary.LittleEndian.PutUint32(hdr[4:8], dataLen)
	binary.LittleEndian.PutUint32(hdr[8:12], 1000)
	binary.LittleEndian.PutUint32(hdr[12:16], 0x10) // CompressionMethod=1
	binary.LittleEndian.PutUint64(hdr[32:40], 4)    // sample_count = 4

	var s []byte
	// Sample 0: raw
	s = append(s, 0x01, 0x02, 0x03, 0x04)
	// Sample 1: copy run marker=+4
	s = appendMarker(s, 4)
	// Sample 2: literal -2 then copy +2
	s = appendMarker(s, -2)
	s = append(s, 0xAA, 0xBB)
	s = appendMarker(s, 2)
	// Sample 3: support sample
	s = appendMarker(s, 0)
	s = append(s, 0x10, 0x20, 0x30, 0x40)

	payload := append(hdr, s...)
	msg, err := layout.ParseDataMessage(payload)
	if err != nil {
		t.Fatalf("ParseDataMessage compressed: %v", err)
	}
	if len(msg.Samples) != 4 {
		t.Fatalf("samples: got %d, want 4", len(msg.Samples))
	}

	want := [][]byte{
		{0x01, 0x02, 0x03, 0x04},
		{0x01, 0x02, 0x03, 0x04},
		{0xAA, 0xBB, 0x03, 0x04},
		{0x10, 0x20, 0x30, 0x40},
	}
	for i, w := range want {
		if !bytes.Equal(msg.Samples[i].Raw, w) {
			t.Errorf("sample %d: got %v, want %v", i, msg.Samples[i].Raw, w)
		}
	}
}

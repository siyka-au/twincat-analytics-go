package layout_test

// TestSimpleTypeTestCapture is a once-off integration test that validates
// the full parse pipeline against the real Bin/Tx/Data capture stored in
// internal/parser/testdata/capture-202602231300.
//
// The capture was generated from plc/SimpleTypeTest.st, which declares one
// variable for every ADS/IEC scalar type with known initialisation values.
// This test verifies that each decoded FieldValue matches those values exactly.
//
// To update the capture, run:
//
//	go run ./cmd/capture \
//	  --base-topic analytics-test/cyclic-stream-test \
//	  --data-count 1 --data-timeout 60s \
//	  --project-url <url-to-SimpleTypeTest.st>
//
// then update captureDir below to point to the new capture-{stamp}/ directory.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/siyka-au/twincat-analytics-go/internal/fixture"
	"github.com/siyka-au/twincat-analytics-go/layout"
	"github.com/siyka-au/twincat-analytics-go/parser"
)

// captureDir is the capture directory to test against, relative to this
// package. Update this constant when re-capturing.
const captureDir = "../testdata/fixtures/SimpleTypeTest/captures/capture-202602270534"

func TestSimpleTypeTestCapture(t *testing.T) {
	// ── Load symbols fixture + verify binary hash ──────────────────────────
	f, binData, err := fixture.LoadWithVerify(filepath.Join(captureDir, "symbols", "message-20260227T053425.947909832.yml"))
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	if f.ParseError != "" {
		t.Skipf("fixture records a parse error: %s", f.ParseError)
	}

	// ── Build layout from symbol stream ───────────────────────────────────
	stream, err := parser.ParseSymbolStream(binData)
	if err != nil {
		t.Fatalf("parse symbols: %v", err)
	}
	l := layout.NewLayoutFromStream(stream)
	t.Logf("layout GUID=%s  fields=%d  sampleDataSize=%d", l.GUID, len(l.Fields), l.SampleDataSize)

	// ── Locate data message files ─────────────────────────────────────────
	dataDir := filepath.Join(captureDir, "data")
	msgPaths, err := filepath.Glob(filepath.Join(dataDir, "*.bin"))
	if err != nil {
		t.Fatalf("glob data dir: %v", err)
	}
	if len(msgPaths) == 0 {
		t.Fatalf("no *.bin files found in %s", dataDir)
	}
	t.Logf("found %d data message(s) in %s", len(msgPaths), dataDir)

	// ── Run sub-test per captured message ─────────────────────────────────
	for _, msgPath := range msgPaths {
		msgPath := msgPath
		t.Run(filepath.Base(msgPath), func(t *testing.T) {
			validateDataMessage(t, msgPath, l)
		})
	}
}

// validateDataMessage parses one Bin/Tx/Data binary and asserts decoded
// field values against the expected PLC initialisation values from
// plc/SimpleTypeTest.st.
func validateDataMessage(t *testing.T, msgPath string, l *layout.Layout) {
	t.Helper()

	payload, err := os.ReadFile(msgPath)
	if err != nil {
		t.Fatalf("read %s: %v", msgPath, err)
	}

	msg, err := layout.ParseDataMessage(payload)
	if err != nil {
		t.Fatalf("ParseDataMessage: %v", err)
	}

	t.Logf("v%d.%d  lenHeader=%d  lenSampleHeader=%d  lenData=%d  sampleTimestamp=%v  samples=%d",
		msg.Header.MajorVersion, msg.Header.MinorVersion,
		msg.Header.LenHeader, msg.Header.LenSampleHeader,
		msg.Header.LenData, msg.Header.Flags.SampleTimestamp,
		len(msg.Samples))

	if msg.Header.SampleCount != nil {
		t.Logf("sampleCount (from header)=%d", *msg.Header.SampleCount)
	}

	// GUID in the data message must match the layout derived from symbols.
	if msg.Header.LayoutGUID != l.GUID {
		t.Errorf("GUID mismatch\n  data message: %s\n  layout:       %s",
			msg.Header.LayoutGUID, l.GUID)
	}

	if len(msg.Samples) == 0 {
		t.Fatal("no samples parsed from data message")
	}

	// Decode the first (and in this capture, only) sample.
	fvs := l.ParseSample(msg.Samples[0].Raw)
	if len(fvs) == 0 {
		t.Fatal("ParseSample returned no field values")
	}
	t.Logf("decoded %d fields from sample[0]", len(fvs))

	byName := make(map[string]any, len(fvs))
	for _, fv := range fvs {
		byName[fv.Field.Name] = fv.Value
	}

	// ── Assert PLC initialisation values ──────────────────────────────────
	// Source: plc/SimpleTypeTest.st — values are the VAR block initialisers.

	// Boolean — AdsDataType = Bit (33) → bool
	assertField(t, byName, "SimpleTypeTest.var_bool", true)

	// Signed integers
	assertField(t, byName, "SimpleTypeTest.var_sint", int8(-100))
	assertField(t, byName, "SimpleTypeTest.var_int", int16(-1000))
	assertField(t, byName, "SimpleTypeTest.var_dint", int32(-100000))
	assertField(t, byName, "SimpleTypeTest.var_lint", int64(-1000000000))

	// Unsigned integers
	assertField(t, byName, "SimpleTypeTest.var_usint", uint8(200))
	assertField(t, byName, "SimpleTypeTest.var_uint", uint16(60000))
	assertField(t, byName, "SimpleTypeTest.var_udint", uint32(3000000000))
	assertField(t, byName, "SimpleTypeTest.var_ulint", uint64(10000000000))

	// Bit-string types — analytics agent reports them as the matching
	// unsigned integer AdsDataType (BYTE→Uint8, WORD→Uint16, etc.)
	assertField(t, byName, "SimpleTypeTest.var_byte", uint8(0xAB))
	assertField(t, byName, "SimpleTypeTest.var_word", uint16(0xABCD))
	assertField(t, byName, "SimpleTypeTest.var_dword", uint32(0xDEADBEEF))
	assertField(t, byName, "SimpleTypeTest.var_lword", uint64(0xDEADBEEFCAFEBABE))

	// Floating point — bit-identical comparison is safe here since the PLC
	// stores standard IEEE 754 representations of these constants.
	assertField(t, byName, "SimpleTypeTest.var_real", float32(3.14))
	assertField(t, byName, "SimpleTypeTest.var_lreal", float64(3.141592653589793))

	// Strings (AdsDataType = String → null-terminated UTF-8)
	assertField(t, byName, "SimpleTypeTest.var_string", "Hello TwinCAT")
	assertField(t, byName, "SimpleTypeTest.var_string_short", "Hi")
	assertField(t, byName, "SimpleTypeTest.var_string_long",
		"The quick brown fox jumps over the lazy dog. Extra padding to stress-test long string parsing in the binary symbol stream.")

	// Wide strings (AdsDataType = WString → UCS-2 LE, null-terminated)
	assertField(t, byName, "SimpleTypeTest.var_wstring", "Hello TwinCAT Wide")
	assertField(t, byName, "SimpleTypeTest.var_wstring_short", "Hi Wide")
	assertField(t, byName, "SimpleTypeTest.var_wstring_long",
		"The quick brown fox jumps over the lazy dog in wide-string format. Testing WSTRING parser alignment across multiple lengths.")

	// Temporal types — decoded by TypeName dispatch.
	// T#1S500MS = 1500 ms
	assertField(t, byName, "SimpleTypeTest.var_time", time.Duration(1500)*time.Millisecond)
	// D#2026-02-23 — seconds since 1970-01-01, truncated to midnight, stored as uint32.
	assertField(t, byName, "SimpleTypeTest.var_date", time.Date(2026, 2, 23, 0, 0, 0, 0, time.UTC))
	// DT#2026-02-23-10:30:00
	assertField(t, byName, "SimpleTypeTest.var_date_and_time", time.Date(2026, 2, 23, 10, 30, 0, 0, time.UTC))
	// TOD#10:30:00.000 = 37800000 ms
	assertField(t, byName, "SimpleTypeTest.var_time_of_day", 10*time.Hour+30*time.Minute)
	// LTIME#1D2H3M4S5MS = 26h3m4.005s
	assertField(t, byName, "SimpleTypeTest.var_ltime", 26*time.Hour+3*time.Minute+4*time.Second+5*time.Millisecond)

	// ── Arrays ───────────────────────────────────────────────────────────────
	// Static 4-element arrays spanning each type's range (min, -1, 0, max).
	// Exercises the same per-element array decode as ArrayAccessTest, on a
	// wider set of element types (bool/int/uint/float/string/wstring/time).
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_bool", []any{true, false, true, false})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_sint", []any{int8(-128), int8(-1), int8(0), int8(127)})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_int", []any{int16(-32768), int16(-1), int16(0), int16(32767)})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_dint", []any{int32(-2147483648), int32(-1), int32(0), int32(2147483647)})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_lint", []any{int64(-1000000000), int64(-1), int64(0), int64(1000000000)})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_usint", []any{uint8(0), uint8(1), uint8(127), uint8(255)})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_uint", []any{uint16(0), uint16(1), uint16(32768), uint16(65535)})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_udint", []any{uint32(0), uint32(1), uint32(2147483648), uint32(4294967295)})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_ulint", []any{uint64(0), uint64(1), uint64(1000000000), uint64(18000000000)})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_byte", []any{uint8(0x00), uint8(0x55), uint8(0xAA), uint8(0xFF)})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_word", []any{uint16(0x0000), uint16(0x1234), uint16(0xABCD), uint16(0xFFFF)})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_dword", []any{uint32(0x00000000), uint32(0x12345678), uint32(0xDEADBEEF), uint32(0xFFFFFFFF)})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_lword", []any{uint64(0x0), uint64(0x123456789ABCDEF0), uint64(0xDEADBEEFCAFEBABE), uint64(0xFFFFFFFFFFFFFFFF)})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_real", []any{float32(0.0), float32(1.0), float32(-1.0), float32(3.14)})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_lreal", []any{float64(0.0), float64(1.0), float64(-1.0), float64(3.141592653589793)})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_string", []any{"Alpha", "Beta", "Gamma", "Delta"})
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_wstring", []any{"Alpha Wide", "Beta Wide", "Gamma Wide", "Delta Wide"})
	// T#0S, T#1S, T#1M, T#1H — TIME arrays decode via the same TypeName
	// dispatch as scalar TIME fields (element type name "TIME").
	assertArrayField(t, byName, "SimpleTypeTest.var_array_of_time",
		[]any{time.Duration(0), time.Second, time.Minute, time.Hour})
}

// assertArrayField checks that byName[name] is a []any equal element-wise to
// want. A dedicated helper because []any isn't comparable with != the way
// assertField's scalar comparison works.
func assertArrayField(t *testing.T, byName map[string]any, name string, want []any) {
	t.Helper()
	got, ok := byName[name].([]any)
	if !ok {
		t.Errorf("field %q: got %T, want []any", name, byName[name])
		return
	}
	if len(got) != len(want) {
		t.Errorf("field %q: got %d elements, want %d", name, len(got), len(want))
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %q[%d]: got %v (%T), want %v (%T)", name, i, got[i], got[i], want[i], want[i])
		}
	}
}

// assertField checks that byName[name] is present and equals want.
// time.Time values are compared with Equal() to handle timezone equivalence.
func assertField(t *testing.T, byName map[string]any, name string, want any) {
	t.Helper()
	got, ok := byName[name]
	if !ok {
		t.Errorf("field %q: not present in decoded output", name)
		return
	}
	if wt, isTime := want.(time.Time); isTime {
		if gt, ok := got.(time.Time); !ok || !gt.Equal(wt) {
			t.Errorf("field %q value mismatch\n  want: %v  (%T)\n  got:  %v  (%T)",
				name, want, want, got, got)
		}
		return
	}
	if got != want {
		t.Errorf("field %q value mismatch\n  want: %v  (%T)\n  got:  %v  (%T)",
			name, want, want, got, got)
	}
}

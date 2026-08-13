package layout_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/siyka-au/twincat-analytics-go/internal/fixture"
	"github.com/siyka-au/twincat-analytics-go/layout"
	"github.com/siyka-au/twincat-analytics-go/parser"
)

// TestStructTypeTestCapture validates BigType struct/enum expansion (the
// TODO-015 mechanism) against every sample in a real capture ported from
// tcanalytics4j's StructTypeTest fixture — see testdata/fixtures/StructTypeTest/
// for provenance.
//
// Main.reading/Main.summary are fully formula-derived from Main.master_count
// (the seed) per plc/StructTypeTest.prg.st's documented derivation — verified
// this holds across the whole capture, not just the one sample checked
// originally (master_count increments every sample within this capture: 4226,
// 4227, ..., 4257 across the 32 samples).
//
// Main.some_enum/Main.anon_enum are NOT checked against master_count by a
// formula: the checked-in plc/StructTypeTest.prg.st doesn't declare these
// variables at all (a pre-existing staleness gap — the capture was taken from
// a program version with two extra enum test variables never reflected in the
// committed source), so there is no documented derivation to verify against,
// and reverse-engineering one from the limited samples in this single capture
// risks asserting a coincidence rather than the real rule (a quick check
// across 5 samples showed no simple pattern tied to master_count parity or
// modulus). Instead, each sample's enum decode is checked for *internal
// consistency*: the "value"/"name" pair must match the real parsed enum table
// (SomeEnum, Implicit_Enum__Main__anon_enum — independently confirmed correct
// against tcanalytics4j's Java output in typemetadata_test.go), which is what
// actually exercises the decode mechanism this test exists to validate.
const structCaptureDir = "../testdata/fixtures/StructTypeTest/captures/capture-20260623T0456"

// someEnumValues and anonEnumValues mirror the real parsed enum tables from
// this capture's type metadata (see typemetadata_test.go, which independently
// verifies Fluffy=1 and Dongers=1 are present in the parsed output). Kept
// here as a second, hand-transcribed source so a bug in the parser producing
// a self-consistent-but-wrong table wouldn't pass both tests for the same
// wrong reason.
var (
	someEnumValues = map[int64]string{0: "Hairy", 1: "Fluffy", 2: "Smooth", 3: "Prickly", 4: "Decomposed", 999: "FoxMulder"}
	anonEnumValues = map[int64]string{0: "Dingers", 1: "Dongers", 2: "Clangers", 3: "Bangers", 4: "Twangers", 5: "Scully"}
)

func TestStructTypeTestCapture(t *testing.T) {
	f, binData, err := fixture.LoadWithVerify(filepath.Join(structCaptureDir, "symbols", "message-20260623T045600.yml"))
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	if f.ParseError != "" {
		t.Skipf("fixture records a parse error: %s", f.ParseError)
	}

	stream, err := parser.ParseSymbolStream(binData)
	if err != nil {
		t.Fatalf("parse symbols: %v", err)
	}
	l := layout.NewLayoutFromStream(stream)
	t.Logf("layout GUID=%s fields=%d sampleDataSize=%d", l.GUID, len(l.Fields), l.SampleDataSize)

	if !l.HasTypeMetadata() {
		t.Fatal("expected HasTypeMetadata() to be true for a struct-carrying capture")
	}

	dataPath := filepath.Join(structCaptureDir, "data", "message-20260623T045611.bin")
	payload, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("read %s: %v", dataPath, err)
	}
	msg, err := layout.ParseDataMessage(payload)
	if err != nil {
		t.Fatalf("ParseDataMessage: %v", err)
	}
	if msg.Header.LayoutGUID != l.GUID {
		t.Fatalf("GUID mismatch\n  data message: %s\n  layout:       %s", msg.Header.LayoutGUID, l.GUID)
	}
	if len(msg.Samples) == 0 {
		t.Fatal("no samples parsed from data message")
	}

	for si, sample := range msg.Samples {
		fvs := l.ParseSample(sample.Raw)
		byName := make(map[string]any, len(fvs))
		for _, fv := range fvs {
			byName[fv.Field.Name] = fv.Value
		}
		ctx := fmt.Sprintf("sample[%d]", si)

		masterCount, ok := byName["Main.master_count"].(uint32)
		if !ok {
			t.Fatalf("%s: master_count missing or wrong type: %#v", ctx, byName["Main.master_count"])
		}
		n := int32(masterCount)

		// ── Struct expansion, 2 levels deep — fully formula-derived ─────────
		// See plc/StructTypeTest.prg.st's determinism-contract comment block:
		//   raw=N, scaled=N/100, signed=N-500, flag=(N>100)
		//   label="Reading#N", inner.counter=signed^2, inner.active=(counter>0)
		//   summary=label+(" OK" if active else " ZERO")
		reading, ok := byName["Main.reading"].(map[string]any)
		if !ok {
			t.Fatalf("%s: Main.reading: got %T, want map[string]any", ctx, byName["Main.reading"])
		}
		signed := n - 500
		flag := masterCount > 100
		label := fmt.Sprintf("Reading#%d", masterCount)
		assertMapField(t, reading, "raw", masterCount)
		assertMapField(t, reading, "scaled", float32(masterCount)/100.0)
		assertMapField(t, reading, "signed", signed)
		assertMapField(t, reading, "flag", flag)
		assertMapField(t, reading, "label", label)

		inner, ok := reading["inner"].(map[string]any)
		if !ok {
			t.Fatalf("%s: Main.reading.inner: got %T, want map[string]any", ctx, reading["inner"])
		}
		counter := signed * signed
		active := counter > 0
		assertMapField(t, inner, "counter", counter)
		assertMapField(t, inner, "active", active)

		wantSummary := label + " ZERO"
		if active {
			wantSummary = label + " OK"
		}
		assertField(t, byName, "Main.summary", wantSummary)

		// ── Enum decoding — internal consistency, not formula-predicted ─────
		assertEnumField(t, ctx, byName, "Main.some_enum", "SomeEnum", someEnumValues)
		assertEnumField(t, ctx, byName, "Main.anon_enum", "Implicit_Enum__Main__anon_enum", anonEnumValues)
	}
}

// assertEnumField checks that byName[name] decodes to a map whose "type"
// matches wantType and whose "name" is the table entry for its "value".
func assertEnumField(t *testing.T, ctx string, byName map[string]any, name, wantType string, table map[int64]string) {
	t.Helper()
	m, ok := byName[name].(map[string]any)
	if !ok {
		t.Fatalf("%s: %s: got %T, want map[string]any", ctx, name, byName[name])
	}
	assertMapField(t, m, "type", wantType)

	value, ok := m["value"].(int64)
	if !ok {
		t.Fatalf("%s: %s.value: got %T, want int64", ctx, name, m["value"])
	}
	wantName, known := table[value]
	if !known {
		t.Errorf("%s: %s.value=%d is not in the known enum table %+v", ctx, name, value, table)
		return
	}
	assertMapField(t, m, "name", wantName)
}

// assertMapField checks that m[key] is present and equals want.
func assertMapField(t *testing.T, m map[string]any, key string, want any) {
	t.Helper()
	got, ok := m[key]
	if !ok {
		t.Errorf("key %q: not present in map %+v", key, m)
		return
	}
	if got != want {
		t.Errorf("key %q value mismatch\n  want: %v  (%T)\n  got:  %v  (%T)", key, want, want, got, got)
	}
}

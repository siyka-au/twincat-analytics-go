package layout_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/siyka-au/twincat-analytics-go/internal/fixture"
	"github.com/siyka-au/twincat-analytics-go/layout"
	"github.com/siyka-au/twincat-analytics-go/parser"
)

// TestArrayAccessTestCapture validates per-element array decoding against
// every sample in the real capture, deriving expected values from
// plc/ArrayAccessTest.prg.st's determinism contract rather than hardcoding
// them: master_count (published, UDINT) is the seed, and every other field
// is a pure function of it — so each sample can verify itself independently,
// with no dependency on capture ordering or sample count.
//
// This test caught a real bug during authoring: array-typed fields
// (var_array_int, var_array_real, var_array_string) previously decoded to
// just their first element instead of an array — DataType for an array
// field is the *element* type with Size = elementSize*count, and nothing
// checked TypeName for "ARRAY [...] OF ..." before falling into the scalar
// decode path. Fixed in layout.go (ParseArrayTypeName / decodeArray).
const arrayAccessCaptureDir = "../testdata/fixtures/ArrayAccessTest/captures/capture-202602270608"

func TestArrayAccessTestCapture(t *testing.T) {
	f, binData, err := fixture.LoadWithVerify(filepath.Join(arrayAccessCaptureDir, "symbols", "message-20260227T060837.593815923.yml"))
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

	dataDir := filepath.Join(arrayAccessCaptureDir, "data")
	msgPaths, err := filepath.Glob(filepath.Join(dataDir, "*.bin"))
	if err != nil {
		t.Fatalf("glob data dir: %v", err)
	}
	if len(msgPaths) == 0 {
		t.Fatalf("no *.bin files found in %s", dataDir)
	}

	wantStrings := []any{"Item0", "Item1", "Item2", "Item3", "Item4", "Item5", "Item6", "Item7"}

	totalSamples := 0
	for _, msgPath := range msgPaths {
		payload, err := os.ReadFile(msgPath)
		if err != nil {
			t.Fatalf("read %s: %v", msgPath, err)
		}
		msg, err := layout.ParseDataMessage(payload)
		if err != nil {
			t.Fatalf("ParseDataMessage(%s): %v", msgPath, err)
		}
		for si, sample := range msg.Samples {
			totalSamples++
			fvs := l.ParseSample(sample.Raw)
			byName := make(map[string]any, len(fvs))
			for _, fv := range fvs {
				byName[fv.Field.Name] = fv.Value
			}

			mc, ok := byName["ArrayAccessTest.master_count"].(uint32)
			if !ok {
				t.Fatalf("%s sample[%d]: master_count missing or wrong type: %#v",
					filepath.Base(msgPath), si, byName["ArrayAccessTest.master_count"])
			}
			n := mc

			// Level-1: var_array_int[i] = UDINT_TO_INT(N * (i+1)), natural
			// 16-bit two's-complement wraparound on overflow.
			gotInt, ok := byName["ArrayAccessTest.var_array_int"].([]any)
			if !ok || len(gotInt) != 16 {
				t.Fatalf("%s sample[%d]: var_array_int: got %#v, want 16-element []any",
					filepath.Base(msgPath), si, byName["ArrayAccessTest.var_array_int"])
			}
			wantIntSum := int32(0)
			for i := 0; i < 16; i++ {
				wantElem := int16(uint32(n) * uint32(i+1))
				if gotInt[i] != wantElem {
					t.Errorf("%s sample[%d]: var_array_int[%d] = %v, want %d (N=%d)",
						filepath.Base(msgPath), si, i, gotInt[i], wantElem, n)
				}
				wantIntSum += int32(wantElem)
			}

			// Level-1: var_array_real[i] = REAL(N) / REAL(i+1).
			gotReal, ok := byName["ArrayAccessTest.var_array_real"].([]any)
			if !ok || len(gotReal) != 16 {
				t.Fatalf("%s sample[%d]: var_array_real: got %#v, want 16-element []any",
					filepath.Base(msgPath), si, byName["ArrayAccessTest.var_array_real"])
			}
			for i := 0; i < 16; i++ {
				wantElem := float32(n) / float32(i+1)
				if gotReal[i] != wantElem {
					t.Errorf("%s sample[%d]: var_array_real[%d] = %v, want %v (N=%d)",
						filepath.Base(msgPath), si, i, gotReal[i], wantElem, n)
				}
			}

			// Level-2: sum_int = sum of the (possibly-wrapped) var_array_int
			// elements, matching the PLC's own derivation order — NOT a
			// shortcut N*136 formula, since that diverges once any element
			// wraps (which it does for large N in this capture: N*16
			// exceeds int16 range well within the ~193..3392 span here).
			assertField(t, byName, "ArrayAccessTest.sum_int", wantIntSum)

			// Level-2: max_real = var_array_real[0] by construction (divisor
			// 1 is always smallest, REAL has no overflow risk at this N).
			assertField(t, byName, "ArrayAccessTest.max_real", float32(n))

			// Static: var_array_string never mutates at runtime.
			gotStr, ok := byName["ArrayAccessTest.var_array_string"].([]any)
			if !ok || len(gotStr) != 8 {
				t.Fatalf("%s sample[%d]: var_array_string: got %#v, want 8-element []any",
					filepath.Base(msgPath), si, byName["ArrayAccessTest.var_array_string"])
			}
			for i := 0; i < 8; i++ {
				if gotStr[i] != wantStrings[i] {
					t.Errorf("%s sample[%d]: var_array_string[%d] = %v, want %v",
						filepath.Base(msgPath), si, i, gotStr[i], wantStrings[i])
				}
			}
		}
	}
	t.Logf("verified %d samples across %d messages", totalSamples, len(msgPaths))
}

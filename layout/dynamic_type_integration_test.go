package layout_test

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/siyka-au/twincat-analytics-go/internal/fixture"
	"github.com/siyka-au/twincat-analytics-go/layout"
	"github.com/siyka-au/twincat-analytics-go/parser"
)

// TestDynamicTypeTestCapture validates every sample in the 100-message
// cyclic capture against plc/DynamicTypeTest.prg.st's determinism contract.
//
// Unlike ArrayAccessTest/StructTypeTest, there's no single published
// "master_count" seed. Instead:
//
//   - The integer fields (var_sint..var_lword) all increment by exactly 1
//     every scan from known init values, with type-specific wraparound.
//     var_dint (DINT, 32-bit signed) never wraps within any realistic
//     capture, so it's used as the scan-count reference here — confirmed
//     empirically before writing this test (zero gaps across all 3200
//     samples in the real capture). Every other integer field is checked
//     against that scan count with its own type's wraparound applied; only
//     var_dint itself isn't self-checked, since it's the reference.
//
//   - var_real/var_lreal are an amplitude-modulated sinusoid driven by
//     _t_real/_t_lreal — internal accumulators that, unlike other fixtures'
//     underscore-prefixed internals, ARE published here. So the formula in
//     the .prg.st header comment can be evaluated directly per sample and
//     compared with a small epsilon (Go's math.Sin vs whatever TwinCAT's
//     SIN() does internally differ by roughly 1 ULP in practice — confirmed
//     empirically: max observed diff was 6e-8 for float32, 2e-16 for
//     float64, well inside the epsilons used below).
//
//   - var_bool and all string/time/date fields are never mutated by the
//     cyclic body (confirmed empirically, not just from the header comment,
//     which doesn't actually mention var_bool) and are checked every sample.
//
//   - The VAR CONSTANT fields (_TWO_PI, _TWO_PI_LR, _T_FAST, _T_SLOW,
//     _cycle_s) are checked every sample too, since they're published and
//     provably constant.
const dynamicTypeCaptureDir = "../testdata/fixtures/DynamicTypeTest/captures/capture-202602270555"

// Sinusoid formula constants, matching plc/DynamicTypeTest.prg.st's VAR
// CONSTANT block exactly (also independently checked against the published
// _TWO_PI/_TWO_PI_LR/_T_FAST/_T_SLOW fields below, so a drift in either the
// PLC source or these literals would show up as a mismatch somewhere).
const (
	dtTwoPi    = 6.28318530
	dtTwoPiLR  = 6.28318530717958648
	dtTFast    = 5.0
	dtTSlow    = 60.0
	dtCycleS   = 0.01
	dtRealEps  = 1e-6
	dtLrealEps = 1e-9
)

func TestDynamicTypeTestCapture(t *testing.T) {
	f, binData, err := fixture.LoadWithVerify(filepath.Join(dynamicTypeCaptureDir, "symbols", "message-20260227T055544.976043097.yml"))
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
	t.Logf("layout GUID=%s  fields=%d  sampleDataSize=%d", l.GUID, len(l.Fields), l.SampleDataSize)

	dataDir := filepath.Join(dynamicTypeCaptureDir, "data")
	msgPaths, err := filepath.Glob(filepath.Join(dataDir, "*.bin"))
	if err != nil {
		t.Fatalf("glob data dir: %v", err)
	}
	if len(msgPaths) == 0 {
		t.Fatalf("no *.bin files found in %s", dataDir)
	}

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
		if msg.Header.LayoutGUID != l.GUID {
			t.Fatalf("%s: GUID mismatch: data=%s layout=%s",
				filepath.Base(msgPath), msg.Header.LayoutGUID, l.GUID)
		}

		for si, sample := range msg.Samples {
			totalSamples++
			fvs := l.ParseSample(sample.Raw)
			byName := make(map[string]any, len(fvs))
			for _, fv := range fvs {
				byName[fv.Field.Name] = fv.Value
			}

			// ── Scan-count reference ─────────────────────────────────────
			dint, ok := byName["DynamicTypeTest.var_dint"].(int32)
			if !ok {
				t.Fatalf("%s sample[%d]: var_dint missing or wrong type: %#v",
					filepath.Base(msgPath), si, byName["DynamicTypeTest.var_dint"])
			}
			scanCount := int64(dint) + 100000 // var_dint init = -100000

			// ── Integer fields: init + scanCount, type-specific wraparound ──
			assertField(t, byName, "DynamicTypeTest.var_sint", int8(int32(-100)+int32(scanCount)))
			assertField(t, byName, "DynamicTypeTest.var_int", int16(int32(-1000)+int32(scanCount)))
			assertField(t, byName, "DynamicTypeTest.var_lint", int64(-1000000000)+scanCount)
			assertField(t, byName, "DynamicTypeTest.var_usint", uint8(uint32(200)+uint32(scanCount)))
			assertField(t, byName, "DynamicTypeTest.var_uint", uint16(uint32(60000)+uint32(scanCount)))
			assertField(t, byName, "DynamicTypeTest.var_udint", uint32(3000000000)+uint32(scanCount))
			assertField(t, byName, "DynamicTypeTest.var_ulint", uint64(10000000000)+uint64(scanCount))
			assertField(t, byName, "DynamicTypeTest.var_byte", uint8(uint32(0xAB)+uint32(scanCount)))
			assertField(t, byName, "DynamicTypeTest.var_word", uint16(uint32(0xABCD)+uint32(scanCount)))
			assertField(t, byName, "DynamicTypeTest.var_dword", uint32(0xDEADBEEF)+uint32(scanCount))
			assertField(t, byName, "DynamicTypeTest.var_lword", uint64(0xDEADBEEFCAFEBABE)+uint64(scanCount))

			// ── Sinusoid: derived from the published accumulators ────────
			tReal, ok := byName["DynamicTypeTest._t_real"].(float32)
			if !ok {
				t.Fatalf("%s sample[%d]: _t_real missing or wrong type: %#v",
					filepath.Base(msgPath), si, byName["DynamicTypeTest._t_real"])
			}
			// The sine *argument* is computed in float32 (REAL) precision,
			// matching the PLC exactly -- _TWO_PI, _t_real, and _T_SLOW/
			// _T_FAST are all REAL on the TwinCAT side, so
			// "_TWO_PI * _t_real / _T_SLOW" accumulates float32 rounding
			// before SIN() is ever called. Promoting to float64 earlier
			// than the PLC does looked "more precise" but actually produced
			// a *different* (wrong) argument -- caught by a real capture
			// diff up to ~1.8e-6, well outside float32 ULP noise, until
			// this was fixed to round at the same point the PLC does.
			argSlow := float32(dtTwoPi) * tReal / float32(dtTSlow)
			argFast := float32(dtTwoPi) * tReal / float32(dtTFast)
			wantReal := float32(math.Sin(float64(argSlow)) * math.Sin(float64(argFast)))
			gotReal, ok := byName["DynamicTypeTest.var_real"].(float32)
			if !ok {
				t.Fatalf("%s sample[%d]: var_real missing or wrong type: %#v",
					filepath.Base(msgPath), si, byName["DynamicTypeTest.var_real"])
			}
			if diff := math.Abs(float64(gotReal - wantReal)); diff > dtRealEps {
				t.Errorf("%s sample[%d]: var_real = %v, want %v (diff %e > eps %e, t_real=%v)",
					filepath.Base(msgPath), si, gotReal, wantReal, diff, dtRealEps, tReal)
			}

			tLreal, ok := byName["DynamicTypeTest._t_lreal"].(float64)
			if !ok {
				t.Fatalf("%s sample[%d]: _t_lreal missing or wrong type: %#v",
					filepath.Base(msgPath), si, byName["DynamicTypeTest._t_lreal"])
			}
			wantLreal := math.Sin(dtTwoPiLR*tLreal/dtTSlow) * math.Sin(dtTwoPiLR*tLreal/dtTFast)
			gotLreal, ok := byName["DynamicTypeTest.var_lreal"].(float64)
			if !ok {
				t.Fatalf("%s sample[%d]: var_lreal missing or wrong type: %#v",
					filepath.Base(msgPath), si, byName["DynamicTypeTest.var_lreal"])
			}
			if diff := math.Abs(gotLreal - wantLreal); diff > dtLrealEps {
				t.Errorf("%s sample[%d]: var_lreal = %v, want %v (diff %e > eps %e, t_lreal=%v)",
					filepath.Base(msgPath), si, gotLreal, wantLreal, diff, dtLrealEps, tLreal)
			}

			// ── Never mutated by the cyclic body ─────────────────────────
			assertField(t, byName, "DynamicTypeTest.var_bool", true)
			assertField(t, byName, "DynamicTypeTest.var_string", "Hello TwinCAT")
			assertField(t, byName, "DynamicTypeTest.var_string_short", "Hi")
			assertField(t, byName, "DynamicTypeTest.var_string_long",
				"The quick brown fox jumps over the lazy dog. Extra padding to stress-test long string parsing in the binary symbol stream.")
			assertField(t, byName, "DynamicTypeTest.var_wstring", "Hello TwinCAT Wide")
			assertField(t, byName, "DynamicTypeTest.var_wstring_short", "Hi Wide")
			assertField(t, byName, "DynamicTypeTest.var_wstring_long",
				"The quick brown fox jumps over the lazy dog in wide-string format. Testing WSTRING parser alignment across multiple lengths.")
			assertField(t, byName, "DynamicTypeTest.var_time", time.Duration(1500)*time.Millisecond)
			assertField(t, byName, "DynamicTypeTest.var_date", time.Date(2026, 2, 23, 0, 0, 0, 0, time.UTC))
			assertField(t, byName, "DynamicTypeTest.var_date_and_time", time.Date(2026, 2, 23, 10, 30, 0, 0, time.UTC))
			assertField(t, byName, "DynamicTypeTest.var_time_of_day", 10*time.Hour+30*time.Minute)
			assertField(t, byName, "DynamicTypeTest.var_ltime", 26*time.Hour+3*time.Minute+4*time.Second+5*time.Millisecond)

			// ── VAR CONSTANT fields ────────────────────────────────────────
			assertField(t, byName, "DynamicTypeTest._TWO_PI", float32(dtTwoPi))
			assertField(t, byName, "DynamicTypeTest._TWO_PI_LR", float64(dtTwoPiLR))
			assertField(t, byName, "DynamicTypeTest._T_FAST", float32(dtTFast))
			assertField(t, byName, "DynamicTypeTest._T_SLOW", float32(dtTSlow))
			assertField(t, byName, "DynamicTypeTest._cycle_s", float32(dtCycleS))
		}
	}
	t.Logf("verified %d samples across %d messages", totalSamples, len(msgPaths))
}

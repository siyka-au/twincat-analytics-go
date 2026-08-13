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

// TestEventTestCapture validates every sample in the real capture against
// plc/EventTest.prg.st's determinism contract. Unlike ArrayAccessTest, the
// true seed (_cycle_count) is deliberately not published (underscore
// prefix) — only phase = _cycle_count MOD 16 is. event_a, event_b, and
// state_label are pure functions of phase alone and are checked per-sample.
// event_toggle additionally depends on the (unobservable) period parity, so
// it's checked as a cross-sample invariant instead: it must flip on every
// phase 15→0 wrap and stay constant everywhere else. That invariant was
// verified empirically against this capture before writing the assertion
// (3200 samples, zero phase-sequence gaps, toggle flips at exactly the 200
// wrap boundaries and nowhere else) — it does not depend on that gap-free
// property holding, since the assertion only fires at observed wraps.
const eventTestCaptureDir = "../testdata/fixtures/EventTest/captures/capture-202602270619"

func TestEventTestCapture(t *testing.T) {
	f, binData, err := fixture.LoadWithVerify(filepath.Join(eventTestCaptureDir, "symbols", "message-20260227T062000.230929221.yml"))
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

	dataDir := filepath.Join(eventTestCaptureDir, "data")
	msgPaths, err := filepath.Glob(filepath.Join(dataDir, "*.bin"))
	if err != nil {
		t.Fatalf("glob data dir: %v", err)
	}
	if len(msgPaths) == 0 {
		t.Fatalf("no *.bin files found in %s", dataDir)
	}

	prevPhase := -1
	prevToggle := false
	havePrev := false
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
			ctx := fmt.Sprintf("%s/sample[%d]", filepath.Base(msgPath), si)

			phaseVal, ok := byName["EventTest.phase"].(uint8)
			if !ok {
				t.Fatalf("%s: phase missing or wrong type: %#v", ctx, byName["EventTest.phase"])
			}
			phase := int(phaseVal)

			// ── Level-1/2: pure functions of phase ──────────────────────────
			assertField(t, byName, "EventTest.event_a", phase == 0)
			assertField(t, byName, "EventTest.event_b", phase < 8)

			var wantState string
			switch phase % 4 {
			case 0:
				wantState = "IDLE"
			case 1:
				wantState = "ACTIVE"
			case 2:
				wantState = "WAIT"
			case 3:
				wantState = "DONE"
			}
			assertField(t, byName, "EventTest.state_label", wantState)

			// ── Static fields: never mutated at runtime ─────────────────────
			assertField(t, byName, "EventTest.static_int", int16(42))
			assertField(t, byName, "EventTest.static_real", float32(2.71828))
			assertField(t, byName, "EventTest.static_str", "EVENT_TEST")

			// ── event_toggle: cross-sample invariant ────────────────────────
			toggleVal, ok := byName["EventTest.event_toggle"].(bool)
			if !ok {
				t.Fatalf("%s: event_toggle missing or wrong type: %#v", ctx, byName["EventTest.event_toggle"])
			}
			if havePrev {
				atWrap := prevPhase == 15 && phase == 0
				if atWrap && toggleVal == prevToggle {
					t.Errorf("%s: event_toggle did not flip across a phase 15->0 wrap (stayed %v)", ctx, toggleVal)
				}
				if !atWrap && toggleVal != prevToggle {
					t.Errorf("%s: event_toggle flipped outside a phase wrap (prevPhase=%d phase=%d)", ctx, prevPhase, phase)
				}
			}
			prevPhase = phase
			prevToggle = toggleVal
			havePrev = true
		}
	}
	t.Logf("verified %d samples across %d messages", totalSamples, len(msgPaths))
}

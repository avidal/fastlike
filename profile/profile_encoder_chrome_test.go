package profile

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeChromeTraceNil(t *testing.T) {
	if _, err := EncodeChromeTrace(nil); err == nil {
		t.Fatal("expected error for nil trace")
	}
}

func TestEncodeChromeTraceNormal(t *testing.T) {
	raw, err := EncodeChromeTrace(fixtureNormalTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "chrome_normal.json", raw)
}

func TestEncodeChromeTraceDroppedCounters(t *testing.T) {
	raw, err := EncodeChromeTrace(fixtureDroppedCountersTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "chrome_dropped.json", raw)
}

func TestEncodeChromeTraceNilPhases(t *testing.T) {
	raw, err := EncodeChromeTrace(fixtureNilPhasesTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "chrome_nil_phases.json", raw)
}

func TestEncodeChromeTraceNativeSamples(t *testing.T) {
	raw, err := EncodeChromeTrace(fixtureNativeSamplesTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "chrome_native_samples.json", raw)
}

func TestEncodeChromeTracePanic(t *testing.T) {
	raw, err := EncodeChromeTrace(fixturePanicTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "chrome_panic.json", raw)
}

func TestEncodeChromeTraceCtxCanceled(t *testing.T) {
	raw, err := EncodeChromeTrace(fixtureCtxCanceledTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "chrome_ctx_canceled.json", raw)
}

// TestEncodeChromeTraceStructuralPredicates is a behavioural smoke test
// that does not depend on the golden file. It enforces invariants the
// encoder must always satisfy regardless of how we evolve the goldens.
func TestEncodeChromeTraceStructuralPredicates(t *testing.T) {
	raw, err := EncodeChromeTrace(fixtureNativeSamplesTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	events := doc["traceEvents"].([]any)

	// Every event has pid and tid; every X event has a ts and dur.
	for i, e := range events {
		ev := e.(map[string]any)
		if _, ok := ev["pid"]; !ok {
			t.Errorf("event %d missing pid", i)
		}
		if _, ok := ev["tid"]; !ok {
			t.Errorf("event %d missing tid", i)
		}
		if ev["ph"] == "X" {
			if _, ok := ev["dur"]; !ok {
				t.Errorf("event %d (X) missing dur", i)
			}
		}
	}

	// Native samples produce instant events on tid 3.
	nativeCount := 0
	for _, e := range events {
		ev := e.(map[string]any)
		if ev["cat"] == "native" {
			nativeCount++
			if ev["ph"] != "i" {
				t.Errorf("native event phase: got %v, want i", ev["ph"])
			}
			if ev["tid"] != float64(chromeTidNative) {
				t.Errorf("native event tid: got %v, want %d", ev["tid"], chromeTidNative)
			}
		}
	}
	if nativeCount != 3 {
		t.Errorf("native sample count: got %d, want 3", nativeCount)
	}
}

func TestEncodeChromeTracePrivacyNoUnexpectedFields(t *testing.T) {
	// Confirm the encoder doesn't leak fields it shouldn't have access
	// to. The trace data model omits header values / body bytes /
	// secrets entirely, so the encoder physically cannot include them,
	// but this test pins the public surface so a future schema change
	// can't accidentally widen it.
	raw, err := EncodeChromeTrace(fixtureNormalTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	body := string(raw)
	for _, forbidden := range []string{"Authorization", "Cookie", "secret", "password"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("encoded trace contains forbidden token %q", forbidden)
		}
	}
}

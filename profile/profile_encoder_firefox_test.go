package profile

import (
	"encoding/json"
	"testing"
)

func TestEncodeFirefoxGeckoNil(t *testing.T) {
	if _, err := EncodeFirefoxGecko(nil); err == nil {
		t.Fatal("expected error for nil trace")
	}
}

func TestEncodeFirefoxGeckoNormal(t *testing.T) {
	raw, err := EncodeFirefoxGecko(fixtureNormalTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "firefox_normal.json", raw)
}

func TestEncodeFirefoxGeckoDroppedCounters(t *testing.T) {
	raw, err := EncodeFirefoxGecko(fixtureDroppedCountersTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "firefox_dropped.json", raw)
}

func TestEncodeFirefoxGeckoNilPhases(t *testing.T) {
	raw, err := EncodeFirefoxGecko(fixtureNilPhasesTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "firefox_nil_phases.json", raw)
}

func TestEncodeFirefoxGeckoNativeSamples(t *testing.T) {
	raw, err := EncodeFirefoxGecko(fixtureNativeSamplesTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "firefox_native_samples.json", raw)
}

func TestEncodeFirefoxGeckoPanic(t *testing.T) {
	raw, err := EncodeFirefoxGecko(fixturePanicTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "firefox_panic.json", raw)
}

func TestEncodeFirefoxGeckoCtxCanceled(t *testing.T) {
	raw, err := EncodeFirefoxGecko(fixtureCtxCanceledTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "firefox_ctx_canceled.json", raw)
}

func TestEncodeFirefoxGeckoStructuralPredicates(t *testing.T) {
	raw, err := EncodeFirefoxGecko(fixtureNativeSamplesTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	threads := doc["threads"].([]any)
	if len(threads) != 1 {
		t.Fatalf("threads: got %d, want 1", len(threads))
	}
	th := threads[0].(map[string]any)
	// Markers populated.
	markers := th["markers"].(map[string]any)
	if got := markers["length"].(float64); got < 1 {
		t.Errorf("markers length: %v", got)
	}
	// Samples populated for native samples fixture.
	samples := th["samples"].(map[string]any)
	if got := samples["length"].(float64); got != 3 {
		t.Errorf("samples length: got %v, want 3", got)
	}
	// String table contains the resolved native sample function names.
	st := th["stringTable"].([]any)
	found := 0
	for _, s := range st {
		if name, _ := s.(string); name == "guest_main" || name == "alloc" || name == "guest_handle_request" {
			found++
		}
	}
	if found != 3 {
		t.Errorf("expected 3 native function names interned in stringTable, found %d (table=%v)", found, st)
	}
}

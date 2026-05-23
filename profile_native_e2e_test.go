package fastlike

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestNativeSamplesEndToEnd wires the perf script importer →
// MergeNativeSamples → JSON encoder → ProfileUI together, end to end.
// It uses the canned fixture (no actual perf invocation required) and
// pins that the join key, the wire schema, and the viewer rendering all
// reach the right state for a real native run.
func TestNativeSamplesEndToEnd(t *testing.T) {
	fix, err := os.Open("testdata/native_samples/perf_script_basic.txt")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = fix.Close() }()
	events, err := NewPerfScriptImporter().Import(fix)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	// Synthesise a trace whose WallStart sits inside the fixture's
	// timestamps (1700000000.0000015 .. 1700000000.0030000).
	store := NewProfileStore()
	start := time.Unix(1700000000, 0)
	r, _ := http.NewRequest("GET", "http://x/", nil)
	tr := store.newRequestTrace("modX", r)
	tr.WallStart = start
	tr.WallNanos = 5_000_000 // 5ms covers the first three fixture events
	store.completeTrace(tr)

	attached := MergeNativeSamples(store, events, 12345, "modX")
	if attached != 3 {
		t.Fatalf("attached: got %d, want 3 (fixture has 3 pid=12345 samples in window)", attached)
	}

	// Confirm the JSON now carries native_samples in the wire schema.
	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"native_samples"`) {
		t.Errorf("native_samples missing from JSON: %s", raw)
	}

	// And the UI page renders the native samples table.
	ui := NewProfileUI(store)
	req := httptest.NewRequest(http.MethodGet, "/r/"+itoaUint(tr.ReqID), nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ui status: %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "native samples (3)") {
		t.Errorf("expected 'native samples (3)' heading; got %s", body)
	}
	if !strings.Contains(body, "wasm_function_a") {
		t.Errorf("expected wasm_function_a in HTML")
	}
}

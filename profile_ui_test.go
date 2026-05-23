package fastlike

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newStoreWithTrace(t *testing.T) (*ProfileStore, *RequestTrace) {
	t.Helper()
	s := NewProfileStore()
	r, _ := http.NewRequest("GET", "http://example.com/foo", nil)
	tr := s.newRequestTrace("mod0001", r)
	tr.Status = 200
	tr.WallNanos = 5_000_000
	tr.HostcallNanos = 1_500_000
	tr.GuestActiveNanos = 3_500_000
	tr.Spans = []Span{{NameIdx: hostcallNameIndex("body_downstream_get"), Duration: 1_000_000}}
	connect := int64(250_000)
	tr.BackendCalls = []BackendCall{{
		PendingID:    0,
		Name:         "api",
		Method:       "POST",
		URLRedacted:  "http://upstream/path",
		Started:      100_000,
		TotalNanos:   2_000_000,
		ConnectNanos: &connect,
		Status:       200,
		Outcome:      BackendOutcomeOk,
	}}
	tr.Dropped = 4
	tr.DroppedBackendCalls = 2
	s.completeTrace(tr)
	return s, tr
}

func TestProfileUIServeIndex(t *testing.T) {
	s, tr := newStoreWithTrace(t)
	ui := NewProfileUI(s)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "fastlike profiler") {
		t.Errorf("expected title in body")
	}
	if !strings.Contains(body, "/r/1") {
		t.Errorf("expected per-request link, got %s", body)
	}
	if !strings.Contains(body, "+4 dropped") {
		t.Errorf("expected dropped spans annotation in index, got %s", body)
	}
	if !strings.Contains(body, "+2 dropped") {
		t.Errorf("expected dropped backend calls annotation in index")
	}
	_ = tr
}

func TestProfileUIServeRequestHTML(t *testing.T) {
	s, tr := newStoreWithTrace(t)
	ui := NewProfileUI(s)

	req := httptest.NewRequest(http.MethodGet, "/r/"+itoaUint(tr.ReqID), nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "request 1") {
		t.Errorf("expected request title")
	}
	if !strings.Contains(body, "body_downstream_get") {
		t.Errorf("expected span name in HTML, got %s", body)
	}
	if !strings.Contains(body, "http://upstream/path") {
		t.Errorf("expected redacted URL in HTML")
	}
	if !strings.Contains(body, "backend waterfall") {
		t.Errorf("expected waterfall heading")
	}
	if !strings.Contains(body, "spans=4") {
		t.Errorf("expected dropped span count surfaced, got %s", body)
	}
}

func TestProfileUIServeRequestJSON(t *testing.T) {
	s, tr := newStoreWithTrace(t)
	ui := NewProfileUI(s)

	req := httptest.NewRequest(http.MethodGet, "/r/"+itoaUint(tr.ReqID)+".json", nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: %q", ct)
	}
	var decoded map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["req_id"] != float64(1) {
		t.Errorf("req_id wrong: %v", decoded["req_id"])
	}
	if decoded["dropped"] != float64(4) {
		t.Errorf("dropped not surfaced: %v", decoded["dropped"])
	}
	spans := decoded["spans"].([]any)
	if len(spans) != 1 {
		t.Fatalf("spans length")
	}
	if spans[0].(map[string]any)["name"] != "body_downstream_get" {
		t.Errorf("span name not resolved in JSON")
	}
	calls := decoded["backend_calls"].([]any)
	call0 := calls[0].(map[string]any)
	if call0["url_redacted"] != "http://upstream/path" {
		t.Errorf("redacted URL missing from JSON")
	}
}

func TestProfileUIMissingTrace(t *testing.T) {
	s, _ := newStoreWithTrace(t)
	ui := NewProfileUI(s)

	cases := []string{"/r/9999", "/r/9999.json", "/r/abc", "/r/", "/anything-else"}
	for _, path := range cases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("path %q: status %d, want 404", path, w.Code)
		}
	}
}

func TestProfileUIMethodNotAllowed(t *testing.T) {
	s, _ := newStoreWithTrace(t)
	ui := NewProfileUI(s)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status: %d, want 405", w.Code)
	}
	if got := w.Header().Get("Allow"); got != "GET" {
		t.Errorf("Allow header: %q, want GET", got)
	}
}

func TestProfileUINilStoreServiceUnavailable(t *testing.T) {
	ui := NewProfileUI(nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("nil store: %d, want 503", w.Code)
	}
}

func TestProfileUITimelineAssetServed(t *testing.T) {
	s := NewProfileStore()
	ui := NewProfileUI(s)

	req := httptest.NewRequest(http.MethodGet, profileUITimelineAssetPath, nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("asset status: %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("content-type: %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "fl-timeline") {
		t.Errorf("asset body missing expected marker; got first 80 bytes: %q", body[:min(80, len(body))])
	}
	if len(body) < 500 {
		t.Errorf("asset suspiciously short: %d bytes", len(body))
	}
}

func TestProfileUIServeChromeJSON(t *testing.T) {
	s, tr := newStoreWithTrace(t)
	ui := NewProfileUI(s)

	req := httptest.NewRequest(http.MethodGet, "/r/"+itoaUint(tr.ReqID)+".chrome.json", nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "fastlike-req-1.chrome.json") {
		t.Errorf("disposition: %q", cd)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := doc["traceEvents"]; !ok {
		t.Errorf("Chrome JSON missing traceEvents")
	}
}

func TestProfileUIServeFirefoxJSON(t *testing.T) {
	s, tr := newStoreWithTrace(t)
	ui := NewProfileUI(s)

	req := httptest.NewRequest(http.MethodGet, "/r/"+itoaUint(tr.ReqID)+".firefox.json", nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: %q", ct)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := doc["threads"]; !ok {
		t.Errorf("firefox JSON missing threads")
	}
}

func TestProfileUIServePprof(t *testing.T) {
	s, tr := newStoreWithTrace(t)
	ui := NewProfileUI(s)

	req := httptest.NewRequest(http.MethodGet, "/r/"+itoaUint(tr.ReqID)+".pprof", nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("content-type: %q", ct)
	}
	if enc := w.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Errorf("content-encoding: %q", enc)
	}
	if w.Body.Len() < 50 {
		t.Errorf("pprof body suspiciously small: %d bytes", w.Body.Len())
	}
}

func TestProfileUIDownloadsListed(t *testing.T) {
	s, tr := newStoreWithTrace(t)
	ui := NewProfileUI(s)

	req := httptest.NewRequest(http.MethodGet, "/r/"+itoaUint(tr.ReqID), nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)
	body := w.Body.String()
	for _, want := range []string{
		`href="/r/1.json"`,
		`href="/r/1.chrome.json"`,
		`href="/r/1.firefox.json"`,
		`href="/r/1.pprof"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("download link %q missing from HTML", want)
		}
	}
}

func TestProfileUIRequestSuffixRegistryConsistency(t *testing.T) {
	// The longest-first suffix list and the handler map must agree
	// exactly. A drift means a download URL either routes to the wrong
	// handler (.chrome.json falling through to .json) or 404s when it
	// should work.
	for _, suffix := range requestDownloadSuffixes {
		if _, ok := requestDownloadHandlers[suffix]; !ok {
			t.Errorf("suffix %q has no handler", suffix)
		}
	}
	if got, want := len(requestDownloadSuffixes), len(requestDownloadHandlers); got != want {
		t.Errorf("suffix list length %d != handler map length %d", got, want)
	}
	// Longest-first: a later (shorter) suffix CAN be a suffix of an
	// earlier (longer) one — that's the whole point. The violation
	// is the reverse: an earlier suffix must not be a suffix of a
	// later one, because the earlier entry would match the later
	// entry's URLs and steal them.
	for i, a := range requestDownloadSuffixes {
		for j := i + 1; j < len(requestDownloadSuffixes); j++ {
			b := requestDownloadSuffixes[j]
			if a != "" && b != "" && strings.HasSuffix(b, a) {
				t.Errorf("suffix order violated: %q at %d would steal %q at %d's URLs",
					a, i, b, j)
			}
		}
	}
}

func TestProfileUISplitRequestSuffix(t *testing.T) {
	cases := []struct {
		in     string
		id     string
		suffix string
	}{
		{"7", "7", ""},
		{"7.json", "7", ".json"},
		{"7.chrome.json", "7", ".chrome.json"},
		{"7.firefox.json", "7", ".firefox.json"},
		{"7.pprof", "7", ".pprof"},
	}
	for _, c := range cases {
		id, suffix := splitRequestSuffix(c.in)
		if id != c.id || suffix != c.suffix {
			t.Errorf("splitRequestSuffix(%q) = (%q, %q), want (%q, %q)", c.in, id, suffix, c.id, c.suffix)
		}
	}
}

func TestProfileUIRequestPageOmitsDeepSectionWhenAbsent(t *testing.T) {
	s, tr := newStoreWithTrace(t)
	ui := NewProfileUI(s)

	req := httptest.NewRequest(http.MethodGet, "/r/"+itoaUint(tr.ReqID), nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)

	if strings.Contains(w.Body.String(), "deep metrics") {
		t.Errorf("deep section should be absent when trace.Deep is nil")
	}
}

func TestProfileUIRequestPageRendersDeepWhenPresent(t *testing.T) {
	s, tr := newStoreWithTrace(t)
	tr.Deep = newDeepMetrics()
	tr.Deep.BodyReadBytes = 4321
	tr.Deep.BodyWriteBytes = 8765
	tr.Deep.CacheLookups = 2
	tr.Deep.CacheInserts = 1
	tr.Deep.bumpStoreAccess("kv", "users")
	tr.Deep.bumpStoreAccess("secret", "github_token")
	tr.Deep.finalize()

	ui := NewProfileUI(s)
	req := httptest.NewRequest(http.MethodGet, "/r/"+itoaUint(tr.ReqID), nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "deep metrics") {
		t.Errorf("deep metrics heading missing")
	}
	if !strings.Contains(body, "4321") {
		t.Errorf("body_read_bytes value missing")
	}
	if !strings.Contains(body, "users") {
		t.Errorf("kv:users store access missing from HTML")
	}
	if !strings.Contains(body, "github_token") {
		t.Errorf("secret:github_token store access missing from HTML")
	}
}

func TestProfileUIRequestPageOmitsNativeSamplesSectionWhenEmpty(t *testing.T) {
	s, tr := newStoreWithTrace(t)
	ui := NewProfileUI(s)

	req := httptest.NewRequest(http.MethodGet, "/r/"+itoaUint(tr.ReqID), nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, "native samples") {
		t.Errorf("native samples section should be absent when trace carries none; body: %s", body)
	}
}

func TestProfileUIRequestPageRendersNativeSamplesSectionWhenPresent(t *testing.T) {
	s, tr := newStoreWithTrace(t)
	tr.NativeSamples = []NativeSample{
		{RelativeNanos: 1_000_000, Function: "guest_main"},
		{RelativeNanos: 2_500_000, Function: "alloc"},
	}
	ui := NewProfileUI(s)

	req := httptest.NewRequest(http.MethodGet, "/r/"+itoaUint(tr.ReqID), nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "native samples (2)") {
		t.Errorf("expected 'native samples (2)' heading; got %s", body)
	}
	if !strings.Contains(body, "guest_main") {
		t.Errorf("expected guest_main row in HTML")
	}
	if !strings.Contains(body, "alloc") {
		t.Errorf("expected alloc row in HTML")
	}
}

func TestProfileUIRequestPageEmbedsCanvasAndScript(t *testing.T) {
	s, tr := newStoreWithTrace(t)
	ui := NewProfileUI(s)

	req := httptest.NewRequest(http.MethodGet, "/r/"+itoaUint(tr.ReqID), nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `id="fl-timeline"`) {
		t.Error("expected canvas root element in HTML")
	}
	wantData := `data-json-url="/r/` + itoaUint(tr.ReqID) + `.json"`
	if !strings.Contains(body, wantData) {
		t.Errorf("expected %q in HTML, got %s", wantData, body)
	}
	if !strings.Contains(body, `src="`+profileUITimelineAssetPath+`"`) {
		t.Error("expected script tag pointing at the embedded asset")
	}
	if !strings.Contains(body, `<noscript>`) {
		t.Error("expected noscript fallback explanation")
	}
	// Server-rendered tables must still be present so the page works
	// without JS — the canvas viewer is purely an enhancement.
	if !strings.Contains(body, "backend waterfall") {
		t.Error("expected CSS waterfall heading (no-JS fallback)")
	}
	if !strings.Contains(body, "hostcall spans") {
		t.Error("expected span table heading (no-JS fallback)")
	}
}

func TestProfileUIEmptyIndex(t *testing.T) {
	s := NewProfileStore()
	ui := NewProfileUI(s)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "No completed traces yet") {
		t.Errorf("expected empty-state copy")
	}
}

// itoaUint formats a uint64 without importing strconv into the test file
// for a single use.
func itoaUint(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

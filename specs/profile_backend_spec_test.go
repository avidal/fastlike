package spec_test

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"testing"
	"time"

	"fastlike.dev/profile"

	"fastlike.dev"
)

// TestProfileBackendSync covers the parts of step 3 reachable through the
// existing rust wasm fixture, which only issues synchronous backend calls
// via `req.send(BACKEND)`. Async coverage stays in profile_backend_test.go
// where the recorder API is exercised directly; bringing async in here
// would require extending the rust fixture.
func TestProfileBackendSync(t *testing.T) {
	t.Parallel()

	if _, perr := os.Stat(*wasmfile); os.IsNotExist(perr) {
		t.Skipf("wasm test file %q does not exist; skipping backend tests", *wasmfile)
	}

	t.Run("ok-no-transport", func(st *testing.T) {
		st.Parallel()
		f := fastlike.New(*wasmfile, fastlike.WithProfileMode(profile.ProfileModeTrace))
		ps := f.ProfileStore()

		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://x/proxy", io.NopCloser(bytes.NewBuffer(nil)))
		inst := f.Instantiate(fastlike.WithBackend("backend", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("hi"))
		})))
		inst.ServeHTTP(w, r)

		traces := ps.Recent(0)
		if len(traces) != 1 {
			st.Fatalf("expected 1 trace, got %d", len(traces))
		}
		if got := len(traces[0].BackendCalls); got != 1 {
			st.Fatalf("expected 1 profile.BackendCall, got %d", got)
		}
		call := traces[0].BackendCalls[0]
		if call.Outcome != profile.BackendOutcomeOk {
			st.Errorf("outcome: %d, want ok", call.Outcome)
		}
		if call.Status != http.StatusOK {
			st.Errorf("status: %d, want 200", call.Status)
		}
		if call.Name != "backend" {
			st.Errorf("name: %q", call.Name)
		}
		// No transport → no phase data.
		if call.DNSNanos != nil || call.ConnectNanos != nil || call.TLSNanos != nil || call.TTFBNanos != nil {
			st.Errorf("phase fields should be nil for non-traced backend, got %+v", call)
		}
		if call.TotalNanos <= 0 {
			st.Errorf("TotalNanos not stamped: %d", call.TotalNanos)
		}
	})

	t.Run("ok-traced-transport-phases-populated", func(st *testing.T) {
		st.Parallel()
		// Real HTTP server so the transport actually performs a TCP
		// connect we can observe via httptrace.
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}))
		defer upstream.Close()

		u, _ := url.Parse(upstream.URL)
		transport := &http.Transport{
			DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		}
		proxy := httputil.NewSingleHostReverseProxy(u)
		proxy.Transport = transport

		f := fastlike.New(*wasmfile, fastlike.WithProfileMode(profile.ProfileModeTrace))
		ps := f.ProfileStore()

		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://x/proxy", io.NopCloser(bytes.NewBuffer(nil)))
		inst := f.Instantiate(fastlike.WithBackendTraced("backend", proxy, transport))
		inst.ServeHTTP(w, r)

		traces := ps.Recent(0)
		if len(traces) != 1 || len(traces[0].BackendCalls) != 1 {
			st.Fatalf("expected exactly 1 profile.BackendCall in 1 trace, got %d traces", len(traces))
		}
		call := traces[0].BackendCalls[0]
		if call.Outcome != profile.BackendOutcomeOk {
			st.Errorf("outcome: %d, want ok", call.Outcome)
		}
		// Connect and TTFB should always populate for a real TCP server.
		// DNS may or may not fire depending on whether the address is
		// already an IP literal (httptest uses 127.0.0.1, so no DNS).
		if call.ConnectNanos == nil {
			st.Error("ConnectNanos should be populated for traced TCP backend")
		}
		if call.TTFBNanos == nil {
			st.Error("TTFBNanos should be populated for traced TCP backend")
		}
	})

	t.Run("synthetic-failure", func(st *testing.T) {
		st.Parallel()
		f := fastlike.New(*wasmfile, fastlike.WithProfileMode(profile.ProfileModeTrace))
		ps := f.ProfileStore()

		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://x/proxy", io.NopCloser(bytes.NewBuffer(nil)))
		// 0% uptime → reliability wrapper short-circuits with synthetic 502.
		inst := f.Instantiate(fastlike.WithUnreliableBackend("backend", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			st.Fatal("real handler must not run when uptime=0")
		}), 0))
		inst.ServeHTTP(w, r)

		traces := ps.Recent(0)
		if len(traces) != 1 || len(traces[0].BackendCalls) != 1 {
			st.Fatalf("expected 1 profile.BackendCall in 1 trace, got %d traces", len(traces))
		}
		call := traces[0].BackendCalls[0]
		if call.Outcome != profile.BackendOutcomeSyntheticFailure {
			st.Errorf("outcome: %d, want synthetic-failure", call.Outcome)
		}
		if call.Status != http.StatusBadGateway {
			st.Errorf("status: %d, want 502", call.Status)
		}
		// Synthetic failure short-circuits before any transport call.
		if call.DNSNanos != nil || call.ConnectNanos != nil || call.TLSNanos != nil || call.TTFBNanos != nil {
			st.Errorf("phase fields should be nil for synthetic failure, got %+v", call)
		}
	})
}

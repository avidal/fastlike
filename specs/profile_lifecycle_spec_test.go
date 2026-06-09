package spec_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"fastlike.dev/profile"

	"fastlike.dev"
)

// TestProfileLifecycle covers the finalize-on-every-exit-path requirement
// from plans/guest-profiling.md step 1. Each subtest serves a request
// through a profile-enabled Fastlike and then asserts the trace that
// landed in the store.
func TestProfileLifecycle(t *testing.T) {
	t.Parallel()

	if _, perr := os.Stat(*wasmfile); os.IsNotExist(perr) {
		t.Skipf("wasm test file %q does not exist; skipping lifecycle tests", *wasmfile)
	}

	t.Run("normal-return", func(st *testing.T) {
		st.Parallel()
		f := fastlike.New(*wasmfile, fastlike.WithProfileMode(profile.ProfileModeTrace))
		ps := f.ProfileStore()

		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://x/simple-response", io.NopCloser(bytes.NewBuffer(nil)))
		inst := f.Instantiate(fastlike.WithDefaultBackend(failingBackendHandler(st)))
		inst.ServeHTTP(w, r)

		traces := ps.Recent(0)
		if len(traces) != 1 {
			st.Fatalf("expected 1 completed trace, got %d", len(traces))
		}
		tr := traces[0]
		if tr.Outcome != profile.TraceOutcomeNormal {
			st.Errorf("outcome: got %s, want normal", tr.Outcome)
		}
		if tr.Status != http.StatusOK {
			st.Errorf("status: got %d, want 200", tr.Status)
		}
		if tr.WallNanos <= 0 {
			st.Errorf("WallNanos not stamped: %d", tr.WallNanos)
		}
		if tr.ModuleID != f.ModuleID() {
			st.Errorf("module id mismatch: trace=%q fastlike=%q", tr.ModuleID, f.ModuleID())
		}
		if tr.HeaderFlushNanos == nil {
			st.Error("HeaderFlushNanos should be set for a normal response")
		}
	})

	t.Run("loop-fail", func(st *testing.T) {
		st.Parallel()
		f := fastlike.New(*wasmfile, fastlike.WithProfileMode(profile.ProfileModeTrace))
		ps := f.ProfileStore()

		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://x/simple-response", io.NopCloser(bytes.NewBuffer(nil)))
		r.Header.Set("cdn-loop", "fastlike")
		inst := f.Instantiate(fastlike.WithDefaultBackend(failingBackendHandler(st)))
		inst.ServeHTTP(w, r)

		if w.Code != http.StatusLoopDetected {
			st.Errorf("loop-fail status: got %d, want %d", w.Code, http.StatusLoopDetected)
		}
		traces := ps.Recent(0)
		if len(traces) != 1 {
			st.Fatalf("expected 1 completed trace, got %d", len(traces))
		}
		tr := traces[0]
		if tr.Outcome != profile.TraceOutcomeLoopFail {
			st.Errorf("outcome: got %s, want loop-fail", tr.Outcome)
		}
		if tr.Status != http.StatusLoopDetected {
			st.Errorf("status: got %d, want %d", tr.Status, http.StatusLoopDetected)
		}
	})

	t.Run("trap", func(st *testing.T) {
		st.Parallel()
		f := fastlike.New(*wasmfile, fastlike.WithProfileMode(profile.ProfileModeTrace))
		ps := f.ProfileStore()

		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://x/panic!", io.NopCloser(bytes.NewBuffer(nil)))
		inst := f.Instantiate(fastlike.WithDefaultBackend(failingBackendHandler(st)))
		inst.ServeHTTP(w, r)

		traces := ps.Recent(0)
		if len(traces) != 1 {
			st.Fatalf("expected 1 completed trace, got %d", len(traces))
		}
		tr := traces[0]
		if tr.Outcome != profile.TraceOutcomeTrap {
			st.Errorf("outcome: got %s, want trap", tr.Outcome)
		}
		if tr.Status != http.StatusInternalServerError {
			st.Errorf("status: got %d, want 500", tr.Status)
		}
	})

	t.Run("ctx-canceled", func(st *testing.T) {
		st.Parallel()
		f := fastlike.New(*wasmfile, fastlike.WithProfileMode(profile.ProfileModeTrace))
		ps := f.ProfileStore()

		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://x/proxy", io.NopCloser(bytes.NewBuffer(nil)))
		ctx, cancel := context.WithTimeout(r.Context(), 50*time.Millisecond)
		defer cancel()
		r = r.WithContext(ctx)
		inst := f.Instantiate(fastlike.WithDefaultBackend(testBackendHandler(st, func(w http.ResponseWriter, _ *http.Request) {
			<-time.After(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		})))
		inst.ServeHTTP(w, r)

		traces := ps.Recent(0)
		if len(traces) != 1 {
			st.Fatalf("expected 1 completed trace, got %d", len(traces))
		}
		tr := traces[0]
		if tr.Outcome != profile.TraceOutcomeCtxCanceled {
			st.Errorf("outcome: got %s, want ctx-canceled", tr.Outcome)
		}
	})

	t.Run("pooled-isolation", func(st *testing.T) {
		st.Parallel()
		f := fastlike.New(*wasmfile, fastlike.WithProfileMode(profile.ProfileModeTrace))
		ps := f.ProfileStore()

		// Serve the same Fastlike multiple times through the pooling path.
		for i := 0; i < 3; i++ {
			w := httptest.NewRecorder()
			r, _ := http.NewRequest("GET", "http://x/simple-response", io.NopCloser(bytes.NewBuffer(nil)))
			f.ServeHTTP(w, r)
		}

		traces := ps.Recent(0)
		if len(traces) != 3 {
			st.Fatalf("expected 3 completed traces, got %d", len(traces))
		}
		seen := map[uint64]struct{}{}
		for _, tr := range traces {
			if _, dup := seen[tr.ReqID]; dup {
				st.Errorf("duplicate ReqID %d across pooled requests", tr.ReqID)
			}
			seen[tr.ReqID] = struct{}{}
			if tr.Outcome != profile.TraceOutcomeNormal {
				st.Errorf("outcome: got %s, want normal", tr.Outcome)
			}
		}
	})

	t.Run("reload-retains-old-module-id", func(st *testing.T) {
		st.Parallel()
		f := fastlike.New(*wasmfile, fastlike.WithProfileMode(profile.ProfileModeTrace))
		ps := f.ProfileStore()
		originalID := f.ModuleID()

		// First request runs against the original module.
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://x/simple-response", io.NopCloser(bytes.NewBuffer(nil)))
		f.ServeHTTP(w, r)

		// Reload re-reads the wasm file from disk; the bytes are unchanged
		// in this test so ModuleID stays equal, but the code path that
		// recomputes the id and replaces the pool/instancefn is exercised.
		if err := f.Reload(); err != nil {
			st.Fatalf("reload failed: %v", err)
		}

		w2 := httptest.NewRecorder()
		r2, _ := http.NewRequest("GET", "http://x/simple-response", io.NopCloser(bytes.NewBuffer(nil)))
		f.ServeHTTP(w2, r2)

		traces := ps.Recent(0)
		if len(traces) != 2 {
			st.Fatalf("expected 2 retained traces across reload, got %d", len(traces))
		}
		for _, tr := range traces {
			if tr.ModuleID != originalID {
				st.Errorf("retained trace has unexpected module id %q (want %q)", tr.ModuleID, originalID)
			}
		}
	})
}

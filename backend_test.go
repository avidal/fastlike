package fastlike

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWrapWithReliability_Disabled(t *testing.T) {
	calls := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		name   string
		uptime *uint8
	}{
		{"nil uptime is unsimulated", nil},
		{"100% uptime is unsimulated", ptrU8(100)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls = 0
			h := wrapWithReliability(inner, tc.uptime)
			for i := 0; i < 200; i++ {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("expected 200, got %d", rec.Code)
				}
			}
			if calls != 200 {
				t.Fatalf("expected 200 inner calls, got %d", calls)
			}
		})
	}
}

func TestWrapWithReliability_AlwaysDown(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	})

	h := wrapWithReliability(inner, ptrU8(0))

	for i := 0; i < 50; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("expected 502 at iteration %d, got %d", i, rec.Code)
		}
		body, _ := io.ReadAll(rec.Body)
		if !strings.Contains(string(body), "simulated backend failure") {
			t.Fatalf("expected simulated-failure body, got %q", string(body))
		}
		if !strings.Contains(string(body), "uptime=0%") {
			t.Fatalf("expected uptime=0%% in body, got %q", string(body))
		}
	}
	if called {
		t.Fatal("inner handler was called despite 0% uptime")
	}
}

func TestWrapWithReliability_PartialDistribution(t *testing.T) {
	const trials = 20000
	const uptime = 30

	successes := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		successes++
		w.WriteHeader(http.StatusOK)
	})

	h := wrapWithReliability(inner, ptrU8(uptime))

	failures := 0
	for i := 0; i < trials; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
		h.ServeHTTP(rec, req)
		switch rec.Code {
		case http.StatusOK:
			// counted in successes
		case http.StatusBadGateway:
			failures++
		default:
			t.Fatalf("unexpected status %d", rec.Code)
		}
	}

	if successes+failures != trials {
		t.Fatalf("trial accounting mismatch: %d + %d != %d", successes, failures, trials)
	}

	expected := float64(uptime) / 100.0
	observed := float64(successes) / float64(trials)
	if observed < expected-0.05 || observed > expected+0.05 {
		t.Fatalf("observed success rate %.3f deviates from expected %.3f beyond tolerance", observed, expected)
	}
}

func TestAddBackend_AppliesReliabilityWrap(t *testing.T) {
	called := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})

	i := &Instance{backends: map[string]*Backend{}}
	i.addBackend("flaky", &Backend{Handler: inner, UptimePercent: ptrU8(0)})

	h := i.getBackendHandler("flaky")
	for n := 0; n < 10; n++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("expected 502, got %d", rec.Code)
		}
	}
	if called != 0 {
		t.Fatalf("inner handler ran %d times despite 0%% uptime", called)
	}
}

func ptrU8(v uint8) *uint8 { return &v }

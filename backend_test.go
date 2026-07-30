package fastlike

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
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

	h, _ := i.resolveBackendHandler("flaky")
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

func TestBackendIsHealthy_DerivedFromUptime(t *testing.T) {
	cases := []struct {
		name   string
		uptime *uint8
		want   uint32
	}{
		{"no uptime configured is unknown", nil, BackendHealthUnknown},
		{"0% uptime is unhealthy", ptrU8(0), BackendHealthUnhealthy},
		{"1% uptime is healthy", ptrU8(1), BackendHealthHealthy},
		{"50% uptime is healthy", ptrU8(50), BackendHealthHealthy},
		{"100% uptime is healthy", ptrU8(100), BackendHealthHealthy},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inst := &Instance{
				backends: map[string]*Backend{
					"api": {Name: "api", UptimePercent: tc.uptime},
				},
				memory: &Memory{ByteMemory(make([]byte, 4096))},
				abilog: log.New(io.Discard, "", 0),
			}

			const namePtr int32 = 0
			const healthOut int32 = 256
			if _, err := inst.memory.WriteAt([]byte("api"), int64(namePtr)); err != nil {
				t.Fatalf("write name: %v", err)
			}

			status := inst.xqd_backend_is_healthy(namePtr, int32(len("api")), healthOut)
			if status != XqdStatusOK {
				t.Fatalf("status = %d, want %d", status, XqdStatusOK)
			}

			if got := inst.memory.Uint32(int64(healthOut)); got != tc.want {
				t.Errorf("health = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestBackendIsHealthy_UnknownBackend(t *testing.T) {
	inst := &Instance{
		backends: map[string]*Backend{},
		memory:   &Memory{ByteMemory(make([]byte, 4096))},
		abilog:   log.New(io.Discard, "", 0),
	}

	const namePtr int32 = 0
	if _, err := inst.memory.WriteAt([]byte("missing"), int64(namePtr)); err != nil {
		t.Fatalf("write name: %v", err)
	}

	status := inst.xqd_backend_is_healthy(namePtr, int32(len("missing")), 256)
	if status != XqdErrInvalidArgument {
		t.Errorf("status = %d, want %d", status, XqdErrInvalidArgument)
	}
}

func ptrU8(v uint8) *uint8 { return &v }

func TestOverrideHostHandler(t *testing.T) {
	var got string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Host
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequest(http.MethodGet, "http://guest.example.com/", nil)
	OverrideHostHandler(inner, "origin.example.com").ServeHTTP(httptest.NewRecorder(), r)

	if got != "origin.example.com" {
		t.Errorf("forwarded Host = %q, want %q", got, "origin.example.com")
	}
	if r.Host != "guest.example.com" {
		t.Errorf("source Host = %q, want %q", r.Host, "guest.example.com")
	}
}

func TestOverrideHostHandlerEmpty(t *testing.T) {
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	if got := OverrideHostHandler(inner, ""); reflect.ValueOf(got).Pointer() != reflect.ValueOf(inner).Pointer() {
		t.Error("an empty override wrapped the handler, want it returned untouched")
	}
}

// A backend registered with a handler of its own still has to honor
// OverrideHost, which only the fastlike-managed transport handler used to do.
func TestAddBackendAppliesOverrideHost(t *testing.T) {
	var got string
	i := &Instance{backends: map[string]*Backend{}}
	i.addBackend("origin", &Backend{
		Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = r.Host
		}),
		OverrideHost: "origin.example.com",
	})

	b := i.getBackend("origin")
	b.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://guest.example.com/", nil))

	if got != "origin.example.com" {
		t.Errorf("forwarded Host = %q, want %q", got, "origin.example.com")
	}
	if b.URL == nil || b.URL.Host != "origin" {
		t.Errorf("URL = %v, want http://origin synthesized from the name", b.URL)
	}
}

// A fastlike-managed backend builds its handler before registration, so the
// override has to survive the wrapping addBackend does around it.
func TestManagedBackendSendsOverrideHost(t *testing.T) {
	var got string
	origin := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.Host
	}))
	defer origin.Close()

	u, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	b := &Backend{URL: u, OverrideHost: "vhost.example.org", IsDynamic: true}
	b.Handler = b.newTransportHandler()

	i := &Instance{backends: map[string]*Backend{}}
	i.addBackend("origin", b)
	i.getBackend("origin").Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://guest.example.com/", nil))

	if got != "vhost.example.org" {
		t.Errorf("upstream saw Host = %q, want %q", got, "vhost.example.org")
	}
}

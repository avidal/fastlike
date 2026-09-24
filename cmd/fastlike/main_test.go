package main

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
	"time"

	"fastlike.dev"
)

func TestOverrideHostFlags(t *testing.T) {
	flags := make(overrideHostFlags)

	if err := flags.Set("origin=origin.example.com"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	if got := flags["origin"]; got != "origin.example.com" {
		t.Errorf("host override = %q, want %q", got, "origin.example.com")
	}
}

// Ports and bracketed IPv6 literals are legitimate authorities.
func TestOverrideHostFlagsAcceptsAuthorities(t *testing.T) {
	hosts := []string{
		"origin.example.com",
		"origin.example.com:8080",
		"Origin.Example.COM",
		"127.0.0.1:8080",
		"[::1]",
		"[::1]:8080",
		"localhost",
	}

	for _, host := range hosts {
		flags := make(overrideHostFlags)
		if err := flags.Set("origin=" + host); err != nil {
			t.Errorf("Set(origin=%s) error = %v", host, err)
			continue
		}
		if got := flags["origin"]; got != host {
			t.Errorf("host override = %q, want %q", got, host)
		}
	}
}

// Both forms -backend accepts for the catch-all work here too.
func TestOverrideHostFlagsCatchAll(t *testing.T) {
	for _, value := range []string{"origin.example.com", "=origin.example.com"} {
		flags := make(overrideHostFlags)

		if err := flags.Set(value); err != nil {
			t.Fatalf("Set(%q) error = %v", value, err)
		}
		if got := flags[""]; got != "origin.example.com" {
			t.Errorf("Set(%q): catch-all override = %q, want %q", value, got, "origin.example.com")
		}
	}
}

func TestOverrideHostFlagsRejectInvalidValue(t *testing.T) {
	values := []string{
		"",
		"origin=",
		"origin=host with spaces",
		"origin=http://origin.example.com",
		"origin=origin.example.com/path",
		"origin=user@origin.example.com",
		"origin=origin.example.com\r\nX-Injected: 1",
		"origin=origin.example.com\x7f",
		"origin=origin.example.com:http",
		"origin=:8080",
		"origin=[::1",
		"origin=ex%41mple.com",
	}

	for _, value := range values {
		flags := make(overrideHostFlags)
		if err := flags.Set(value); err == nil {
			t.Errorf("Set(%q) succeeded, want an error", value)
		}
	}
}

// The CLI's reverse proxy has to actually put the override on the wire, which
// the library's handler wrapper alone does not prove.
func TestOverrideHostReachesTheUpstream(t *testing.T) {
	var receivedHost string
	origin := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
	}))
	defer origin.Close()

	target, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	proxy := fastlike.OverrideHostHandler(httputil.NewSingleHostReverseProxy(target), "origin.example.com")
	proxy.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://localhost/", nil))

	if receivedHost != "origin.example.com" {
		t.Errorf("forwarded Host = %q, want %q", receivedHost, "origin.example.com")
	}
}

func mustSet(t *testing.T, f flag.Value, v string) {
	t.Helper()
	if err := f.Set(v); err != nil {
		t.Fatalf("Set(%q) error = %v", v, err)
	}
}

func TestBuildBackendOptions(t *testing.T) {
	backends := make(backendFlags)
	mustSet(t, &backends, "origin=localhost:9000")
	mustSet(t, &backends, "localhost:9001")
	overrides := make(overrideHostFlags)
	mustSet(t, &overrides, "origin=origin.example.com")
	timeouts := make(backendTimeoutFlags)
	mustSet(t, &timeouts, "origin=1000,15000,10000")

	opts, err := buildBackendOptions(backends, overrides, timeouts)
	if err != nil {
		t.Fatalf("buildBackendOptions() error = %v", err)
	}
	if len(opts) != len(backends) {
		t.Errorf("options = %d, want %d", len(opts), len(backends))
	}
	for name, b := range backends {
		if _, ok := b.proxy.Transport.(*http.Transport); ok || b.proxy.Transport == nil {
			t.Errorf("backend %q does not get the connect timeout", name)
		}
		if b.proxy.ErrorHandler == nil {
			t.Errorf("backend %q answers its errors with a 502", name)
		}
	}
}

func TestCLITransportFitsTheLongestConnectTimeout(t *testing.T) {
	timeouts := make(backendTimeoutFlags)
	mustSet(t, &timeouts, "origin=45000,0,0")
	if got := newCLITransport(timeouts).TLSHandshakeTimeout; got < 45*time.Second {
		t.Errorf("handshake limit = %v, want at least 45s", got)
	}
}

// An override for a backend nobody configured is the shape a typo takes.
func TestBuildBackendOptionsRejectsUnknownBackend(t *testing.T) {
	backends := make(backendFlags)
	mustSet(t, &backends, "origin=localhost:9000")

	for _, override := range []string{"orgin=origin.example.com", "origin.example.com"} {
		overrides := make(overrideHostFlags)
		mustSet(t, &overrides, override)
		if _, err := buildBackendOptions(backends, overrides, nil); err == nil {
			t.Errorf("buildBackendOptions() with override %q succeeded, want an error", override)
		}
	}

	timeouts := make(backendTimeoutFlags)
	mustSet(t, &timeouts, "orgin=1000,15000,10000")
	if _, err := buildBackendOptions(backends, nil, timeouts); err == nil {
		t.Error("buildBackendOptions() with timeouts for an unknown backend succeeded, want an error")
	}
}

func TestBackendTimeoutFlags(t *testing.T) {
	timeouts := make(backendTimeoutFlags)
	mustSet(t, &timeouts, "api=2000, 0,60000")
	want := backendTimeouts{connectMs: 2000, firstByteMs: 0, betweenBytesMs: 60000}
	if got := timeouts["api"]; got != want {
		t.Errorf("timeouts = %+v, want %+v", got, want)
	}

	for _, v := range []string{
		"1000,15000,10000",
		"=1000,15000,10000",
		"api=1000,15000",
		"api=1000,15000,10000,5",
		"api=1s,15000,10000",
		"api=-1,15000,10000",
		"api=4294967296,15000,10000",
		"api=0,15000,10000",
	} {
		if err := timeouts.Set(v); err == nil {
			t.Errorf("Set(%q) succeeded, want an error", v)
		}
	}
}

func TestBackendConfig(t *testing.T) {
	uptime := uint8(50)
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: "localhost:9000"})
	timeouts := backendTimeouts{connectMs: 2000, firstByteMs: 15000, betweenBytesMs: 10000}
	transport := newCLITransport(nil)
	config := backendConfig("origin", backend{proxy: proxy, uptime: &uptime}, "origin.example.com", timeouts, transport)

	if config.OverrideHost != "origin.example.com" {
		t.Errorf("OverrideHost = %q, want %q", config.OverrideHost, "origin.example.com")
	}
	if config.UptimePercent == nil || *config.UptimePercent != 50 {
		t.Errorf("UptimePercent = %v, want 50", config.UptimePercent)
	}
	// Losing the transport here costs the profile recorder its phase data.
	if config.Transport != transport {
		t.Error("Transport does not match the shared CLI transport")
	}
	if config.ConnectTimeoutMs != 2000 || config.FirstByteTimeoutMs != 15000 || config.BetweenBytesTimeoutMs != 10000 {
		t.Errorf("timeouts = %d, %d, %d, want 2000, 15000, 10000", config.ConnectTimeoutMs, config.FirstByteTimeoutMs, config.BetweenBytesTimeoutMs)
	}
}

// The shared transport cannot enforce a first-byte timeout per backend, so
// the proxy has to.
func TestBackendFirstByteTimeout(t *testing.T) {
	release := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	defer origin.Close()
	defer close(release)

	backends := make(backendFlags)
	mustSet(t, &backends, "origin="+origin.URL)
	timeouts := make(backendTimeoutFlags)
	mustSet(t, &timeouts, "origin=1000,50,0")
	if _, err := buildBackendOptions(backends, nil, timeouts); err != nil {
		t.Fatalf("buildBackendOptions() error = %v", err)
	}

	w := httptest.NewRecorder()
	served := make(chan struct{})
	go func() {
		defer close(served)
		backends["origin"].proxy.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	}()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("the proxy is still waiting, want the 50 ms timeout")
	}
	// Outside a guest's send, the error handler falls back to a 502.
	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}
}

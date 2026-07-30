package main

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"

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

func TestBuildBackendOptions(t *testing.T) {
	backends := make(backendFlags)
	if err := backends.Set("origin=localhost:9000"); err != nil {
		t.Fatalf("backends.Set() error = %v", err)
	}
	if err := backends.Set("localhost:9001"); err != nil {
		t.Fatalf("backends.Set() error = %v", err)
	}

	overrides := make(overrideHostFlags)
	if err := overrides.Set("origin=origin.example.com"); err != nil {
		t.Fatalf("overrides.Set() error = %v", err)
	}

	opts, err := buildBackendOptions(backends, overrides)
	if err != nil {
		t.Fatalf("buildBackendOptions() error = %v", err)
	}
	if len(opts) != len(backends) {
		t.Errorf("options = %d, want %d", len(opts), len(backends))
	}
}

// An override for a backend nobody configured is the shape a typo takes.
func TestBuildBackendOptionsRejectsUnknownBackend(t *testing.T) {
	backends := make(backendFlags)
	if err := backends.Set("origin=localhost:9000"); err != nil {
		t.Fatalf("backends.Set() error = %v", err)
	}

	for _, override := range []string{"orgin=origin.example.com", "origin.example.com"} {
		overrides := make(overrideHostFlags)
		if err := overrides.Set(override); err != nil {
			t.Fatalf("overrides.Set(%q) error = %v", override, err)
		}

		if _, err := buildBackendOptions(backends, overrides); err == nil {
			t.Errorf("buildBackendOptions() with override %q succeeded, want an error", override)
		}
	}
}

func TestNamedBackendConfig(t *testing.T) {
	proxy := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	uptime := uint8(50)
	config := namedBackendConfig("origin", proxy, &uptime, "origin.example.com")

	if config.OverrideHost != "origin.example.com" {
		t.Errorf("OverrideHost = %q, want %q", config.OverrideHost, "origin.example.com")
	}
	if config.UptimePercent == nil || *config.UptimePercent != 50 {
		t.Errorf("UptimePercent = %v, want 50", config.UptimePercent)
	}
	// Losing the transport here costs the profile recorder its phase data.
	if config.Transport != cliTransport {
		t.Error("Transport does not match the shared CLI transport")
	}
}

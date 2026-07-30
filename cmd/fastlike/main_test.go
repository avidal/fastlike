package main

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
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

func TestOverrideHostFlagsRejectInvalidValue(t *testing.T) {
	flags := make(overrideHostFlags)

	for _, value := range []string{"origin", "=origin.example.com", "origin="} {
		if err := flags.Set(value); err == nil {
			t.Errorf("Set(%q) succeeded, want an error", value)
		}
	}
}

func TestWithOverrideHost(t *testing.T) {
	var receivedHost string
	origin := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		receivedHost = request.Host
	}))
	defer origin.Close()

	target, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	response := httptest.NewRecorder()

	withOverrideHost(proxy, "origin.example.com").ServeHTTP(response, request)

	if receivedHost != "origin.example.com" {
		t.Errorf("forwarded Host = %q, want %q", receivedHost, "origin.example.com")
	}
	if request.Host != "localhost" {
		t.Errorf("source Host = %q, want %q", request.Host, "localhost")
	}
}

package profile

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsLoopbackBindAddress(t *testing.T) {
	loopback := []string{
		"127.0.0.1:9000",
		"127.0.0.1",
		"127.5.5.5:80",
		"[::1]:9000",
		"::1",
		"localhost:9000",
		"LOCALHOST:9000",
	}
	for _, addr := range loopback {
		if !IsLoopbackBindAddress(addr) {
			t.Errorf("%q should be loopback", addr)
		}
	}

	nonLoopback := []string{
		"0.0.0.0:9000",
		"10.0.0.5:9000",
		"192.168.1.1",
		"[2001:db8::1]:9000",
		":9000",              // wildcard bind: not safe to treat as loopback
		"",                   // empty: no listener case, but the helper is conservative
		"example.com:9000",   // arbitrary hostname: auth required
		"unix:/tmp/sock",     // path-shaped: net.Listen("tcp", ...) would fail anyway
		"/tmp/fastlike.sock", // path-shaped: same
	}
	for _, addr := range nonLoopback {
		if IsLoopbackBindAddress(addr) {
			t.Errorf("%q should NOT be loopback", addr)
		}
	}
}

func TestValidateProfileUIAuthLoopbackNoAuth(t *testing.T) {
	if err := ValidateProfileUIAuth("127.0.0.1:9000", "", false); err != nil {
		t.Errorf("loopback with no auth should be permitted: %v", err)
	}
}

func TestValidateProfileUIAuthEmptyAddrNoOp(t *testing.T) {
	if err := ValidateProfileUIAuth("", "", false); err != nil {
		t.Errorf("empty addr should be a no-op: %v", err)
	}
}

func TestValidateProfileUIAuthNonLoopbackRequiresAuth(t *testing.T) {
	err := ValidateProfileUIAuth("0.0.0.0:9000", "", false)
	if err == nil {
		t.Fatal("non-loopback without auth must error")
	}
	if !errors.Is(err, ErrProfileUIAuthMissing) {
		t.Errorf("expected ErrProfileUIAuthMissing, got %T %v", err, err)
	}
}

func TestValidateProfileUIAuthNonLoopbackWithToken(t *testing.T) {
	if err := ValidateProfileUIAuth("0.0.0.0:9000", "supersecret", false); err != nil {
		t.Errorf("non-loopback with token should be permitted: %v", err)
	}
}

func TestValidateProfileUIAuthNonLoopbackWithInsecureFlag(t *testing.T) {
	if err := ValidateProfileUIAuth("0.0.0.0:9000", "", true); err != nil {
		t.Errorf("non-loopback with insecure flag should be permitted: %v", err)
	}
}

func TestWrapProfileUIAuthAcceptsCorrectBearer(t *testing.T) {
	h := WrapProfileUIAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}), "tokenA")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer tokenA")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusTeapot {
		t.Errorf("status: %d, want 418", w.Code)
	}
}

func TestWrapProfileUIAuthRejectsMissingHeader(t *testing.T) {
	h := WrapProfileUIAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), "tokenA")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: %d, want 401", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); got == "" {
		t.Errorf("missing WWW-Authenticate header")
	}
}

func TestWrapProfileUIAuthRejectsWrongToken(t *testing.T) {
	h := WrapProfileUIAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), "tokenA")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer tokenB")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d, want 401", w.Code)
	}
}

func TestWrapProfileUIAuthRejectsWrongScheme(t *testing.T) {
	h := WrapProfileUIAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), "tokenA")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic tokenA")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong scheme: %d, want 401", w.Code)
	}
}

func TestWrapProfileUIAuthEmptyTokenIsPassthrough(t *testing.T) {
	called := false
	h := WrapProfileUIAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}), "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !called {
		t.Fatal("empty token should not gate the handler")
	}
}

func TestConstantTimeStringEq(t *testing.T) {
	cases := map[[2]string]bool{
		{"", ""}:                true,
		{"abc", "abc"}:          true,
		{"abc", "abd"}:          false,
		{"abc", "ab"}:           false,
		{"", "x"}:               false,
		{"long-string", "long"}: false,
	}
	for in, want := range cases {
		if got := constantTimeStringEq(in[0], in[1]); got != want {
			t.Errorf("eq(%q, %q) = %v, want %v", in[0], in[1], got, want)
		}
	}
}

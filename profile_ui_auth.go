package fastlike

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// IsLoopbackBindAddress reports whether addr is a loopback bind for purposes
// of the UI auth gate. The check happens at config time, not on every
// request: the operator picked the address up-front and we want one
// authoritative answer about whether bearer auth is required.
//
// Returns true only for:
//   - any IP literal in 127.0.0.0/8
//   - "::1"
//   - the literal hostname "localhost" (case-insensitive)
//
// Wildcard binds (":<port>", "0.0.0.0", "[::]") and any other hostname
// require -profile-auth or -profile-insecure-ui. Resolving arbitrary
// hostnames is intentionally NOT supported: the auth gate must not depend
// on resolver state at startup, which can disagree with whatever net.Listen
// ultimately binds. Unix-socket paths are not accepted either — the profile
// UI binds via net.Listen("tcp", ...) and a path-shaped addr would just
// crash the process at bind time.
func IsLoopbackBindAddress(addr string) bool {
	if addr == "" {
		return false
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// No port present; treat addr itself as host.
		host = addr
	}
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}

// ErrProfileUIAuthMissing is returned by ValidateProfileUIAuth when the
// supplied bind/auth combination violates the security policy. The error
// message names the flag the operator needs to add so the failure mode
// is self-documenting.
var ErrProfileUIAuthMissing = errors.New("profile UI auth required for non-loopback bind")

// ValidateProfileUIAuth enforces the security policy from
// plans/guest-profiling.md: loopback binds need no auth, non-loopback
// binds require either a bearer token or the explicit insecure-ui escape
// hatch. The caller (CLI startup, embedder bootstrap) should hard-exit on
// a non-nil error.
func ValidateProfileUIAuth(addr, token string, insecure bool) error {
	if addr == "" {
		return nil
	}
	if IsLoopbackBindAddress(addr) {
		return nil
	}
	if token != "" || insecure {
		return nil
	}
	return fmt.Errorf("%w (%q): pass -profile-auth TOKEN, or -profile-insecure-ui if the bind has externalized auth", ErrProfileUIAuthMissing, addr)
}

// WrapProfileUIAuth wraps h with a bearer-token check when token is
// non-empty. The middleware enforces the token on every request including
// the index, JSON endpoints, and any future SSE stream. When token is
// empty the handler is returned unwrapped.
//
// The constant-time comparison avoids the timing oracle a naive `==`
// would create, even though the token is configured by the operator
// rather than the network.
func WrapProfileUIAuth(h http.Handler, token string) http.Handler {
	if token == "" {
		return h
	}
	expected := "Bearer " + token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if !constantTimeStringEq(got, expected) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="fastlike-profiler"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// constantTimeStringEq returns true when a and b are equal-length and
// byte-equal in constant time. crypto/subtle.ConstantTimeCompare is the
// canonical primitive; we wrap it to keep call sites readable.
func constantTimeStringEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

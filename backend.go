package fastlike

import (
	"context"
	"crypto/tls"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"time"
)

// syntheticFailureCtxKey is the context key the reliability wrapper uses to
// signal a short-circuited synthetic 502 to the backend recorder. The
// recorder reads the flag from the request context after the handler
// returns and tags the BackendCall with BackendOutcomeSyntheticFailure
// when set. Embedders cannot collide with the key because the type is
// unexported.
type syntheticFailureCtxKey struct{}

// markSyntheticFailure stores a sentinel on the request context so the
// recorder can distinguish a synthetic 502 from a genuine one. The pointer
// targets a per-request bool so multiple ServeHTTP layers see consistent
// state without allocating per request beyond the bool itself.
func markSyntheticFailure(ctx context.Context, flag *bool) context.Context {
	return context.WithValue(ctx, syntheticFailureCtxKey{}, flag)
}

func syntheticFailureFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	if flag, ok := ctx.Value(syntheticFailureCtxKey{}).(*bool); ok && flag != nil {
		return *flag
	}
	return false
}

// Backend represents a complete backend configuration with all introspectable properties
type Backend struct {
	// Name is the identifier for this backend
	Name string

	// URL is the target URL for this backend (scheme, host, port)
	URL *url.URL

	// OverrideHost is an optional Host header override
	OverrideHost string

	// Handler is the actual http.Handler used to make requests
	Handler http.Handler

	// IsDynamic indicates if this backend was registered at runtime
	IsDynamic bool

	// Timeout settings (in milliseconds)
	ConnectTimeoutMs      uint32
	FirstByteTimeoutMs    uint32
	BetweenBytesTimeoutMs uint32

	// HTTP keepalive time (in milliseconds)
	HTTPKeepaliveTimeMs uint32

	// TCP keepalive settings
	TCPKeepaliveEnable     bool
	TCPKeepaliveTimeMs     uint32
	TCPKeepaliveIntervalMs uint32
	TCPKeepaliveProbes     uint32

	// SSL/TLS settings
	UseSSL        bool
	SSLMinVersion uint32 // TLS version constant
	SSLMaxVersion uint32 // TLS version constant

	// Connection pool settings
	PreferIPv6     bool   // Prefer IPv6 addresses over IPv4 when resolving backends
	MaxConnections uint32 // Maximum connections in pool (0 = unlimited)
	MaxUse         uint32 // How many times a pooled connection can be reused (0 = unlimited)
	MaxLifetimeMs  uint32 // Upper bound for how long a keepalive connection can remain open (0 = unlimited)

	// CacheKey is the cache key override for shield backends
	CacheKey string

	// UptimePercent simulates backend reliability for testing. When non-nil, each
	// request to this backend has a UptimePercent / 100 chance of being forwarded
	// normally; otherwise the runtime synthesises a 502 response identical to the
	// one produced when a real upstream is unreachable. Valid values are 0..100;
	// 0 means the backend always appears down, 100 means no simulation. A nil
	// value disables simulation entirely (the default for every existing
	// construction path).
	UptimePercent *uint8

	// Transport is the optional *http.Transport that the registered Handler
	// actually dispatches through. When non-nil, fastlike attaches an
	// httptrace.ClientTrace via per-request context so the profile recorder
	// can capture DNS / connect / TLS / TTFB phase timings. The transport is
	// embedder-owned: fastlike does not clone, mutate, or close it.
	// Backends registered via WithBackend keep this field nil and surface
	// only the total span; phase fields stay nil in the trace.
	Transport *http.Transport
}

// addBackend registers a backend with the given name and configuration.
func (i *Instance) addBackend(name string, b *Backend) {
	b.Name = name
	if b.Handler != nil {
		b.Handler = wrapWithReliability(b.Handler, b.UptimePercent)
	}
	i.backends[name] = b
}

// wrapWithReliability returns a handler that simulates backend failures based
// on the supplied uptime percentage. A nil percentage or a value of 100 short
// circuits and returns the original handler unchanged; any other value in
// [0, 99] makes the wrapper draw a random number per request and emit a 502
// when the draw falls outside the success window. The 502 body matches the
// shape produced by the real RoundTrip failure path so guest code observes
// identical behavior to a genuine outage.
func wrapWithReliability(h http.Handler, uptime *uint8) http.Handler {
	if uptime == nil || *uptime >= 100 {
		return h
	}
	pct := *uptime
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if uint8(rand.IntN(100)) >= pct {
			if flag, ok := r.Context().Value(syntheticFailureCtxKey{}).(*bool); ok && flag != nil {
				*flag = true
			}
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, "Backend request failed: simulated backend failure (uptime=%d%%)", pct)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// getBackend retrieves a backend by name. Returns nil if not found.
func (i *Instance) getBackend(name string) *Backend {
	return i.backends[name]
}

// backendExists checks whether a backend with the given name is registered.
func (i *Instance) backendExists(name string) bool {
	_, ok := i.backends[name]
	return ok
}

// getBackendHandler retrieves the http.Handler for a named backend.
// Falls back to the default backend handler if the backend is not found.
func (i *Instance) getBackendHandler(name string) http.Handler {
	b := i.getBackend(name)
	if b == nil {
		return i.defaultBackend(name)
	}

	return b.Handler
}

// defaultBackend returns a handler that responds with 502 Bad Gateway for unknown backends.
func defaultBackend(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		msg := fmt.Sprintf(`Unknown backend '%s'. Did you configure your backends correctly?`, name)
		_, _ = w.Write([]byte(msg))
	})
}

// fastlyTLSVersionToGo converts Fastly TLS version constants to Go's tls package constants.
func fastlyTLSVersionToGo(v uint32) uint16 {
	switch v {
	case TLSv10:
		return tls.VersionTLS10
	case TLSv11:
		return tls.VersionTLS11
	case TLSv12:
		return tls.VersionTLS12
	case TLSv13:
		return tls.VersionTLS13
	default:
		return 0
	}
}

// CreateTransport creates an http.Transport configured according to the backend's settings.
func (b *Backend) CreateTransport() *http.Transport {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	if b.ConnectTimeoutMs > 0 {
		transport.DialContext = (&net.Dialer{
			Timeout:   time.Duration(b.ConnectTimeoutMs) * time.Millisecond,
			KeepAlive: 30 * time.Second,
		}).DialContext
	}

	if b.UseSSL || b.SSLMinVersion > 0 || b.SSLMaxVersion > 0 {
		tlsConfig := &tls.Config{}

		if b.SSLMinVersion > 0 {
			tlsConfig.MinVersion = fastlyTLSVersionToGo(b.SSLMinVersion)
		}
		if b.SSLMaxVersion > 0 {
			tlsConfig.MaxVersion = fastlyTLSVersionToGo(b.SSLMaxVersion)
		}

		transport.TLSClientConfig = tlsConfig
	}

	if b.FirstByteTimeoutMs > 0 {
		transport.ResponseHeaderTimeout = time.Duration(b.FirstByteTimeoutMs) * time.Millisecond
	}

	// Apply connection pool settings
	if b.MaxConnections > 0 {
		transport.MaxIdleConns = int(b.MaxConnections)
		transport.MaxIdleConnsPerHost = int(b.MaxConnections)
	}

	if b.MaxLifetimeMs > 0 {
		transport.IdleConnTimeout = time.Duration(b.MaxLifetimeMs) * time.Millisecond
	}

	return transport
}

// DynamicBackendConfig represents the configuration structure passed from guest code
// when registering a dynamic backend at runtime.
// This struct maps directly to the dynamic_backend_config struct in the XQD ABI,
// with all pointer fields and lengths matching the C-style interface.
type DynamicBackendConfig struct {
	HostOverride             int32  // Pointer to host override string
	HostOverrideLen          uint32 // Length of host override string
	ConnectTimeoutMs         uint32 // Connection timeout in milliseconds
	FirstByteTimeoutMs       uint32 // First byte timeout in milliseconds
	BetweenBytesTimeoutMs    uint32 // Between bytes timeout in milliseconds
	SSLMinVersion            uint32 // Minimum TLS version
	SSLMaxVersion            uint32 // Maximum TLS version
	CertHostname             int32  // Pointer to certificate hostname
	CertHostnameLen          uint32 // Length of certificate hostname
	CACert                   int32  // Pointer to CA certificate
	CACertLen                uint32 // Length of CA certificate
	Ciphers                  int32  // Pointer to cipher list
	CiphersLen               uint32 // Length of cipher list
	SNIHostname              int32  // Pointer to SNI hostname
	SNIHostnameLen           uint32 // Length of SNI hostname
	ClientCertificate        int32  // Pointer to client certificate
	ClientCertificateLen     uint32 // Length of client certificate
	ClientKey                uint32 // Secret handle for client key
	HTTPKeepaliveTimeMs      uint32 // HTTP keepalive time in milliseconds
	TCPKeepaliveEnable       uint32 // TCP keepalive enabled (0 or 1)
	TCPKeepaliveIntervalSecs uint32 // TCP keepalive interval in seconds
	TCPKeepaliveProbes       uint32 // Number of TCP keepalive probes
	TCPKeepaliveTimeSecs     uint32 // TCP keepalive time in seconds

	// Connection pool settings
	PreferIPv6     uint32 // Prefer IPv6 over IPv4 (0 or 1)
	MaxConnections uint32 // Max connections in pool (0 = unlimited)
	MaxUse         uint32 // How many times a pooled connection can be reused (0 = unlimited)
	MaxLifetimeMs  uint32 // Upper bound for keepalive connection lifetime (0 = unlimited)
}

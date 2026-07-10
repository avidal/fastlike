package fastlike

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"sync"
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

// backendSendErrorCtxKey is the context key a fastlike-managed backend handler
// uses to hand a transport failure back to the send hostcall, which surfaces it
// to the guest as a send error rather than a synthetic 502. Unexported so
// embedders cannot collide.
type backendSendErrorCtxKey struct{}

// markBackendSendError installs errp on ctx for a backend handler to record a
// transport failure into, to be read back after the handler returns.
func markBackendSendError(ctx context.Context, errp *error) context.Context {
	return context.WithValue(ctx, backendSendErrorCtxKey{}, errp)
}

// captureBackendSendError records err on the pointer installed by
// markBackendSendError. With no pointer installed (e.g. an embedder calling the
// handler directly) it is a no-op and the handler's synthetic 502 stands.
func captureBackendSendError(ctx context.Context, err error) {
	if p, ok := ctx.Value(backendSendErrorCtxKey{}).(*error); ok && p != nil {
		*p = err
	}
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

	// TCP keepalive settings.
	// TCPKeepaliveSet records that the keepalive option was configured, so
	// an explicit enable=0 can be told apart from the default.
	TCPKeepaliveSet        bool
	TCPKeepaliveEnable     bool
	TCPKeepaliveTimeMs     uint32
	TCPKeepaliveIntervalMs uint32
	TCPKeepaliveProbes     uint32

	// SSL/TLS settings.
	// The version fields hold Fastly TLS version constants, where TLSv10 is
	// zero, so the Set flags record whether a version was configured at all.
	UseSSL           bool
	SSLMinVersion    uint32
	SSLMinVersionSet bool
	SSLMaxVersion    uint32
	SSLMaxVersionSet bool

	// CertHostname is the hostname the server certificate must be valid for.
	// Empty means the target host.
	CertHostname string

	// SNIHostname is the hostname sent in the TLS SNI extension.
	// Empty means the certificate verification hostname.
	SNIHostname string

	// DisableSNI suppresses the SNI extension entirely, matching a dynamic
	// backend registered with an empty SNI hostname.
	DisableSNI bool

	// CACerts holds additional root certificates to trust for this backend.
	// Nil means the system roots.
	CACerts *x509.CertPool

	// Ciphers is the OpenSSL-format cipher list requested for this backend.
	// Go's TLS stack picks its own suites, so this is recorded but not enforced.
	Ciphers string

	// ClientCert is presented to the origin during the TLS handshake for
	// backends registered with a client certificate (mutual TLS).
	ClientCert *tls.Certificate

	// DontPool disables connection reuse for this backend.
	DontPool bool

	// GRPC forces HTTP/2 for this backend, including unencrypted HTTP/2 when
	// UseSSL is off, matching viceroy's h2-only client for gRPC backends.
	GRPC bool

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
	i.backendsMu.Lock()
	i.backends[name] = b
	i.backendsMu.Unlock()
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
			_, _ = fmt.Fprintf(w, "Backend request failed: simulated backend failure (uptime=%d%%)", pct)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// getBackend retrieves a backend by name. Returns nil if not found.
func (i *Instance) getBackend(name string) *Backend {
	i.backendsMu.RLock()
	defer i.backendsMu.RUnlock()
	return i.backends[name]
}

// backendHealth derives the value reported by the fastly_backend_is_healthy
// hostcall from a backend's configured reliability. A nil UptimePercent means
// no reliability was configured, so health is unknown, matching production for
// a backend without health checks and preserving the historical default. An
// explicit 0% uptime reads unhealthy; any positive uptime reads healthy.
// Health is intentionally derived from delivery reliability rather than
// configured separately: a backend simulated as always-down also reports
// unhealthy. Note that an explicit 100% keeps UptimePercent non-nil (the
// short circuit in wrapWithReliability only skips the failure wrapper), so it
// reads healthy and stays distinguishable from the no-simulation nil case.
func backendHealth(b *Backend) uint32 {
	if b.UptimePercent == nil {
		return BackendHealthUnknown
	}
	if *b.UptimePercent == 0 {
		return BackendHealthUnhealthy
	}
	return BackendHealthHealthy
}

// backendExists checks whether a backend with the given name is registered.
func (i *Instance) backendExists(name string) bool {
	i.backendsMu.RLock()
	defer i.backendsMu.RUnlock()
	_, ok := i.backends[name]
	return ok
}

// resolveBackendHandler resolves a backend name to the handler a send
// hostcall dispatches through, plus whether the backend exposes a transport
// fastlike can trace.
func (i *Instance) resolveBackendHandler(name string) (http.Handler, bool) {
	if name == "geolocation" {
		return geoHandler(i.geolookup), false
	}
	b := i.getBackend(name)
	if b == nil {
		return i.defaultBackend(name), false
	}
	return b.Handler, b.Transport != nil
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

// betweenBytesBody enforces a maximum delay between successive reads of a
// response body.
// When the body stays idle past the timeout it is closed, which unblocks a
// pending read with an error, matching the between_bytes_timeout behavior of
// a real backend.
// Expiry re-arms itself based on the last activity time instead of racing
// each read against a timer reset, so a chunk that arrives just before the
// deadline cannot be misclassified as a stall.
type betweenBytesBody struct {
	rc      io.ReadCloser
	timeout time.Duration

	mu     sync.Mutex
	last   time.Time
	closed bool
	timer  *time.Timer
}

func newBetweenBytesBody(rc io.ReadCloser, timeout time.Duration) *betweenBytesBody {
	b := &betweenBytesBody{rc: rc, timeout: timeout, last: time.Now()}
	b.timer = time.AfterFunc(timeout, b.expire)
	return b
}

func (b *betweenBytesBody) expire() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	if idle := time.Since(b.last); idle < b.timeout {
		b.timer.Reset(b.timeout - idle)
		return
	}
	b.closed = true
	_ = b.rc.Close()
}

func (b *betweenBytesBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	b.mu.Lock()
	b.last = time.Now()
	b.mu.Unlock()
	return n, err
}

func (b *betweenBytesBody) Close() error {
	b.mu.Lock()
	b.closed = true
	b.timer.Stop()
	b.mu.Unlock()
	return b.rc.Close()
}

// sslMinConfigured reports whether a minimum TLS version was configured,
// via the Set flag (dynamic backends, where TLSv10 encodes as zero) or a
// non-zero version from an embedder that never sets the flag.
func (b *Backend) sslMinConfigured() bool {
	return b.SSLMinVersionSet || b.SSLMinVersion > 0
}

func (b *Backend) sslMaxConfigured() bool {
	return b.SSLMaxVersionSet || b.SSLMaxVersion > 0
}

// tlsClientConfig builds the TLS client configuration for a backend.
// The certificate is verified against CertHostname, falling back to the
// target host, while SNIHostname only controls what goes on the wire.
// Go's ServerName drives both at once, so when the two names differ (or SNI
// is suppressed entirely) verification is done by hand via VerifyConnection.
func (b *Backend) tlsClientConfig() *tls.Config {
	cfg := &tls.Config{}

	if b.sslMinConfigured() {
		cfg.MinVersion = fastlyTLSVersionToGo(b.SSLMinVersion)
	}
	if b.sslMaxConfigured() {
		cfg.MaxVersion = fastlyTLSVersionToGo(b.SSLMaxVersion)
	}
	if b.CACerts != nil {
		cfg.RootCAs = b.CACerts
	}
	if b.ClientCert != nil {
		cfg.Certificates = []tls.Certificate{*b.ClientCert}
	}

	// Like viceroy, the certificate is verified against the cert hostname,
	// falling back to the SNI hostname and then the target host.
	verifyName := b.CertHostname
	if verifyName == "" {
		verifyName = b.SNIHostname
	}
	if verifyName == "" && b.URL != nil {
		verifyName = b.URL.Hostname()
	}

	sniName := ""
	if !b.DisableSNI {
		sniName = b.SNIHostname
		if sniName == "" {
			sniName = verifyName
		}
	}

	cfg.ServerName = sniName
	if sniName == verifyName {
		return cfg
	}

	roots := b.CACerts
	cfg.InsecureSkipVerify = true
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return fmt.Errorf("backend %q presented no certificate", b.Name)
		}
		opts := x509.VerifyOptions{
			DNSName:       verifyName,
			Roots:         roots,
			Intermediates: x509.NewCertPool(),
		}
		for _, cert := range cs.PeerCertificates[1:] {
			opts.Intermediates.AddCert(cert)
		}
		_, err := cs.PeerCertificates[0].Verify(opts)
		return err
	}
	return cfg
}

// CreateTransport creates an http.Transport configured according to the backend's settings.
func (b *Backend) CreateTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	if b.ConnectTimeoutMs > 0 {
		dialer.Timeout = time.Duration(b.ConnectTimeoutMs) * time.Millisecond
	}
	if b.TCPKeepaliveSet {
		if b.TCPKeepaliveEnable {
			cfg := net.KeepAliveConfig{Enable: true}
			if b.TCPKeepaliveTimeMs > 0 {
				cfg.Idle = time.Duration(b.TCPKeepaliveTimeMs) * time.Millisecond
			}
			if b.TCPKeepaliveIntervalMs > 0 {
				cfg.Interval = time.Duration(b.TCPKeepaliveIntervalMs) * time.Millisecond
			}
			if b.TCPKeepaliveProbes > 0 {
				cfg.Count = int(b.TCPKeepaliveProbes)
			}
			dialer.KeepAliveConfig = cfg
		} else {
			dialer.KeepAlive = -1
		}
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	if b.UseSSL || b.sslMinConfigured() || b.sslMaxConfigured() {
		transport.TLSClientConfig = b.tlsClientConfig()
	}

	if b.GRPC {
		protocols := new(http.Protocols)
		protocols.SetHTTP2(true)
		if !b.UseSSL {
			protocols.SetUnencryptedHTTP2(true)
		}
		transport.Protocols = protocols
	}

	if b.DontPool {
		transport.DisableKeepAlives = true
	}

	if b.HTTPKeepaliveTimeMs > 0 {
		transport.IdleConnTimeout = time.Duration(b.HTTPKeepaliveTimeMs) * time.Millisecond
	}

	if b.FirstByteTimeoutMs > 0 {
		transport.ResponseHeaderTimeout = time.Duration(b.FirstByteTimeoutMs) * time.Millisecond
	}

	// Apply connection pool settings
	if b.MaxConnections > 0 {
		transport.MaxIdleConns = int(b.MaxConnections)
		transport.MaxIdleConnsPerHost = int(b.MaxConnections)
		if b.IsDynamic {
			// pooling_limits.max_connections is a hard cap on a dynamic
			// backend, while embedder-configured static backends keep the
			// historical idle-pool semantics of this field.
			transport.MaxConnsPerHost = int(b.MaxConnections)
		}
	}

	if b.MaxLifetimeMs > 0 {
		transport.IdleConnTimeout = time.Duration(b.MaxLifetimeMs) * time.Millisecond
	}

	return transport
}

// newTransportHandler builds the proxy handler for a fastlike-managed
// backend, recording the transport on the Backend so tracing and reset()
// cleanup can observe it.
func (b *Backend) newTransportHandler() http.Handler {
	transport := b.CreateTransport()
	b.Transport = transport
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The connection goes to the registered target, while the Host
		// header follows viceroy's precedence: the host override, then the
		// Host header from the guest request, then the request URI's
		// authority.
		if b.OverrideHost != "" {
			r.Host = b.OverrideHost
		} else if r.Host == "" {
			r.Host = r.URL.Host
		}
		r.URL.Scheme = b.URL.Scheme
		r.URL.Host = b.URL.Host

		resp, err := transport.RoundTrip(r)
		if err != nil {
			captureBackendSendError(r.Context(), err)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = fmt.Fprintf(w, "Backend request failed: %v", err)
			return
		}
		body := io.ReadCloser(resp.Body)
		if b.BetweenBytesTimeoutMs > 0 {
			body = newBetweenBytesBody(resp.Body, time.Duration(b.BetweenBytesTimeoutMs)*time.Millisecond)
		}
		defer func() { _ = body.Close() }()

		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, body)
	})
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
	MaxConnections uint32 // Max connections in pool (0 = unlimited)
	MaxUse         uint32 // How many times a pooled connection can be reused (0 = unlimited)
	MaxLifetimeMs  uint32 // Upper bound for keepalive connection lifetime (0 = unlimited)
}

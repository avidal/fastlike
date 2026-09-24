package fastlike

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptrace"
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

	dynamicRegistration *dynamicBackendRegistration

	// Timeout settings (in milliseconds).
	// Zero disables a first-byte or between-bytes timeout, and means the
	// default connect timeout unless ConnectTimeoutSet.
	ConnectTimeoutMs      uint32
	ConnectTimeoutSet     bool
	FirstByteTimeoutMs    uint32
	BetweenBytesTimeoutMs uint32

	// Guest overrides of a shield's timeouts, which only requests see.
	firstByteOverrideMs    *uint32
	betweenBytesOverrideMs *uint32

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
	MaxConnections uint32 // Requests waiting for their headers at once, and idle pool size (0 = unlimited)
	MaxUse         uint32 // How many times a pooled connection can be reused (0 = unlimited)
	MaxLifetimeMs  uint32 // Upper bound for how long a keepalive connection can remain open (0 = unlimited)

	// CacheKey is the cache key override for shield backends
	CacheKey string

	// UptimePercent simulates backend reliability for testing. When non-nil, each
	// request to this backend has a UptimePercent / 100 chance of being forwarded
	// normally; otherwise the runtime synthesises a 502 response. Valid values are 0..100;
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

type dynamicBackendRegistration struct {
	target                      string
	options                     uint32
	hostOverride                string
	connectTimeoutMs            uint32
	firstByteTimeoutMs          uint32
	betweenBytesTimeoutMs       uint32
	sslMinVersion               uint32
	sslMaxVersion               uint32
	certHostname                string
	caCert                      string
	ciphers                     string
	sniHostname                 string
	clientCertificate           string
	clientKeySHA256             [32]byte
	httpKeepaliveTimeMs         uint32
	tcpKeepaliveEnable          uint32
	tcpKeepaliveIntervalSeconds uint32
	tcpKeepaliveProbes          uint32
	tcpKeepaliveTimeSeconds     uint32
	maxConnections              uint32
	maxUse                      uint32
	maxLifetimeMs               uint32
	healthcheck                 dynamicBackendHealthcheck
}

type dynamicBackendHealthcheck struct {
	intervalMs     uint64
	timeoutMs      uint64
	host           string
	method         string
	path           string
	expectedStatus uint16
	window         uint32
	threshold      uint32
	initial        uint32
}

func prepareBackend(name string, b *Backend) {
	b.Name = name
	if b.URL == nil {
		b.URL = backendURL(name)
	}
	if b.Handler != nil {
		b.Handler = wrapWithReliability(OverrideHostHandler(b.Handler, b.OverrideHost), b.UptimePercent)
	}
}

// addBackend registers a backend with the given name and configuration.
func (i *Instance) addBackend(name string, b *Backend) {
	prepareBackend(name, b)
	i.backendsMu.Lock()
	i.backends[name] = b
	i.backendsMu.Unlock()
}

func (i *Instance) addDynamicBackend(name string, b *Backend) bool {
	i.backendsMu.Lock()
	defer i.backendsMu.Unlock()

	if existing, ok := i.backends[name]; ok {
		return existing.IsDynamic &&
			existing.dynamicRegistration != nil &&
			b.dynamicRegistration != nil &&
			*existing.dynamicRegistration == *b.dynamicRegistration
	}

	b.Handler = b.newTransportHandler()
	prepareBackend(name, b)
	i.backends[name] = b
	return true
}

// OverrideHostHandler returns a handler that sends every request it forwards
// with the given Host header, and returns h untouched for an empty host.
// The override beats whatever Host the guest set, matching the precedence
// Fastly gives the override_host property.
//
// Registering a backend applies this already. It is exported for the handlers
// that never become a named Backend, such as a default-backend factory.
func OverrideHostHandler(h http.Handler, host string) http.Handler {
	if host == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Copy so the caller's request survives the rewrite.
		outbound := *r
		outbound.Host = host
		h.ServeHTTP(w, &outbound)
	})
}

// wrapWithReliability returns a handler that simulates backend failures based
// on the supplied uptime percentage. A nil percentage or a value of 100 short
// circuits and returns the original handler unchanged; any other value in
// [0, 99] makes the wrapper draw a random number per request and emit a 502
// when the draw falls outside the success window. The 502 body matches
// ProxyErrorHandler's.
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
// hostcall dispatches through, whether the backend exposes a transport
// fastlike can trace, and its between-bytes timeout.
func (i *Instance) resolveBackendHandler(name string) (http.Handler, bool, time.Duration) {
	if name == "geolocation" {
		return geoHandler(i.geolookup), false, 0
	}
	b := i.getBackend(name)
	if b == nil {
		return i.defaultBackend(name), false, 0
	}
	return b.Handler, b.Transport != nil, b.betweenBytesTimeout()
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
// TimeoutTransport applies the connect and first-byte timeouts.
func (b *Backend) CreateTransport() *http.Transport {
	// Connections outlive a connect timeout, so these limits are a backstop.
	connect := b.connectTimeout()
	dialer := &net.Dialer{
		Timeout:   max(30*time.Second, connect),
		KeepAlive: 30 * time.Second,
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
		TLSHandshakeTimeout:   max(10*time.Second, connect),
		ExpectContinueTimeout: 1 * time.Second,
		// Production never asks for compression the guest did not ask for.
		DisableCompression: true,
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

// Production's defaults for the timeouts a backend leaves unset.
const (
	defaultConnectTimeoutMs             = 1000
	dynamicDefaultFirstByteTimeoutMs    = 15000
	dynamicDefaultBetweenBytesTimeoutMs = 10000
)

var (
	errConnectTimeout   = errors.New("backend connect timeout")
	errFirstByteTimeout = errors.New("backend first-byte timeout")
)

func (b *Backend) connectTimeout() time.Duration {
	if b.ConnectTimeoutMs == 0 && !b.ConnectTimeoutSet {
		return defaultConnectTimeoutMs * time.Millisecond
	}
	return time.Duration(b.ConnectTimeoutMs) * time.Millisecond
}

func (b *Backend) firstByteTimeout() time.Duration {
	if b.firstByteOverrideMs != nil {
		return time.Duration(*b.firstByteOverrideMs) * time.Millisecond
	}
	return time.Duration(b.FirstByteTimeoutMs) * time.Millisecond
}

func (b *Backend) betweenBytesTimeout() time.Duration {
	if b.betweenBytesOverrideMs != nil {
		return time.Duration(*b.betweenBytesOverrideMs) * time.Millisecond
	}
	return time.Duration(b.BetweenBytesTimeoutMs) * time.Millisecond
}

// TimeoutTransport wraps transport with the backend's connect and first-byte
// timeouts and MaxConnections limit, applied the way production does.
// The connect timeout only covers a request's own new connection, and the
// first-byte timeout lasts until the headers arrive.
// MaxConnections limits the requests waiting for their headers.
func (b *Backend) TimeoutTransport(transport http.RoundTripper) http.RoundTripper {
	t := &timeoutTransport{
		transport: transport,
		connect:   b.connectTimeout(),
		firstByte: b.firstByteTimeout(),
	}
	if b.MaxConnections > 0 {
		t.permits = make(chan struct{}, b.MaxConnections)
	}
	return t
}

type timeoutTransport struct {
	transport http.RoundTripper
	connect   time.Duration
	firstByte time.Duration
	permits   chan struct{}
}

func (t *timeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.permits != nil {
		select {
		case t.permits <- struct{}{}:
			defer func() { <-t.permits }()
		case <-req.Context().Done():
			return nil, context.Cause(req.Context())
		}
	}

	// Cancelling leaves the dial running, and net/http pools its connection.
	ctx, cancel := context.WithCancelCause(req.Context())
	d := &roundTripDeadlines{connect: t.connect, firstByte: t.firstByte, cancel: cancel}
	trace := &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { d.dialing() },
		ConnectStart:      func(string, string) { d.dialing() },
		TLSHandshakeStart: d.dialing,
		GotConn:           func(httptrace.GotConnInfo) { d.connected() },
	}
	resp, err := t.transport.RoundTrip(req.WithContext(httptrace.WithClientTrace(ctx, trace)))
	if expired := d.finish(); expired != nil {
		if err == nil {
			_ = resp.Body.Close()
		}
		return nil, expired
	}
	if err != nil {
		cancel(err)
		return nil, err
	}
	// ReverseProxy needs the writable body of a protocol switch as it is.
	if _, ok := resp.Body.(io.Writer); !ok {
		resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	}
	return resp, nil
}

type roundTripPhase int

const (
	phaseWaiting roundTripPhase = iota
	phaseDialing
	phaseConnected
	phaseFinished
)

// roundTripDeadlines runs one round trip's timers, each for the phase it
// started in.
type roundTripDeadlines struct {
	connect   time.Duration
	firstByte time.Duration
	cancel    context.CancelCauseFunc

	mu      sync.Mutex
	phase   roundTripPhase
	timer   *time.Timer
	expired error
}

// dialing runs on net/http's dial goroutine, which can outlive the request.
func (d *roundTripDeadlines) dialing() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.phase == phaseWaiting {
		d.phase = phaseDialing
		d.startLocked(d.connect, errConnectTimeout)
	}
}

func (d *roundTripDeadlines) connected() {
	d.mu.Lock()
	defer d.mu.Unlock()
	// A retry on another connection keeps the first-byte timer.
	if d.phase > phaseDialing {
		return
	}
	d.phase = phaseConnected
	d.stopLocked()
	if d.firstByte > 0 {
		d.startLocked(d.firstByte, errFirstByteTimeout)
	}
}

func (d *roundTripDeadlines) startLocked(timeout time.Duration, err error) {
	// Production's zero connect timeout fails at once.
	if timeout <= 0 {
		d.expireLocked(err)
		return
	}
	phase := d.phase
	d.timer = time.AfterFunc(timeout, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		// Stop cannot recall a callback that already started.
		if d.phase == phase {
			d.expireLocked(err)
		}
	})
}

func (d *roundTripDeadlines) stopLocked() {
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}

func (d *roundTripDeadlines) expireLocked(err error) {
	d.expired = err
	d.phase = phaseFinished
	d.stopLocked()
	d.cancel(err)
}

// finish returns the timeout that cut the round trip, if any.
func (d *roundTripDeadlines) finish() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.phase = phaseFinished
	d.stopLocked()
	return d.expired
}

// cancelOnClose ends the round trip's context with its body.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelCauseFunc
}

func (b *cancelOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.cancel(nil)
	return err
}

// newTransportHandler builds the proxy handler for a fastlike-managed
// backend, recording the transport on the Backend so tracing and reset()
// cleanup can observe it.
func (b *Backend) newTransportHandler() http.Handler {
	transport := b.CreateTransport()
	b.Transport = transport
	roundTripper := b.TimeoutTransport(transport)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The connection goes to the registered target, while the Host header
		// follows viceroy's precedence: the host override, then the Host
		// header from the guest request, then the request URI's authority.
		// addBackend has applied any override before a request reaches here,
		// so only the last fallback is left.
		if r.Host == "" {
			r.Host = r.URL.Host
		}
		r.URL.Scheme = b.URL.Scheme
		r.URL.Host = b.URL.Host

		resp, err := roundTripper.RoundTrip(r)
		if err != nil {
			ProxyErrorHandler(w, r, err)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		if _, err := io.Copy(w, resp.Body); err != nil {
			captureBackendError(r.Context(), err)
			return
		}
		for k, v := range resp.Trailer {
			w.Header()[http.TrailerPrefix+k] = v
		}
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
	Healthcheck    int32  // Pointer to health-check configuration
}

// HealthcheckConfig represents the health-check structure referenced by a dynamic backend config.
type HealthcheckConfig struct {
	IntervalMs     uint64
	TimeoutMs      uint64
	Host           int32
	HostLen        uint32
	Method         int32
	MethodLen      uint32
	Path           int32
	PathLen        uint32
	ExpectedStatus uint32
	Window         uint32
	Threshold      uint32
	Initial        uint32
}

package fastlike

import (
	"bufio"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const timeoutGetterOut = 3000

func readBackendTimeouts(t *testing.T, i *Instance, name string) [3]uint32 {
	t.Helper()
	addr, size := writeStr(t, i, 2000, name)
	getters := []func(int32, int32, int32) int32{
		i.xqd_backend_get_connect_timeout_ms,
		i.xqd_backend_get_first_byte_timeout_ms,
		i.xqd_backend_get_between_bytes_timeout_ms,
	}
	var got [3]uint32
	for n, get := range getters {
		if status := get(addr, size, timeoutGetterOut); status != XqdStatusOK {
			t.Fatalf("timeout getter %d for %q: status = %d", n, name, status)
		}
		got[n] = i.memory.Uint32(timeoutGetterOut)
	}
	return got
}

func TestDynamicBackendTimeoutDefaults(t *testing.T) {
	i := newDynInstance()
	if status := registerDyn(t, i, "defaults", "origin.example.org", 0); status != XqdStatusOK {
		t.Fatalf("registration status = %d", status)
	}
	if got, want := readBackendTimeouts(t, i, "defaults"), [3]uint32{1000, 15000, 10000}; got != want {
		t.Errorf("timeouts = %v, want production's defaults %v", got, want)
	}
	addr, size := writeStr(t, i, 2000, "defaults")
	if status := i.xqd_backend_is_dynamic(addr, size, timeoutGetterOut); status != XqdStatusOK || i.memory.Uint32(timeoutGetterOut) != 1 {
		t.Errorf("is_dynamic = %d with status %d, want 1", i.memory.Uint32(timeoutGetterOut), status)
	}

	// Explicit values are taken as they are, zero included.
	allTimeouts := BackendConfigOptionsConnectTimeout | BackendConfigOptionsFirstByteTimeout | BackendConfigOptionsBetweenBytesTimeout
	for _, want := range [][3]uint32{{0, 0, 0}, {2500, 60000, 5}} {
		name := "explicit"
		i := newDynInstance()
		for n, v := range want {
			pokeCfgU32(i, cfgConnectTimeout+4*int64(n), v)
		}
		if status := registerDyn(t, i, name, "origin.example.org", allTimeouts); status != XqdStatusOK {
			t.Fatalf("registration status = %d", status)
		}
		if got := readBackendTimeouts(t, i, name); got != want {
			t.Errorf("timeouts = %v, want %v", got, want)
		}
	}
}

// A backend configured without timeouts gets production's static default.
func TestStaticBackendTimeoutGetters(t *testing.T) {
	i := newDynInstance()
	i.addBackend("static", &Backend{Handler: http.NotFoundHandler()})
	if got, want := readBackendTimeouts(t, i, "static"), [3]uint32{1000, 0, 0}; got != want {
		t.Errorf("timeouts = %v, want %v", got, want)
	}
}

// slowTLSOrigin is a TLS origin whose first TLS handshake completes after
// delay, and which never completes any later one.
type slowTLSOrigin struct {
	*httptest.Server
	accepts  atomic.Int32
	accepted chan struct{}
}

type slowListener struct {
	net.Listener
	origin *slowTLSOrigin
	delay  time.Duration
	closed chan struct{}
}

func (l *slowListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	var delay <-chan time.Time
	if l.origin.accepts.Add(1) == 1 {
		defer close(l.origin.accepted)
		delay = time.After(l.delay)
	}
	select {
	case <-delay:
		return conn, nil
	case <-l.closed:
		_ = conn.Close()
		return nil, net.ErrClosed
	}
}

func (l *slowListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return l.Listener.Close()
}

func newSlowTLSOrigin(t *testing.T, delay time.Duration) *slowTLSOrigin {
	t.Helper()
	origin := &slowTLSOrigin{accepted: make(chan struct{})}
	origin.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	origin.Listener = &slowListener{Listener: origin.Listener, origin: origin, delay: delay, closed: make(chan struct{})}
	origin.StartTLS()
	t.Cleanup(origin.Close)
	return origin
}

// backendConfig leaves the handler to the caller.
func (o *slowTLSOrigin) backendConfig() *Backend {
	roots := x509.NewCertPool()
	roots.AddCert(o.Certificate())
	u, _ := url.Parse(o.URL)
	return &Backend{URL: u, UseSSL: true, CACerts: roots}
}

func (o *slowTLSOrigin) backend(connectMs, firstByteMs uint32) *Backend {
	b := o.backendConfig()
	b.ConnectTimeoutMs, b.FirstByteTimeoutMs = connectMs, firstByteMs
	b.Handler = b.newTransportHandler()
	return b
}

// sendV2Timed returns the status, error detail tag and duration of a GET to
// the "origin" backend.
func sendV2Timed(t *testing.T, i *Instance, path string) (int32, uint32, time.Duration) {
	t.Helper()
	reqHandle, req := i.requests.New()
	req.URL, _ = url.Parse("http://origin.example.org" + path)
	i.memory.PutUint32(0xffffffff, streamDetailOut)
	start := time.Now()
	status := sendV2(i, int32(reqHandle))
	return status, i.memory.Uint32(streamDetailOut), time.Since(start)
}

func readAll(t *testing.T, i *Instance, handle int32) string {
	t.Helper()
	var all string
	for {
		status, chunk := readBody(t, i, handle, 4096)
		if status != XqdStatusOK {
			t.Fatalf("body_read status = %d after %q", status, all)
		}
		if chunk == "" {
			return all
		}
		all += chunk
	}
}

// Like in production, the late connection serves the next request.
func TestConnectTimeoutPoolsTheLateConnection(t *testing.T) {
	origin := newSlowTLSOrigin(t, 150*time.Millisecond)
	i := newOriginInstance(t, origin.backend(50, 0))

	status, tag, elapsed := sendV2Timed(t, i, "/")
	if status != XqdError || tag != SendErrorDetailConnectionTimeout {
		t.Fatalf("send = %d with detail %d, want %d with connection_timeout", status, tag, XqdError)
	}
	if elapsed >= 150*time.Millisecond {
		t.Errorf("send failed after %v, want the 50 ms connect timeout", elapsed)
	}

	<-origin.accepted
	time.Sleep(100 * time.Millisecond)
	status, tag, _ = sendV2Timed(t, i, "/")
	if status != XqdStatusOK {
		t.Fatalf("second send = %d with detail %d, want the pooled connection", status, tag)
	}
	if n := origin.accepts.Load(); n != 1 {
		t.Errorf("origin accepted %d connections, want 1", n)
	}
}

// Production times out right away on a dynamic backend registered with a
// zero connect timeout.
func TestExplicitZeroConnectTimeout(t *testing.T) {
	origin := newSlowTLSOrigin(t, time.Hour)
	i := newDynInstance()
	i.ds_context = t.Context()
	pokeCfgString(t, i, cfgCACertPtr, cfgCACertLen, dynStrAddr, serverCertPEM(origin.Server))
	pokeCfgU32(i, cfgConnectTimeout, 0)
	mask := BackendConfigOptionsUseSSL | BackendConfigOptionsCACert | BackendConfigOptionsConnectTimeout
	if status := registerDyn(t, i, "origin", origin.Listener.Addr().String(), mask); status != XqdStatusOK {
		t.Fatalf("registration status = %d", status)
	}
	writeStr(t, i, streamBackendAddr, "origin")

	status, tag, elapsed := sendV2Timed(t, i, "/")
	if status != XqdError || tag != SendErrorDetailConnectionTimeout {
		t.Fatalf("send = %d with detail %d, want %d with connection_timeout", status, tag, XqdError)
	}
	if elapsed > time.Second {
		t.Errorf("send failed after %v, want at once", elapsed)
	}
}

// Like production, which skips the connect timeout for an idle pooled
// connection, a zero timeout only fails requests that need a new one.
func TestZeroConnectTimeoutUsesIdleConnections(t *testing.T) {
	origin := newSlowTLSOrigin(t, 100*time.Millisecond)
	b := origin.backendConfig()
	b.ConnectTimeoutSet = true
	b.Handler = b.newTransportHandler()
	i := newOriginInstance(t, b)

	if status, tag, _ := sendV2Timed(t, i, "/"); status != XqdError || tag != SendErrorDetailConnectionTimeout {
		t.Fatalf("send = %d with detail %d, want %d with connection_timeout", status, tag, XqdError)
	}
	<-origin.accepted
	time.Sleep(100 * time.Millisecond)
	if status, tag, _ := sendV2Timed(t, i, "/"); status != XqdStatusOK {
		t.Fatalf("second send = %d with detail %d, want the pooled connection", status, tag)
	}
}

func timeoutRoundTripper(t *testing.T, b *Backend) http.RoundTripper {
	t.Helper()
	transport := b.CreateTransport()
	t.Cleanup(transport.CloseIdleConnections)
	return b.TimeoutTransport(transport)
}

func roundTrip(t *testing.T, rt http.RoundTripper, uri string) (*http.Response, time.Duration, error) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, uri, nil)
	start := time.Now()
	resp, err := rt.RoundTrip(req)
	return resp, time.Since(start), err
}

// Like production's permits, max_connections limits requests until their
// headers arrive, and waiting for one is not part of the connect timeout.
func TestMaxConnectionsLimitsRequestsUntilTheirHeaders(t *testing.T) {
	holding, release := make(chan struct{}), make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow-headers":
			close(holding)
			time.Sleep(300 * time.Millisecond)
		case "/slow-body":
			w.(http.Flusher).Flush()
			<-release
		}
		_, _ = io.WriteString(w, "done")
	}))
	defer origin.Close()
	defer close(release)
	rt := timeoutRoundTripper(t, &Backend{MaxConnections: 1, ConnectTimeoutMs: 100})

	first := make(chan error, 1)
	go func() {
		resp, _, err := roundTrip(t, rt, origin.URL+"/slow-headers")
		if err == nil {
			_ = resp.Body.Close()
		}
		first <- err
	}()
	<-holding
	resp, elapsed, err := roundTrip(t, rt, origin.URL+"/")
	if err != nil {
		t.Fatalf("request waiting for a permit failed after %v: %v", elapsed, err)
	}
	_ = resp.Body.Close()
	if elapsed < 100*time.Millisecond {
		t.Errorf("request took %v, want it to wait for the permit", elapsed)
	}
	if err := <-first; err != nil {
		t.Fatalf("first request: %v", err)
	}

	slow, _, err := roundTrip(t, rt, origin.URL+"/slow-body")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slow.Body.Close() }()
	resp, elapsed, err = roundTrip(t, rt, origin.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if elapsed >= 300*time.Millisecond {
		t.Errorf("request took %v, want the permit back once the other request had its headers", elapsed)
	}
}

// A connection still being set up after its request timed out does not hold
// on to the request's permit.
func TestTimedOutDialReleasesItsPermit(t *testing.T) {
	origin := newSlowTLSOrigin(t, time.Hour)
	b := origin.backendConfig()
	b.MaxConnections, b.ConnectTimeoutMs = 1, 100
	rt := timeoutRoundTripper(t, b)

	for n := range 2 {
		_, elapsed, err := roundTrip(t, rt, origin.URL)
		if !errors.Is(err, errConnectTimeout) || elapsed > time.Second {
			t.Errorf("request %d failed after %v with %v, want the 100 ms connect timeout", n, elapsed, err)
		}
	}
}

// The timers only cover the wait for the headers.
func TestTimeoutsStopOnceTheHeadersArrive(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.(http.Flusher).Flush()
		time.Sleep(500 * time.Millisecond)
		_, _ = io.WriteString(w, "done")
	}))
	defer origin.Close()
	target, _ := url.Parse(origin.URL)
	b := &Backend{URL: target, ConnectTimeoutMs: 100, FirstByteTimeoutMs: 400}
	b.Handler = b.newTransportHandler()
	i := newOriginInstance(t, b)

	status, tag, _ := sendV2Timed(t, i, "/")
	if status != XqdStatusOK {
		t.Fatalf("send = %d with detail %d, want headers after the connect timeout to be fine", status, tag)
	}
	if body := readAll(t, i, int32(i.memory.Uint32(streamBodyOut))); body != "done" {
		t.Errorf("body = %q, want the part sent after the first-byte timeout", body)
	}
}

// A ReverseProxy backend reports its errors like a transport does.
func TestProxyErrorHandlerFailsTheSend(t *testing.T) {
	release := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	defer origin.Close()
	defer close(release)
	target, _ := url.Parse(origin.URL)

	transport := (&Backend{}).CreateTransport()
	defer transport.CloseIdleConnections()
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = (&Backend{FirstByteTimeoutMs: 50}).TimeoutTransport(transport)
	proxy.ErrorHandler = ProxyErrorHandler
	i := newOriginInstance(t, &Backend{Handler: proxy, Transport: transport})

	if status, tag, _ := sendV2Timed(t, i, "/"); status != XqdError || tag != SendErrorDetailHttpResponseTimeout {
		t.Errorf("send = %d with detail %d, want %d with http_response_timeout", status, tag, XqdError)
	}
}

type stubRoundTripper func(*http.Request) (*http.Response, error)

func (f stubRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type writableBody struct{ io.ReadCloser }

func (writableBody) Write(p []byte) (int, error) { return len(p), nil }

// The round trip's context lives as long as the body, and a protocol switch
// keeps the writable body ReverseProxy needs.
func TestTimeoutTransportReleasesItsContext(t *testing.T) {
	var ctx context.Context
	body := io.NopCloser(strings.NewReader("body"))
	rt := (&Backend{}).TimeoutTransport(stubRoundTripper(func(r *http.Request) (*http.Response, error) {
		ctx = r.Context()
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	}))

	resp, err := rt.RoundTrip(httptest.NewRequest(http.MethodGet, "http://origin.example.org/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("the context ended before the body was closed")
	}
	_ = resp.Body.Close()
	if ctx.Err() == nil {
		t.Error("closing the body did not release the context")
	}

	body = writableBody{body}
	resp, err = rt.RoundTrip(httptest.NewRequest(http.MethodGet, "http://origin.example.org/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.Body.(io.Writer); !ok {
		t.Error("the body of a protocol switch lost its Write method")
	}
}

// Registering fills a Backend in, so concurrent instances must not share one.
func TestWithBackendConfigRegistersACopy(t *testing.T) {
	uptime := uint8(50)
	handler := http.NewServeMux()
	backend := &Backend{Name: "origin", Handler: handler, UptimePercent: &uptime}
	opt := WithBackendConfig(backend)

	instances := []*Instance{newDynInstance(), newDynInstance()}
	var wg sync.WaitGroup
	for _, i := range instances {
		wg.Add(1)
		go func() {
			defer wg.Done()
			opt(i)
		}()
	}
	wg.Wait()

	if instances[0].getBackend("origin") == instances[1].getBackend("origin") {
		t.Error("both instances registered the same Backend")
	}
	if backend.Handler != handler {
		t.Error("registration wrapped the configured handler")
	}
}

// The first-byte timeout starts once a connection is ready.
func TestFirstByteTimeoutExcludesConnecting(t *testing.T) {
	origin := newSlowTLSOrigin(t, 150*time.Millisecond)
	i := newOriginInstance(t, origin.backend(2000, 100))
	if status, tag, _ := sendV2Timed(t, i, "/"); status != XqdStatusOK {
		t.Fatalf("send = %d with detail %d, want success after a slow connect", status, tag)
	}
}

func TestFirstByteTimeout(t *testing.T) {
	release := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			<-release
		}
	}))
	defer origin.Close()
	defer close(release)
	target, _ := url.Parse(origin.URL)

	for _, tc := range []struct {
		firstByteMs uint32
		path        string
		wantStatus  int32
		wantTag     uint32
	}{
		{firstByteMs: 50, path: "/slow", wantStatus: XqdError, wantTag: SendErrorDetailHttpResponseTimeout},
		{firstByteMs: 5000, path: "/", wantStatus: XqdStatusOK},
	} {
		b := &Backend{URL: target, FirstByteTimeoutMs: tc.firstByteMs}
		b.Handler = b.newTransportHandler()
		i := newOriginInstance(t, b)
		if status, tag, _ := sendV2Timed(t, i, tc.path); status != tc.wantStatus || (status != XqdStatusOK && tag != tc.wantTag) {
			t.Errorf("first byte timeout %d ms, %s: send = %d with detail %d, want %d with %d", tc.firstByteMs, tc.path, status, tag, tc.wantStatus, tc.wantTag)
		}
	}
}

// Production's first-byte timeout covers sending the request, which Go's
// ResponseHeaderTimeout does not.
func TestFirstByteTimeoutCoversTheUpload(t *testing.T) {
	// A raw origin avoids net/http's delay before closing a connection
	// whose request body was not read to the end.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if req, err := http.ReadRequest(bufio.NewReader(conn)); err == nil {
			_, _ = io.Copy(io.Discard, req.Body)
			_, _ = io.WriteString(conn, "HTTP/1.1 204 No Content\r\n\r\n")
		}
	}()

	body, upload := io.Pipe()
	go func() {
		for range 6 {
			time.Sleep(50 * time.Millisecond)
			if _, err := upload.Write([]byte("chunk")); err != nil {
				return
			}
		}
		_ = upload.Close()
	}()
	defer func() { _ = body.Close() }()

	req, _ := http.NewRequest(http.MethodPost, "http://"+ln.Addr().String(), body)
	resp, err := timeoutRoundTripper(t, &Backend{FirstByteTimeoutMs: 100}).RoundTrip(req)
	if err == nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, errFirstByteTimeout) {
		t.Errorf("round trip error = %v, want the first-byte timeout", err)
	}
}

func TestBackendForShield(t *testing.T) {
	const (
		nameAddr    = 0
		configAddr  = 512
		nameOut     = 1024
		nameOutLen  = 1024
		nwrittenOut = 2048
	)
	i := newDynInstance()
	target := "https://shield.example.org"
	backendForShield := func(mask uint32, firstByteMs, betweenBytesMs uint32) (int32, string) {
		t.Helper()
		addr, size := writeStr(t, i, nameAddr, target)
		i.memory.PutUint32(firstByteMs, configAddr+8)
		i.memory.PutUint32(betweenBytesMs, configAddr+12)
		status := i.xqd_backend_for_shield(addr, size, int32(mask), configAddr, nameOut, nameOutLen, nwrittenOut)
		if status != XqdStatusOK {
			return status, ""
		}
		name := make([]byte, i.memory.Uint32(nwrittenOut))
		_, _ = i.memory.ReadAt(name, nameOut)
		return status, string(name)
	}

	_, name := backendForShield(0, 0, 0)
	if want := "**fastly-shield-https://shield.example.org-fbto15000-bbto60000**"; name != want {
		t.Errorf("default name = %q, want %q", name, want)
	}
	b := i.getBackend(name)
	if b == nil || b.firstByteTimeout() != 15*time.Second || b.betweenBytesTimeout() != 60*time.Second {
		t.Fatalf("default backend = %+v, want 15 s and 60 s", b)
	}

	// The reserved bit is accepted.
	mask := ShieldBackendOptionsReserved | ShieldBackendOptionsFirstByteTimeout | ShieldBackendOptionsBetweenBytesTimeout
	_, overridden := backendForShield(mask, 5000, 0)
	if want := "**fastly-shield-https://shield.example.org-fbto5000-bbto0**"; overridden != want {
		t.Errorf("overridden name = %q, want %q", overridden, want)
	}
	b = i.getBackend(overridden)
	if b == nil || b.firstByteTimeout() != 5*time.Second || b.betweenBytesTimeout() != 0 {
		t.Fatalf("overridden backend = %+v, want 5 s and none", b)
	}
	if i.getBackend(name) == nil {
		t.Error("the backend with other timeouts replaced the default one")
	}

	// The getters report the site's timeouts, not the guest's.
	for _, name := range []string{name, overridden} {
		if got, want := readBackendTimeouts(t, i, name), [3]uint32{2000, 15000, 60000}; got != want {
			t.Errorf("%s: timeouts = %v, want %v", name, got, want)
		}
	}

	addr, size := writeStr(t, i, 2000, name)
	if status := i.xqd_backend_is_dynamic(addr, size, timeoutGetterOut); status != XqdStatusOK || i.memory.Uint32(timeoutGetterOut) != 0 {
		t.Errorf("is_dynamic = %d with status %d, want 0 like production", i.memory.Uint32(timeoutGetterOut), status)
	}

	if status, _ := backendForShield(1<<4, 0, 0); status != XqdErrInvalidArgument {
		t.Errorf("unknown mask bit: status = %d, want %d", status, XqdErrInvalidArgument)
	}
	target = "\xff"
	if status, _ := backendForShield(0, 0, 0); status != XqdErrInvalidArgument {
		t.Errorf("invalid UTF-8 name: status = %d, want %d", status, XqdErrInvalidArgument)
	}
}

package fastlike

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"
	"time"
)

const (
	streamBackendAddr = 100
	streamRespOut     = 200
	streamBodyOut     = 204
	streamDetailOut   = 300
	streamReadBuf     = 4096
	streamReadOut     = 400
)

// newOriginInstance returns an instance whose "origin" backend is b.
func newOriginInstance(t *testing.T, b *Backend) *Instance {
	t.Helper()
	i := newDynInstance()
	i.ds_context = context.Background()
	i.addBackend("origin", b)
	writeStr(t, i, streamBackendAddr, "origin")
	return i
}

func newStreamTestInstance(t *testing.T, handler http.Handler) *Instance {
	t.Helper()
	return newOriginInstance(t, &Backend{Handler: handler})
}

func transportBackend(target *url.URL, betweenBytesMs uint32) *Backend {
	b := &Backend{URL: target, BetweenBytesTimeoutMs: betweenBytesMs}
	b.Handler = b.newTransportHandler()
	return b
}

// newRawOriginInstance proxies to a server that writes raw responses.
func newRawOriginInstance(t *testing.T, betweenBytesMs uint32, respond func(net.Conn)) *Instance {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
					return
				}
				respond(conn)
			}()
		}
	}()
	return newOriginInstance(t, transportBackend(&url.URL{Scheme: "http", Host: ln.Addr().String()}, betweenBytesMs))
}

// sendToOrigin fails unless send_v2 returns once the headers are in.
func sendToOrigin(t *testing.T, i *Instance) (int32, int32) {
	t.Helper()
	reqHandle, _ := i.requests.New()
	return sendRequestToOrigin(t, i, int32(reqHandle))
}

func sendRequestToOrigin(t *testing.T, i *Instance, reqHandle int32) (int32, int32) {
	t.Helper()
	status := make(chan int32, 1)
	go func() { status <- sendV2(i, reqHandle) }()
	select {
	case s := <-status:
		if s != XqdStatusOK {
			t.Fatalf("send_v2 status = %d, want %d", s, XqdStatusOK)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("send_v2 did not return once the headers were in")
	}
	return int32(i.memory.Uint32(streamRespOut)), int32(i.memory.Uint32(streamBodyOut))
}

func sendV2(i *Instance, reqHandle int32) int32 {
	bodyHandle, _ := i.bodies.NewBuffer()
	return i.xqd_req_send_v2(reqHandle, int32(bodyHandle), streamBackendAddr, int32(len("origin")), streamDetailOut, streamRespOut, streamBodyOut)
}

// sendAsyncToOrigin sends a GET with send_async and returns its pending
// handle.
func sendAsyncToOrigin(t *testing.T, i *Instance) int32 {
	t.Helper()
	reqHandle, _ := i.requests.New()
	bodyHandle, _ := i.bodies.NewBuffer()
	if status := i.xqd_req_send_async(int32(reqHandle), int32(bodyHandle), streamBackendAddr, int32(len("origin")), streamRespOut); status != XqdStatusOK {
		t.Fatalf("send_async status = %d", status)
	}
	return int32(i.memory.Uint32(streamRespOut))
}

func readBody(t *testing.T, i *Instance, handle int32, maxlen int32) (int32, string) {
	t.Helper()
	status := i.xqd_body_read(handle, streamReadBuf, maxlen, streamReadOut)
	if status != XqdStatusOK {
		return status, ""
	}
	n := i.memory.Uint32(streamReadOut)
	return status, string(i.memory.Data()[streamReadBuf : streamReadBuf+n])
}

func expectRead(t *testing.T, i *Instance, handle int32, maxlen int32, wantStatus int32, want string) {
	t.Helper()
	status, got := readBody(t, i, handle, maxlen)
	if status != wantStatus || got != want {
		t.Fatalf("body_read = (%d, %q), want (%d, %q)", status, got, wantStatus, want)
	}
}

func knownLength(i *Instance, handle int32) (int32, uint64) {
	status := i.xqd_body_known_length(handle, 500)
	return status, i.memory.Uint64(500)
}

func bodyIsReady(t *testing.T, i *Instance, handle int32) bool {
	t.Helper()
	if status := i.xqd_async_io_is_ready(handle, 900); status != XqdStatusOK {
		t.Fatalf("is_ready status = %d", status)
	}
	return i.memory.Uint32(900) == 1
}

func waitUntilReady(t *testing.T, i *Instance, handle int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !bodyIsReady(t, i, handle) {
		if time.Now().After(deadline) {
			t.Fatal("the body never became ready")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBackendSendReturnsBeforeTheBodyIsComplete(t *testing.T) {
	release := make(chan struct{})
	i := newStreamTestInstance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ab"))
		_, _ = w.Write([]byte("cd"))
		<-release
		_, _ = w.Write([]byte("ef"))
	}))
	defer close(release)

	_, body := sendToOrigin(t, i)
	// Each read returns a single chunk, even with room for more.
	expectRead(t, i, body, 1, XqdStatusOK, "a")
	expectRead(t, i, body, 64, XqdStatusOK, "b")
	expectRead(t, i, body, 64, XqdStatusOK, "cd")

	release <- struct{}{}
	expectRead(t, i, body, 64, XqdStatusOK, "ef")
	expectRead(t, i, body, 64, XqdStatusOK, "")
}

func TestAsyncBackendSendCompletesWithTheHeaders(t *testing.T) {
	release := make(chan struct{})
	i := newStreamTestInstance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		<-release
		_, _ = w.Write([]byte("late"))
	}))
	if status := i.xqd_pending_req_wait(sendAsyncToOrigin(t, i), streamRespOut, streamBodyOut); status != XqdStatusOK {
		t.Fatalf("pending_req_wait status = %d", status)
	}
	if got := i.responses.Get(int(i.memory.Uint32(streamRespOut))).StatusCode; got != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", got, http.StatusAccepted)
	}
	close(release)
	expectRead(t, i, int32(i.memory.Uint32(streamBodyOut)), 64, XqdStatusOK, "late")
}

func TestBackendBodyTruncatedByTheOrigin(t *testing.T) {
	for name, response := range map[string]string{
		"content-length": "HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nhello",
		"chunked":        "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			i := newRawOriginInstance(t, 0, func(conn net.Conn) {
				_, _ = io.WriteString(conn, response)
			})
			_, body := sendToOrigin(t, i)

			// Every byte that arrived reaches the guest before the error.
			expectRead(t, i, body, 64, XqdStatusOK, "hello")
			expectRead(t, i, body, 64, XqdErrHttpIncomplete, "")
			// Like an HTTP/1 body in production, it then just ends.
			expectRead(t, i, body, 64, XqdStatusOK, "")
		})
	}
}

func TestBackendBodyFailuresAfterTheHeaders(t *testing.T) {
	tests := map[string]http.HandlerFunc{
		"panic": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("hello"))
			panic(http.ErrAbortHandler)
		},
		"short content-length": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "10")
			_, _ = w.Write([]byte("hello"))
		},
	}
	for name, handler := range tests {
		t.Run(name, func(t *testing.T) {
			i := newStreamTestInstance(t, handler)
			_, body := sendToOrigin(t, i)
			expectRead(t, i, body, 64, XqdStatusOK, "hello")
			expectRead(t, i, body, 64, XqdErrHttpIncomplete, "")
		})
	}
}

func TestBackendHandlerPanicBeforeTheHeadersFailsTheSend(t *testing.T) {
	i := newStreamTestInstance(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	reqHandle, _ := i.requests.New()
	if status := sendV2(i, int32(reqHandle)); status != XqdError {
		t.Fatalf("send_v2 status = %d, want %d", status, XqdError)
	}
	if tag := i.memory.Uint32(streamDetailOut); tag != SendErrorDetailInternalError {
		t.Fatalf("error detail tag = %d, want %d", tag, SendErrorDetailInternalError)
	}
}

func TestBodyReadReturnsAnErrorThatCameWithDataNext(t *testing.T) {
	i := newDynInstance()
	failure := &backendBodyError{err: io.ErrUnexpectedEOF}
	reader := iotest.DataErrReader(io.MultiReader(strings.NewReader("abc"), iotest.ErrReader(failure)))
	handle, _ := i.bodies.NewReader(io.NopCloser(reader))
	expectRead(t, i, int32(handle), 64, XqdStatusOK, "abc")
	expectRead(t, i, int32(handle), 64, XqdErrHttpIncomplete, "")
}

func TestBodyReadStatus(t *testing.T) {
	tests := []struct {
		err  error
		want int32
	}{
		{&backendBodyError{err: io.ErrUnexpectedEOF}, XqdErrHttpIncomplete},
		{&backendBodyError{err: syscall.ECONNRESET}, XqdError},
		{errBetweenBytesTimeout, XqdError},
		// Cache bodies also end with io.ErrUnexpectedEOF when their write
		// failed, which production reports as a generic error.
		{io.ErrUnexpectedEOF, XqdError},
	}
	for _, tt := range tests {
		if got := bodyReadStatus(tt.err); got != tt.want {
			t.Errorf("bodyReadStatus(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}

func TestBetweenBytesTimeoutOnlyFailsReadsThatFindNothing(t *testing.T) {
	const timeout = 100 * time.Millisecond
	release := make(chan struct{})
	sent := make(chan struct{})
	i := newRawOriginInstance(t, uint32(timeout/time.Millisecond), func(conn net.Conn) {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nfirst\r\n")
		<-release
		_, _ = io.WriteString(conn, "6\r\nsecond\r\n")
		close(sent)
		<-release
		_, _ = io.WriteString(conn, "5\r\nthird\r\n0\r\n\r\n")
	})
	_, body := sendToOrigin(t, i)
	expectRead(t, i, body, 64, XqdStatusOK, "first")

	start := time.Now()
	status := make(chan int32, 1)
	go func() { status <- i.xqd_body_read(body, streamReadBuf, 64, streamReadOut) }()
	select {
	case s := <-status:
		if s != XqdError {
			t.Fatalf("body_read status = %d, want %d", s, XqdError)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read never timed out")
	}
	if elapsed := time.Since(start); elapsed < timeout*9/10 {
		t.Fatalf("the read timed out after %v, before the %v timeout", elapsed, timeout)
	}

	// Data that arrives after a timeout is still returned.
	release <- struct{}{}
	<-sent
	time.Sleep(20 * time.Millisecond)
	expectRead(t, i, body, 64, XqdStatusOK, "second")

	// A guest slower than the timeout does not time out when the data is
	// already there.
	release <- struct{}{}
	time.Sleep(2 * timeout)
	expectRead(t, i, body, 64, XqdStatusOK, "third")
	expectRead(t, i, body, 64, XqdStatusOK, "")
}

func TestBackendBodyKnownLength(t *testing.T) {
	compressed := gzipBytes([]byte("hello"))

	tests := []struct {
		name       string
		method     string
		decompress bool
		handler    http.HandlerFunc
		wantStatus int32
		wantLength uint64
		wantBody   string
		afterRead  uint64
		readMaxlen int32
	}{
		{
			name: "content-length", method: http.MethodGet,
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "5")
				_, _ = w.Write([]byte("hello"))
			},
			wantStatus: XqdStatusOK, wantLength: 5, readMaxlen: 2, wantBody: "he", afterRead: 3,
		},
		{
			name: "chunked", method: http.MethodGet,
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("hello"))
			},
			wantStatus: XqdErrNone,
		},
		{
			name: "head", method: http.MethodHead,
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "5")
				_, _ = w.Write([]byte("hello"))
			},
			wantStatus: XqdStatusOK, wantLength: 0, readMaxlen: 64, wantBody: "",
		},
		{
			name: "no content", method: http.MethodGet,
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			},
			wantStatus: XqdStatusOK, wantLength: 0,
		},
		{
			name: "decompressed", method: http.MethodGet, decompress: true,
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Set("Content-Length", strconv.Itoa(len(compressed)))
				_, _ = w.Write(compressed)
			},
			wantStatus: XqdErrNone, readMaxlen: 64, wantBody: "hello",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := newStreamTestInstance(t, tt.handler)
			reqHandle, req := i.requests.New()
			req.Method = tt.method
			if tt.decompress {
				req.autoDecompressEncodings = ContentEncodingsGzip
			}
			_, body := sendRequestToOrigin(t, i, int32(reqHandle))
			status, length := knownLength(i, body)
			if status != tt.wantStatus || (status == XqdStatusOK && length != tt.wantLength) {
				t.Fatalf("known_length = (%d, %d), want (%d, %d)", status, length, tt.wantStatus, tt.wantLength)
			}
			if tt.readMaxlen > 0 {
				expectRead(t, i, body, tt.readMaxlen, XqdStatusOK, tt.wantBody)
			}
			if tt.afterRead > 0 {
				if status, length := knownLength(i, body); status != XqdStatusOK || length != tt.afterRead {
					t.Fatalf("known_length after a read = (%d, %d), want (%d, %d)", status, length, XqdStatusOK, tt.afterRead)
				}
			}
		})
	}
}

func TestBackendBodyTrailersAfterTheEnd(t *testing.T) {
	i := newStreamTestInstance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Trailer", "X-Sum")
		_, _ = w.Write([]byte("data"))
		w.Header().Set("X-Sum", "42")
	}))
	_, body := sendToOrigin(t, i)
	nameAddr, nameSize := writeStr(t, i, 600, "X-Sum")
	if status := i.xqd_body_trailer_value_get(body, nameAddr, nameSize, 700, 16, 800); status != XqdErrAgain {
		t.Fatalf("trailer_value_get before the end = %d, want %d", status, XqdErrAgain)
	}
	expectRead(t, i, body, 64, XqdStatusOK, "data")
	expectRead(t, i, body, 64, XqdStatusOK, "")
	if status := i.xqd_body_trailer_value_get(body, nameAddr, nameSize, 700, 16, 800); status != XqdStatusOK {
		t.Fatalf("trailer_value_get = %d, want %d", status, XqdStatusOK)
	}
	if got := string(i.memory.Data()[700 : 700+i.memory.Uint32(800)]); got != "42" {
		t.Fatalf("trailer = %q, want %q", got, "42")
	}
}

func TestBackendBodyReadiness(t *testing.T) {
	release := make(chan struct{})
	i := newStreamTestInstance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		<-release
		_, _ = w.Write([]byte("now"))
	}))
	_, body := sendToOrigin(t, i)
	if bodyIsReady(t, i, body) {
		t.Fatal("a body with nothing to read is ready")
	}
	close(release)
	waitUntilReady(t, i, body)
	expectRead(t, i, body, 64, XqdStatusOK, "now")
}

func TestAppendedBackendBodyReadiness(t *testing.T) {
	release := make(chan struct{})
	i := newStreamTestInstance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		<-release
		_, _ = w.Write([]byte("backend"))
	}))
	_, backend := sendToOrigin(t, i)
	head, headBody := i.bodies.NewBuffer()
	_, _ = headBody.Write([]byte("head,"))
	if status := i.xqd_body_append(int32(head), backend); status != XqdStatusOK {
		t.Fatalf("body_append status = %d", status)
	}

	if !bodyIsReady(t, i, int32(head)) {
		t.Fatal("a body with buffered data is not ready")
	}
	expectRead(t, i, int32(head), 64, XqdStatusOK, "head,")
	// The next read would wait for the backend.
	if bodyIsReady(t, i, int32(head)) {
		t.Fatal("the body is ready while its backend part has nothing")
	}
	close(release)
	waitUntilReady(t, i, int32(head))
	expectRead(t, i, int32(head), 64, XqdStatusOK, "backend")
}

// Like production, readiness moves past parts that already ended.
func TestReadinessSkipsEndedParts(t *testing.T) {
	release := make(chan struct{})
	i := newStreamTestInstance(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stalled" {
			w.WriteHeader(http.StatusOK)
			<-release
		}
		_, _ = w.Write([]byte(r.URL.Path))
	}))
	_, first := sendToOrigin(t, i)
	firstPart := i.bodies.Get(int(first)).reader.(*backendBody)
	stalledReq, req := i.requests.New()
	req.URL.Path = "/stalled"
	_, second := sendRequestToOrigin(t, i, int32(stalledReq))
	if status := i.xqd_body_append(first, second); status != XqdStatusOK {
		t.Fatalf("body_append status = %d", status)
	}
	expectRead(t, i, first, 64, XqdStatusOK, "/")
	for deadline := time.Now().Add(5 * time.Second); !firstPart.ended(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the first part never ended")
		}
	}
	if bodyIsReady(t, i, first) {
		t.Fatal("the body is ready while its next part has nothing")
	}
	close(release)
	waitUntilReady(t, i, first)
	expectRead(t, i, first, 64, XqdStatusOK, "/stalled")
}

func TestAutoDecompressionAsksForGzip(t *testing.T) {
	accepted := make(chan []string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		accepted <- r.Header.Values("Accept-Encoding")
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)

	i := newOriginInstance(t, transportBackend(target, 0))

	// Unlike Go's client, production never asks for compression on its own.
	sendToOrigin(t, i)
	if got := <-accepted; len(got) != 0 {
		t.Fatalf("Accept-Encoding without auto-decompression = %q, want none", got)
	}

	reqHandle, req := i.requests.New()
	req.Header.Set("Accept-Encoding", "br")
	req.autoDecompressEncodings = ContentEncodingsGzip
	sendRequestToOrigin(t, i, int32(reqHandle))
	if got := <-accepted; len(got) != 1 || got[0] != "gzip" {
		t.Fatalf("Accept-Encoding with auto-decompression = %q, want gzip", got)
	}
}

func TestSendDownstreamCutsATruncatedBodyShort(t *testing.T) {
	i := newStreamTestInstance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "10")
		_, _ = w.Write([]byte("hello"))
	}))
	downstream := httptest.NewRecorder()
	i.ds_response = downstream
	resp, body := sendToOrigin(t, i)
	// The SDK's send_to_client panics on an error.
	if status := i.xqd_resp_send_downstream(resp, body, 0); status != XqdStatusOK {
		t.Fatalf("send_downstream status = %d, want %d", status, XqdStatusOK)
	}
	if !i.finishDownstream() {
		t.Fatal("the truncated body was not cut short")
	}
	if got := downstream.Body.String(); got != "hello" {
		t.Fatalf("downstream body = %q, want %q", got, "hello")
	}
}

func TestClosingABackendBodyStopsTheHandler(t *testing.T) {
	stopped := make(chan error, 1)
	i := newStreamTestInstance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := make([]byte, 4096)
		for {
			if _, err := w.Write(chunk); err != nil {
				stopped <- err
				return
			}
		}
	}))
	_, body := sendToOrigin(t, i)
	if status, _ := readBody(t, i, body, 64); status != XqdStatusOK {
		t.Fatalf("body_read status = %d", status)
	}
	closeBody(t, i, body)
	select {
	case err := <-stopped:
		if !errors.Is(err, errBackendBodyClosed) {
			t.Fatalf("handler write error = %v, want %v", err, errBackendBodyClosed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler kept writing a closed body")
	}
}

func TestResetClosesUncollectedBackendResponses(t *testing.T) {
	stopped := make(chan struct{})
	i := newStreamTestInstance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		defer close(stopped)
		chunk := make([]byte, 4096)
		for {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	sendAsyncToOrigin(t, i)
	i.reset()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler of an uncollected response kept running")
	}
}

func TestStreamingSendDownstreamKeepsTheBodyContent(t *testing.T) {
	i := newStreamTestInstance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("from the backend, "))
	}))
	downstream := httptest.NewRecorder()
	i.ds_response = downstream
	resp, body := sendToOrigin(t, i)
	if status := i.xqd_resp_send_downstream(resp, body, 1); status != XqdStatusOK {
		t.Fatalf("send_downstream status = %d, want %d", status, XqdStatusOK)
	}
	dataAddr, dataSize := writeStr(t, i, 600, "then from the guest")
	if status := i.xqd_body_write(body, dataAddr, dataSize, BodyWriteEndBack, 700); status != XqdStatusOK {
		t.Fatalf("body_write status = %d", status)
	}
	closeBody(t, i, body)
	if i.finishDownstream() {
		t.Fatal("the finished body was cut short")
	}
	if got, want := downstream.Body.String(), "from the backend, then from the guest"; got != want {
		t.Fatalf("downstream body = %q, want %q", got, want)
	}
}

// A body handed over, for example to a cache insert, keeps streaming after
// the guest returns, even when its response handle was left open.
func TestResetKeepsBackendBodiesThatWereHandedOver(t *testing.T) {
	release := make(chan struct{})
	i := newStreamTestInstance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("first,"))
		<-release
		_, _ = w.Write([]byte("second"))
	}))
	_, body := sendToOrigin(t, i)
	handedOver := i.bodies.Take(int(body))
	i.reset()
	close(release)
	got, err := readWithin(handedOver)
	if err != nil || got != "first,second" {
		t.Fatalf("handed-over body = (%q, %v), want %q", got, err, "first,second")
	}
}

func TestLargeBackendWritesWaitForTheGuest(t *testing.T) {
	large := bytes.Repeat([]byte("0123456789abcdef"), 64*1024)
	i := newStreamTestInstance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(large)
	}))
	_, handle := sendToOrigin(t, i)
	body := i.bodies.Get(int(handle)).reader.(*backendBody)
	time.Sleep(50 * time.Millisecond)
	body.mu.Lock()
	buffered := body.buffered
	body.mu.Unlock()
	if buffered > 2*backendBodyBuffer {
		t.Fatalf("%d bytes wait for the guest, want at most %d", buffered, 2*backendBodyBuffer)
	}
	got, err := readWithin(i.bodies.Get(int(handle)))
	if err != nil || got != string(large) {
		t.Fatalf("body = %d bytes, %v, want the %d bytes written", len(got), err, len(large))
	}
}

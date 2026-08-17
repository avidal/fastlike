package fastlike

import (
	"bufio"
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// The request from the Viceroy report, which mixes casing, ends with a repeated name and puts Host where the client put it.
const issueRequest = "GET / HTTP/1.1\r\n" +
	"Host: example.com\r\n" +
	"User-Agent: curl/8.0\r\n" +
	"Accept: */*\r\n" +
	"Zeta-Header: z\r\n" +
	"X-MiXeD-CaSe: 1\r\n" +
	"Alpha-Thing: a\r\n" +
	"X-Dup: one\r\n" +
	"X-Dup: two\r\n\r\n"

var issueHeaderNames = []string{
	"Host", "User-Agent", "Accept", "Zeta-Header", "X-MiXeD-CaSe", "Alpha-Thing", "X-Dup", "X-Dup",
}

// startCaptureServer serves requests with the capture installed.
// It reports the address to dial and a channel carrying the names each handler call saw.
func startCaptureServer(t *testing.T, depth int) (string, <-chan []string) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	names := make(chan []string, depth)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		names <- claimOriginalHeaderNames(r)
		w.WriteHeader(http.StatusNoContent)
	})}
	go func() { _ = srv.Serve(CaptureOriginalHeaders(srv, listener)) }()
	t.Cleanup(func() { _ = srv.Close() })

	return listener.Addr().String(), names
}

func wantHeaderNames(t *testing.T, got, want [][]string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("captured %d requests, want %d", len(got), len(want))
	}
	for n := range want {
		if !slices.Equal(got[n], want[n]) {
			t.Fatalf("request %d names = %v, want %v", n, got[n], want[n])
		}
	}
}

func readResponse(t *testing.T, responses *bufio.Reader) {
	t.Helper()

	response, err := http.ReadResponse(responses, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
}

// captureNames serves requests over a real connection and returns the names each handler call saw.
// Every request waits for the previous response, the way a client without pipelining behaves.
func captureNames(t *testing.T, requests ...string) [][]string {
	t.Helper()

	addr, names := startCaptureServer(t, len(requests))
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	got := make([][]string, 0, len(requests))
	responses := bufio.NewReader(conn)
	for _, request := range requests {
		if _, err := io.WriteString(conn, request); err != nil {
			t.Fatalf("write request: %v", err)
		}
		readResponse(t, responses)
		got = append(got, <-names)
	}
	return got
}

func TestCapturedHeaderNamesKeepTheirWireForm(t *testing.T) {
	got := captureNames(t, issueRequest)
	if !slices.Equal(got[0], issueHeaderNames) {
		t.Fatalf("names = %v, want %v", got[0], issueHeaderNames)
	}
}

func TestCapturedHeaderNamesSurviveKeepAlive(t *testing.T) {
	second := "POST /upload HTTP/1.1\r\nhost: example.com\r\nContent-Length: 5\r\nX-Second: y\r\n\r\nhello"
	third := "GET /last HTTP/1.1\r\nHOST: example.com\r\nX-Third: z\r\n\r\n"

	wantHeaderNames(t, captureNames(t, issueRequest, second, third), [][]string{
		issueHeaderNames,
		{"host", "Content-Length", "X-Second"},
		{"HOST", "X-Third"},
	})
}

func TestCapturedHeaderNamesSurviveChunkedBodies(t *testing.T) {
	chunked := "POST /stream HTTP/1.1\r\nHost: example.com\r\nTransfer-Encoding: chunked\r\nTrailer: X-Sum\r\n\r\n" +
		"5;ext=1\r\nhello\r\n" +
		"3\r\n s!\r\n" +
		"0\r\nX-Sum: 9\r\n\r\n"
	next := "GET /after HTTP/1.1\r\nHost: example.com\r\nX-After: 1\r\n\r\n"

	wantHeaderNames(t, captureNames(t, chunked, next), [][]string{
		{"Host", "Transfer-Encoding", "Trailer"},
		{"Host", "X-After"},
	})
}

// Go's parser eats the headers it acts on.
// The names still have to come back, or the match against the parsed request rejects a good capture.
func TestCapturedHeaderNamesSurviveHeadersGoConsumes(t *testing.T) {
	request := "POST /form HTTP/1.1\r\nHost: example.com\r\n" +
		"Transfer-Encoding: chunked\r\nTrailer: X-Sum\r\nConnection: close\r\n\r\n" +
		"4\r\nbody\r\n0\r\nX-Sum: 4\r\n\r\n"

	got := captureNames(t, request)
	want := []string{"Host", "Transfer-Encoding", "Trailer", "Connection"}
	if !slices.Equal(got[0], want) {
		t.Fatalf("names = %v, want %v", got[0], want)
	}
}

// A pipelining client puts several requests on the wire before reading any response.
// The capture then reads a head while the previous request is still being handled, and each handler still has to see its own names.
func TestCapturedHeaderNamesSurvivePipelining(t *testing.T) {
	second := "GET /second HTTP/1.1\r\nHost: example.com\r\nX-SeCoNd: 2\r\n\r\n"

	addr, names := startCaptureServer(t, 2)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := io.WriteString(conn, issueRequest+second); err != nil {
		t.Fatalf("write requests: %v", err)
	}

	responses := bufio.NewReader(conn)
	want := [][]string{issueHeaderNames, {"Host", "X-SeCoNd"}}
	for n := range want {
		readResponse(t, responses)
		if got := <-names; !slices.Equal(got, want[n]) {
			t.Fatalf("request %d names = %v, want %v", n, got, want[n])
		}
	}
}

// Go answers "OPTIONS *" itself unless told not to.
// A request that never reaches the handler is a request whose names nobody claims.
func TestCapturedHeaderNamesSurviveGeneralOptions(t *testing.T) {
	options := "OPTIONS * HTTP/1.1\r\nHost: example.com\r\nX-OpTiOnS: 1\r\n\r\n"

	wantHeaderNames(t, captureNames(t, options, issueRequest), [][]string{
		{"Host", "X-OpTiOnS"}, issueHeaderNames,
	})
}

// feedStream runs a byte stream through a fresh capture, one slice at a time.
func feedStream(chunks ...string) *headerCapture {
	capture := &headerCapture{}
	for _, chunk := range chunks {
		capture.feed([]byte(chunk))
	}
	return capture
}

func drain(capture *headerCapture) [][]string {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.pending
}

func TestCaptureHandlesReadsSplitAnywhere(t *testing.T) {
	stream := issueRequest + "POST /p HTTP/1.1\r\nHost: h\r\nContent-Length: 4\r\n\r\nbody" + issueRequest

	byteAtATime := &headerCapture{}
	for n := range len(stream) {
		byteAtATime.feed([]byte(stream[n : n+1]))
	}

	want := [][]string{issueHeaderNames, {"Host", "Content-Length"}, issueHeaderNames}
	for _, capture := range []*headerCapture{feedStream(stream), byteAtATime} {
		wantHeaderNames(t, drain(capture), want)
	}
}

func TestCaptureStopsOnStreamsItCannotFollow(t *testing.T) {
	tests := []struct {
		name   string
		stream string
		want   [][]string
	}{
		{
			name:   "not http at all",
			stream: "\x16\x03\x01\x02\x00\x01\x00\x01\xfc\r\n\r\nciphertext",
		},
		{
			name:   "http/0.9",
			stream: "GET /\r\n\r\n",
		},
		{
			name:   "framing we do not understand",
			stream: "POST / HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: gzip, chunked\r\n\r\n",
		},
		{
			name:   "chunked before http/1.1",
			stream: "POST / HTTP/1.0\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n",
		},
		{
			name:   "two content lengths",
			stream: "POST / HTTP/1.1\r\nHost: h\r\nContent-Length: 1\r\nContent-Length: 2\r\n\r\nxx",
		},
		{
			name:   "connect tunnel",
			stream: "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com\r\n\r\n\x16\x03\x01",
			want:   [][]string{{"Host"}},
		},
		{
			name:   "protocol upgrade",
			stream: "GET /ws HTTP/1.1\r\nHost: h\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\nframes",
			want:   [][]string{{"Host", "Upgrade", "Connection"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := feedStream(tt.stream, issueRequest)
			wantHeaderNames(t, drain(capture), tt.want)
			if capture.state != captureStopped {
				t.Fatalf("capture state = %d, want %d", capture.state, captureStopped)
			}
		})
	}
}

func TestCaptureStopsOnOversizedHead(t *testing.T) {
	head := "GET / HTTP/1.1\r\nHost: h\r\nX-Big: " + strings.Repeat("x", maxCapturedHeadBytes)
	capture := feedStream(head)
	if capture.state != captureStopped {
		t.Fatalf("capture state = %d, want %d", capture.state, captureStopped)
	}
	if names := drain(capture); names != nil {
		t.Fatalf("names = %v, want none", names)
	}
}

func TestCaptureStopsWhenNothingClaimsRequests(t *testing.T) {
	capture := feedStream(strings.Repeat(issueRequest, maxCapturedHeads+1))
	if capture.state != captureStopped {
		t.Fatalf("capture state = %d, want %d", capture.state, captureStopped)
	}
	if got := len(drain(capture)); got != maxCapturedHeads {
		t.Fatalf("captured %d requests, want %d", got, maxCapturedHeads)
	}
}

// Go accepts a Content-Length repeated with the same value, then collapses it to one.
// The scanner has to follow the body all the same, or the rest of the connection loses its capture.
func TestCaptureFollowsRepeatedContentLength(t *testing.T) {
	repeated := "POST / HTTP/1.1\r\nHost: example.com\r\nContent-Length: 2\r\nContent-Length: 2\r\n\r\nhi"
	capture := feedStream(repeated + issueRequest)

	wantHeaderNames(t, drain(capture), [][]string{
		{"Host", "Content-Length", "Content-Length"}, issueHeaderNames,
	})

	// The parsed request only kept one of the two, so the claim refuses names it cannot confirm.
	if names := capture.claimOriginalHeaderNames(parseRequest(t, repeated)); names != nil {
		t.Fatalf("names = %v, want none", names)
	}
}

func parseRequest(t *testing.T, raw string) *http.Request {
	t.Helper()
	r, err := http.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	return r
}

func TestReconstructedHeaderNamesCountEveryOccurrence(t *testing.T) {
	got := originalHeaderNamesFromRequest(parseRequest(t, issueRequest))
	want := []string{"Host", "Accept", "Alpha-Thing", "User-Agent", "X-Dup", "X-Dup", "X-Mixed-Case", "Zeta-Header"}
	if !slices.Equal(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

func TestReconstructedHeaderNamesRestoreConsumedHeaders(t *testing.T) {
	raw := "POST / HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\nTrailer: X-Sum\r\nConnection: close\r\n\r\n0\r\nX-Sum: 9\r\n\r\n"
	got := originalHeaderNamesFromRequest(parseRequest(t, raw))
	want := []string{"Host", "Connection", "Trailer", "Transfer-Encoding"}
	if !slices.Equal(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

func TestReconstructedHeaderNamesLowercaseForHTTP2(t *testing.T) {
	r := &http.Request{
		Method:     http.MethodGet,
		URL:        &url.URL{Path: "/"},
		Host:       "example.com",
		Header:     http.Header{"X-Mixed-Case": {"1"}, "Accept": {"*/*"}},
		Proto:      "HTTP/2.0",
		ProtoMajor: 2,
	}
	got := originalHeaderNamesFromRequest(r)
	want := []string{"host", "accept", "x-mixed-case"}
	if !slices.Equal(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

func TestWithOriginalHeaderNamesReachesTheInstance(t *testing.T) {
	r := WithOriginalHeaderNames(parseRequest(t, issueRequest), issueHeaderNames)
	got := claimOriginalHeaderNames(r)
	if !slices.Equal(got, issueHeaderNames) {
		t.Fatalf("names = %v, want %v", got, issueHeaderNames)
	}
}

// A request nobody claimed, because middleware answered it or the loop guard rejected it, must not shift the names behind it.
func TestClaimSkipsNamesThatCannotBelongToTheRequest(t *testing.T) {
	second := "GET /second HTTP/1.1\r\nHost: example.com\r\nX-SeCoNd: 2\r\n\r\n"
	capture := feedStream(issueRequest + second)

	got := capture.claimOriginalHeaderNames(parseRequest(t, second))
	want := []string{"Host", "X-SeCoNd"}
	if !slices.Equal(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	if left := drain(capture); len(left) != 0 {
		t.Fatalf("%d entries left behind, want none", len(left))
	}
}

func TestClaimRefusesNamesFromAnotherRequest(t *testing.T) {
	capture := feedStream(issueRequest)
	other := "GET /other HTTP/1.1\r\nHost: example.com\r\nX-Other: 1\r\n\r\n"

	if got := capture.claimOriginalHeaderNames(parseRequest(t, other)); got != nil {
		t.Fatalf("names = %v, want none", got)
	}
}

func newOriginalHeadersTestInstance() *Instance {
	return &Instance{
		requests:   &RequestHandles{},
		responses:  &ResponseHandles{},
		bodies:     &BodyHandles{},
		memory:     &Memory{ByteMemory(make([]byte, 2048))},
		abilog:     log.New(io.Discard, "", 0),
		ds_context: context.Background(),
		secureFn:   func(*http.Request) bool { return false },
	}
}

// readHeaderNames drives the cursor protocol to completion, the way a guest does.
func readHeaderNames(t *testing.T, i *Instance, handle int32, maxlen int32) []string {
	t.Helper()

	const (
		addr            = 512
		endingCursorOut = 256
		nwrittenOut     = 264
	)
	var names []string
	for cursor := int32(0); cursor >= 0; {
		if status := i.xqd_http_downstream_original_header_names(handle, addr, maxlen, cursor, endingCursorOut, nwrittenOut); status != XqdStatusOK {
			t.Fatalf("original_header_names status = %d, want %d", status, XqdStatusOK)
		}
		written := i.memory.Uint32(nwrittenOut)
		if written > 0 {
			buffer := string(i.memory.Data()[addr : addr+int64(written)])
			names = append(names, strings.Split(strings.TrimSuffix(buffer, "\x00"), "\x00")...)
		}
		cursor = int32(int64(i.memory.Uint64(endingCursorOut)))
	}
	return names
}

func TestDownstreamOriginalHeaderHostcalls(t *testing.T) {
	// A buffer that fits two names at a time proves the cursor advances.
	for _, maxlen := range []int32{16, 256} {
		i := newOriginalHeadersTestInstance()
		i.ds_request = parseRequest(t, issueRequest)
		i.ds_originalHeaders = issueHeaderNames

		if status := i.xqd_req_body_downstream_get(100, 104); status != XqdStatusOK {
			t.Fatalf("body_downstream_get status = %d, want %d", status, XqdStatusOK)
		}
		handle := int32(i.memory.Uint32(100))

		if status := i.xqd_http_downstream_original_header_count(handle, 108); status != XqdStatusOK {
			t.Fatalf("original_header_count status = %d, want %d", status, XqdStatusOK)
		}
		if got := i.memory.Uint32(108); got != uint32(len(issueHeaderNames)) {
			t.Fatalf("header count = %d, want %d", got, len(issueHeaderNames))
		}

		if got := readHeaderNames(t, i, handle, maxlen); !slices.Equal(got, issueHeaderNames) {
			t.Fatalf("names with maxlen %d = %v, want %v", maxlen, got, issueHeaderNames)
		}
	}
}

func TestDownstreamOriginalHeaderNamesFallBackWithoutCapture(t *testing.T) {
	i := newOriginalHeadersTestInstance()
	i.ds_request = parseRequest(t, issueRequest)

	if status := i.xqd_req_body_downstream_get(100, 104); status != XqdStatusOK {
		t.Fatalf("body_downstream_get status = %d, want %d", status, XqdStatusOK)
	}
	handle := int32(i.memory.Uint32(100))

	want := originalHeaderNamesFromRequest(i.ds_request)
	if got := readHeaderNames(t, i, handle, 256); !slices.Equal(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

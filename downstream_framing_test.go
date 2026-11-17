package fastlike

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newFramingTestInstance() *Instance {
	i := newCacheReplaceTestInstance()
	i.responses = &ResponseHandles{}
	i.pendingRequests = &PendingRequestHandles{}
	return i
}

// framingSeen is how a real client received a response, and whether Fastlike
// cut it short.
type framingSeen struct {
	contentLength int64
	chunked       bool
	body          string
	readErr       error
	cutShort      bool
}

// serveFraming has guest send i's response to a real client, and reports how
// it was framed.
// guest runs on the server, so it must report failures with t.Errorf.
func serveFraming(t *testing.T, h2 bool, method string, i *Instance, guest func()) framingSeen {
	t.Helper()
	cutShort := make(chan bool, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i.ds_request = r
		i.ds_response = w
		guest()
		cut := i.finishDownstream()
		cutShort <- cut
		if cut {
			panic(http.ErrAbortHandler)
		}
	}))
	server.EnableHTTP2 = h2
	server.StartTLS()
	t.Cleanup(server.Close)

	req, _ := http.NewRequest(method, server.URL, nil)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if h2 != (resp.ProtoMajor == 2) {
		t.Fatalf("got %s", resp.Proto)
	}
	body, err := io.ReadAll(resp.Body)
	seen := framingSeen{contentLength: resp.ContentLength, body: string(body), readErr: err}
	seen.chunked = len(resp.TransferEncoding) == 1 && resp.TransferEncoding[0] == "chunked"
	select {
	case seen.cutShort = <-cutShort:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never finished")
	}
	return seen
}

// sendTestResponse sends body with automatic framing, or with manual framing
// when it is given headers.
func sendTestResponse(t *testing.T, i *Instance, headers http.Header, body int, stream int32) {
	t.Helper()
	rh, resp := i.responses.New()
	if headers != nil {
		resp.Header = headers
		resp.framingHeadersMode = FramingHeadersModeManuallyFromHeaders
	}
	if status := i.xqd_resp_send_downstream(int32(rh), int32(body), stream); status != XqdStatusOK {
		t.Errorf("resp_send_downstream status = %d", status)
	}
}

func newTestBuffer(t *testing.T, i *Instance, data string) int {
	t.Helper()
	id, body := i.bodies.NewBuffer()
	if _, err := body.Write([]byte(data)); err != nil {
		t.Error(err)
	}
	return id
}

// writeTestBody writes data to an end of body, as a guest does.
func writeTestBody(t *testing.T, i *Instance, body int, data string, end int32) {
	t.Helper()
	const addr, nwrittenOut = 2048, 2040
	if _, err := i.memory.WriteAt([]byte(data), addr); err != nil {
		t.Error(err)
	}
	if status := i.xqd_body_write(int32(body), addr, int32(len(data)), end, nwrittenOut); status != XqdStatusOK {
		t.Errorf("body_write status = %d", status)
	}
}

func newTestBackendBody(i *Instance, data string, length int64) int {
	id, _ := i.bodies.NewResponseReader(&http.Response{Body: io.NopCloser(strings.NewReader(data)), ContentLength: length})
	return id
}

// With automatic framing, production sends a Content-Length when it knows the
// body's length and chunks otherwise, whatever the size.
// It hangs up on a body that ends before its Content-Length.
func TestResponsesAreFramedLikeProduction(t *testing.T) {
	big := strings.Repeat("x", 5000)
	tenBytes := http.Header{"Content-Length": {"10"}}
	tests := []struct {
		name   string
		method string
		guest  func(t *testing.T, i *Instance)
		// length is the Content-Length the client sees, or -1 for none.
		length   int64
		wantBody string
		cutShort bool
		// net/http adds a length by itself to a short HTTP/2 response that is
		// not flushed before its body.
		alsoHTTP2 bool
	}{
		{
			name: "a backend body with bytes written after it",
			guest: func(t *testing.T, i *Instance) {
				id := newTestBackendBody(i, big, 5000)
				writeTestBody(t, i, id, "end", BodyWriteEndBack)
				sendTestResponse(t, i, nil, id, 0)
			},
			length: 5003, wantBody: big + "end",
		},
		{
			name: "a short body of unknown length",
			guest: func(t *testing.T, i *Instance) {
				sendTestResponse(t, i, nil, newTestBackendBody(i, "tiny", -1), 0)
			},
			length: -1, wantBody: "tiny", alsoHTTP2: true,
		},
		{
			name: "a buffer with a body of unknown length appended",
			guest: func(t *testing.T, i *Instance) {
				dst := newTestBuffer(t, i, big)
				if status := i.xqd_body_append(int32(dst), int32(newTestBackendBody(i, "tail", -1))); status != XqdStatusOK {
					t.Errorf("body_append status = %d", status)
				}
				sendTestResponse(t, i, nil, dst, 0)
			},
			length: -1, wantBody: big + "tail",
		},
		{
			name:   "a response to HEAD",
			method: http.MethodHead,
			guest: func(t *testing.T, i *Instance) {
				sendTestResponse(t, i, nil, newTestBuffer(t, i, big), 0)
			},
			length: -1,
		},
		{
			name: "a pending response with a length",
			guest: func(t *testing.T, i *Instance) {
				phid, pr := i.pendingRequests.New()
				pr.Complete(&http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(big)), ContentLength: 5000}, nil)
				if status := i.xqd_resp_send_downstream_pending(int32(phid)); status != XqdStatusOK {
					t.Errorf("send_downstream_pending status = %d", status)
				}
			},
			length: 5000, wantBody: big,
		},
		{
			name: "a buffer shorter than its manual Content-Length",
			guest: func(t *testing.T, i *Instance) {
				sendTestResponse(t, i, tenBytes, newTestBuffer(t, i, "abc"), 0)
			},
			length: 10, cutShort: true,
		},
		{
			name: "a stream shorter than its manual Content-Length",
			guest: func(t *testing.T, i *Instance) {
				body := newTestBuffer(t, i, "abc")
				sendTestResponse(t, i, tenBytes, body, 1)
				if status := i.xqd_body_close(int32(body)); status != XqdStatusOK {
					t.Errorf("body_close status = %d", status)
				}
			},
			length: 10, cutShort: true,
		},
		{
			name: "a stream as long as its manual Content-Length",
			guest: func(t *testing.T, i *Instance) {
				body := newTestBuffer(t, i, "0123456789")
				sendTestResponse(t, i, tenBytes, body, 1)
				if status := i.xqd_body_close(int32(body)); status != XqdStatusOK {
					t.Errorf("body_close status = %d", status)
				}
			},
			length: 10, wantBody: "0123456789",
		},
		{
			// Production then falls back to automatic framing.
			name: "manual framing with both a Content-Length and a Transfer-Encoding",
			guest: func(t *testing.T, i *Instance) {
				headers := http.Header{"Content-Length": {"10"}, "Transfer-Encoding": {"chunked"}}
				sendTestResponse(t, i, headers, newTestBuffer(t, i, "abc"), 0)
			},
			length: 3, wantBody: "abc",
		},
	}
	for _, tt := range tests {
		method := tt.method
		if method == "" {
			method = http.MethodGet
		}
		check := func(h2 bool) func(t *testing.T) {
			return func(t *testing.T) {
				i := newFramingTestInstance()
				got := serveFraming(t, h2, method, i, func() { tt.guest(t, i) })
				if got.contentLength != tt.length {
					t.Errorf("Content-Length %d, want %d", got.contentLength, tt.length)
				}
				if (got.readErr != nil) != tt.cutShort || got.cutShort != tt.cutShort {
					t.Errorf("read error %v, cut short %v, want cut short %v", got.readErr, got.cutShort, tt.cutShort)
				}
				if tt.cutShort {
					return
				}
				if wantChunked := tt.length < 0 && method != http.MethodHead && !h2; got.chunked != wantChunked {
					t.Errorf("chunked %v, want %v", got.chunked, wantChunked)
				}
				if got.body != tt.wantBody {
					t.Errorf("body of %d bytes, want %d", len(got.body), len(tt.wantBody))
				}
			}
		}
		t.Run(tt.name, check(false))
		if tt.alsoHTTP2 {
			t.Run(tt.name+" over HTTP/2", check(true))
		}
	}
}

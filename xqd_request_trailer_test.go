package fastlike

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func newRequestTrailerTestInstance() *Instance {
	return &Instance{
		requests:        &RequestHandles{},
		responses:       &ResponseHandles{},
		bodies:          &BodyHandles{},
		pendingRequests: &PendingRequestHandles{},
		backends:        map[string]*Backend{},
		memory:          &Memory{ByteMemory(make([]byte, 2048))},
		abilog:          log.New(io.Discard, "", 0),
		ds_context:      context.Background(),
	}
}

func TestRequestSendPathsForwardBodyTrailers(t *testing.T) {
	for _, mode := range []string{"sync", "async", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			i := newRequestTrailerTestInstance()
			gotTrailer := make(chan []string, 1)
			i.addBackend("origin", &Backend{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				gotTrailer <- slices.Clone(r.Trailer.Values("X-Body-Trailer"))
				w.WriteHeader(http.StatusNoContent)
			})})

			reqHandle, _ := i.requests.New()
			bodyHandle, body := i.bodies.NewBuffer()
			body.trailers = http.Header{"X-Body-Trailer": {"first", "second"}}
			backendAddr, backendSize := writeStr(t, i, 100, "origin")

			switch mode {
			case "sync":
				if status := i.xqd_req_send(int32(reqHandle), int32(bodyHandle), backendAddr, backendSize, 300, 304); status != XqdStatusOK {
					t.Fatalf("req_send status = %d, want %d", status, XqdStatusOK)
				}
			case "async":
				if status := i.xqd_req_send_async(int32(reqHandle), int32(bodyHandle), backendAddr, backendSize, 300); status != XqdStatusOK {
					t.Fatalf("req_send_async status = %d, want %d", status, XqdStatusOK)
				}
				pending := i.pendingRequests.Get(int(i.memory.Uint32(300)))
				select {
				case <-pending.done:
				case <-time.After(time.Second):
					t.Fatal("async request did not complete")
				}
			case "streaming":
				if status := i.xqd_req_send_async_streaming(int32(reqHandle), int32(bodyHandle), backendAddr, backendSize, 300); status != XqdStatusOK {
					t.Fatalf("req_send_async_streaming status = %d, want %d", status, XqdStatusOK)
				}
				if status := i.xqd_body_close(int32(bodyHandle)); status != XqdStatusOK {
					t.Fatalf("body_close status = %d, want %d", status, XqdStatusOK)
				}
				pending := i.pendingRequests.Get(int(i.memory.Uint32(300)))
				select {
				case <-pending.done:
				case <-time.After(time.Second):
					t.Fatal("streaming request did not complete")
				}
			}

			select {
			case got := <-gotTrailer:
				want := []string{"first", "second"}
				if !slices.Equal(got, want) {
					t.Fatalf("backend trailer values = %q, want %q", got, want)
				}
			case <-time.After(time.Second):
				t.Fatal("backend was not called")
			}
		})
	}
}

func TestReqSendForwardsConfiguredHTTPVersion(t *testing.T) {
	i := newRequestTrailerTestInstance()
	gotVersion := make(chan [2]int, 1)
	i.addBackend("origin", &Backend{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotVersion <- [2]int{r.ProtoMajor, r.ProtoMinor}
		w.WriteHeader(http.StatusNoContent)
	})})

	reqHandle, req := i.requests.New()
	req.version = Http2
	bodyHandle, _ := i.bodies.NewBuffer()
	backendAddr, backendSize := writeStr(t, i, 100, "origin")
	if status := i.xqd_req_send(int32(reqHandle), int32(bodyHandle), backendAddr, backendSize, 300, 304); status != XqdStatusOK {
		t.Fatalf("req_send status = %d, want %d", status, XqdStatusOK)
	}
	if got := <-gotVersion; got != [2]int{2, 0} {
		t.Fatalf("backend HTTP version = %d.%d, want 2.0", got[0], got[1])
	}
}

func TestBackendResponseTrailersReachBodyHandle(t *testing.T) {
	i := newRequestTrailerTestInstance()
	i.addBackend("origin", &Backend{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Trailer", "X-Origin-Trailer")
		_, _ = io.WriteString(w, "body")
		w.Header().Set("X-Origin-Trailer", "after-body")
	})})

	reqHandle, _ := i.requests.New()
	bodyHandle, _ := i.bodies.NewBuffer()
	backendAddr, backendSize := writeStr(t, i, 100, "origin")
	if status := i.xqd_req_send(int32(reqHandle), int32(bodyHandle), backendAddr, backendSize, 300, 304); status != XqdStatusOK {
		t.Fatalf("req_send status = %d, want %d", status, XqdStatusOK)
	}
	responseBodyHandle := int32(i.memory.Uint32(304))

	if status := i.xqd_body_read(responseBodyHandle, 400, 16, 500); status != XqdStatusOK {
		t.Fatalf("first body_read status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_body_read(responseBodyHandle, 400, 16, 500); status != XqdStatusOK {
		t.Fatalf("EOF body_read status = %d, want %d", status, XqdStatusOK)
	}
	nameAddr, nameSize := writeStr(t, i, 600, "X-Origin-Trailer")
	if status := i.xqd_body_trailer_value_get(responseBodyHandle, nameAddr, nameSize, 700, 32, 800); status != XqdStatusOK {
		t.Fatalf("body_trailer_value_get status = %d, want %d", status, XqdStatusOK)
	}
	if got := string(i.memory.Data()[700 : 700+i.memory.Uint32(800)]); got != "after-body" {
		t.Fatalf("response trailer value = %q, want %q", got, "after-body")
	}
}

func TestDownstreamRequestTrailersReachBodyHandle(t *testing.T) {
	i := newRequestTrailerTestInstance()
	i.secureFn = func(*http.Request) bool { return false }
	i.ds_request = &http.Request{
		Method:     http.MethodPost,
		URL:        &url.URL{Path: "/"},
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
		Trailer:    http.Header{"X-Downstream-Trailer": {"downstream-value"}},
		Proto:      "HTTP/2.0",
		ProtoMajor: 2,
	}

	if status := i.xqd_req_body_downstream_get(100, 104); status != XqdStatusOK {
		t.Fatalf("body_downstream_get status = %d, want %d", status, XqdStatusOK)
	}
	bodyHandle := int32(i.memory.Uint32(104))
	requestHandle := int32(i.memory.Uint32(100))
	if status := i.xqd_req_version_get(requestHandle, 108); status != XqdStatusOK {
		t.Fatalf("request version_get status = %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint32(108); got != uint32(Http2) {
		t.Fatalf("downstream request version = %d, want %d", got, Http2)
	}
	if status := i.xqd_body_read(bodyHandle, 200, 16, 300); status != XqdStatusOK {
		t.Fatalf("body_read status = %d, want %d", status, XqdStatusOK)
	}
	nameAddr, nameSize := writeStr(t, i, 400, "X-Downstream-Trailer")
	if status := i.xqd_body_trailer_value_get(bodyHandle, nameAddr, nameSize, 500, 32, 600); status != XqdStatusOK {
		t.Fatalf("body_trailer_value_get status = %d, want %d", status, XqdStatusOK)
	}
	if got := string(i.memory.Data()[500 : 500+i.memory.Uint32(600)]); got != "downstream-value" {
		t.Fatalf("downstream trailer value = %q, want %q", got, "downstream-value")
	}
}

func TestResponseSendDownstreamForwardsBodyTrailers(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered", true: "streaming"}[streaming], func(t *testing.T) {
			i := newRequestTrailerTestInstance()
			recorder := httptest.NewRecorder()
			i.ds_response = recorder
			respHandle, _ := i.responses.New()
			bodyHandle, body := i.bodies.NewBuffer()

			if !streaming {
				body.trailers = http.Header{"X-Downstream-Body-Trailer": {"trailer-value"}}
			}
			stream := int32(0)
			if streaming {
				stream = 1
			}
			if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), stream); status != XqdStatusOK {
				t.Fatalf("resp_send_downstream status = %d, want %d", status, XqdStatusOK)
			}
			if streaming {
				nameAddr, nameSize := writeStr(t, i, 100, "X-Downstream-Body-Trailer")
				valueAddr, valueSize := writeStr(t, i, 200, "trailer-value")
				if status := i.xqd_body_trailer_append(int32(bodyHandle), nameAddr, nameSize, valueAddr, valueSize); status != XqdStatusOK {
					t.Fatalf("body_trailer_append status = %d, want %d", status, XqdStatusOK)
				}
				if status := i.xqd_body_close(int32(bodyHandle)); status != XqdStatusOK {
					t.Fatalf("body_close status = %d, want %d", status, XqdStatusOK)
				}
			}

			if got := recorder.Result().Trailer.Values("X-Downstream-Body-Trailer"); !slices.Equal(got, []string{"trailer-value"}) {
				t.Fatalf("downstream response trailer = %q, want [trailer-value]", got)
			}
		})
	}
}

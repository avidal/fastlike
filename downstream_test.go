package fastlike

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

var sendModes = []struct {
	name   string
	stream int32
}{{"buffered", 0}, {"streaming", 1}}

// within fails the test unless f returns within five seconds.
func within[T any](t *testing.T, what string, f func() T) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- f() }()
	select {
	case v := <-done:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("%s is still waiting", what)
		return *new(T)
	}
}

// hookedWriter is a recorder whose head or body writes a test can replace.
type hookedWriter struct {
	*httptest.ResponseRecorder
	writeHeader func(int)
	write       func([]byte) (int, error)
}

func (w hookedWriter) WriteHeader(status int) {
	if w.writeHeader != nil {
		w.writeHeader(status)
		return
	}
	w.ResponseRecorder.WriteHeader(status)
}

func (w hookedWriter) Write(p []byte) (int, error) {
	if w.write != nil {
		return w.write(p)
	}
	return w.ResponseRecorder.Write(p)
}

// serveDownstream runs guest like ServeHTTP does, for a real client.
func serveDownstream(t *testing.T, guest func(i *Instance)) *http.Response {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i := newPendingTestInstance()
		i.ds_response = w
		guest(i)
		if i.finishDownstream() {
			panic(http.ErrAbortHandler)
		}
	}))
	t.Cleanup(server.Close)
	resp, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func writeBody(t *testing.T, i *Instance, handle int, data string) {
	t.Helper()
	addr, size := writeStr(t, i, 1000, data)
	if status := i.xqd_body_write(int32(handle), addr, size, BodyWriteEndBack, 900); status != XqdStatusOK {
		t.Fatalf("body_write status = %d", status)
	}
}

func TestSendDownstreamPendingReturnsAtOnce(t *testing.T) {
	i := newPendingTestInstance()
	recorder := httptest.NewRecorder()
	i.ds_response = recorder
	phid, pr := i.pendingRequests.New()

	if status := within(t, "send_downstream_pending", func() int32 { return i.xqd_resp_send_downstream_pending(int32(phid)) }); status != XqdStatusOK {
		t.Fatalf("send_downstream_pending status = %d", status)
	}
	pr.Complete(&http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader("late"))}, nil)
	if i.finishDownstream() {
		t.Fatal("the response was cut short")
	}
	if recorder.Code != http.StatusAccepted || recorder.Body.String() != "late" {
		t.Fatalf("downstream = %d %q, want %d %q", recorder.Code, recorder.Body.String(), http.StatusAccepted, "late")
	}
}

func TestSendDownstreamReturnsBeforeTheBody(t *testing.T) {
	for _, mode := range sendModes {
		t.Run(mode.name, func(t *testing.T) {
			i := newPendingTestInstance()
			recorder := httptest.NewRecorder()
			i.ds_response = recorder
			respHandle, _ := i.responses.New()
			source, feed := io.Pipe()
			bodyHandle, _ := i.bodies.NewReader(source)

			if status := within(t, "send_downstream", func() int32 { return i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), mode.stream) }); status != XqdStatusOK {
				t.Fatalf("send_downstream status = %d", status)
			}
			want := "from the body"
			_, _ = feed.Write([]byte(want))
			_ = feed.Close()
			if mode.stream == 1 {
				writeBody(t, i, bodyHandle, ", then from the guest")
				want += ", then from the guest"
				closeBody(t, i, int32(bodyHandle))
			}
			if i.finishDownstream() {
				t.Fatal("the response was cut short")
			}
			if got := recorder.Body.String(); got != want {
				t.Fatalf("downstream body = %q, want %q", got, want)
			}
		})
	}
}

func TestStreamedResponseReachesTheClientAsItIsWritten(t *testing.T) {
	for _, tc := range []struct{ name, held string }{{"empty body", ""}, {"body with content", "held,"}} {
		held := tc.held
		t.Run(tc.name, func(t *testing.T) {
			// The guest waits for the client to get each part before going on.
			progress := make(chan string, 3)
			await := func(want string) {
				select {
				case got := <-progress:
					if got != want {
						t.Errorf("client progress = %q, want %q", got, want)
					}
				case <-time.After(5 * time.Second):
					t.Errorf("the client never got %q", want)
				}
			}
			resp := serveDownstream(t, func(i *Instance) {
				respHandle, _ := i.responses.New()
				bodyHandle, body := i.bodies.NewBuffer()
				_, _ = body.Write([]byte(held))
				if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), 1); status != XqdStatusOK {
					t.Errorf("send_downstream status = %d", status)
				}
				await("head")
				if held != "" {
					await(held)
				}
				writeBody(t, i, bodyHandle, "written,")
				await("written,")
				writeBody(t, i, bodyHandle, "last")
				closeBody(t, i, int32(bodyHandle))
			})

			progress <- "head"
			for _, part := range []string{held, "written,"} {
				if part == "" {
					continue
				}
				got := make([]byte, len(part))
				if _, err := io.ReadFull(resp.Body, got); err != nil || string(got) != part {
					t.Fatalf("client read %q, %v, want %q", got, err, part)
				}
				progress <- part
			}
			if rest, err := io.ReadAll(resp.Body); err != nil || string(rest) != "last" {
				t.Fatalf("rest of the body = %q, %v, want %q", rest, err, "last")
			}
		})
	}
}

func TestUnfinishedStreamIsCutShort(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(i *Instance, handle int)
	}{
		{"guest exits", func(*Instance, int) {}},
		{"guest abandons", func(i *Instance, handle int) {
			if status := i.xqd_body_abandon(int32(handle)); status != XqdStatusOK {
				panic("body_abandon failed")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := serveDownstream(t, func(i *Instance) {
				respHandle, _ := i.responses.New()
				bodyHandle, _ := i.bodies.NewBuffer()
				if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), 1); status != XqdStatusOK {
					t.Errorf("send_downstream status = %d", status)
				}
				writeBody(t, i, bodyHandle, "partial")
				tc.end(i, bodyHandle)
			})
			body, err := io.ReadAll(resp.Body)
			if string(body) != "partial" || !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("client read %q, %v, want %q and an unexpected EOF", body, err, "partial")
			}
		})
	}
}

func TestStreamedResponseWaitsForFreeSlots(t *testing.T) {
	i := newPendingTestInstance()
	recorder := httptest.NewRecorder()
	release := make(chan struct{})
	i.ds_response = hookedWriter{ResponseRecorder: recorder, write: func(p []byte) (int, error) {
		<-release
		return recorder.Write(p)
	}}
	respHandle, _ := i.responses.New()
	bodyHandle, _ := i.bodies.NewBuffer()
	if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), 1); status != XqdStatusOK {
		t.Fatalf("send_downstream status = %d", status)
	}
	stream := i.bodies.Get(bodyHandle).sink.(*downstreamStream)

	// The first write is being sent, and the next ones fill every slot.
	writeBody(t, i, bodyHandle, "x")
	for deadline := time.Now().Add(time.Second); ; time.Sleep(time.Millisecond) {
		stream.mu.Lock()
		taken := len(stream.queue) == 0
		stream.mu.Unlock()
		if taken {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first write was never sent")
		}
	}
	for range downstreamStreamSlots {
		writeBody(t, i, bodyHandle, "x")
	}
	if bodyIsReady(t, i, int32(bodyHandle)) {
		t.Fatal("a full stream is ready for writes")
	}
	written := make(chan struct{})
	go func() {
		writeBody(t, i, bodyHandle, "y")
		close(written)
	}()
	select {
	case <-written:
		t.Fatal("a write went past a full stream")
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("the write never got a free slot")
	}
	closeBody(t, i, int32(bodyHandle))
	if i.finishDownstream() {
		t.Fatal("the response was cut short")
	}
	if got, want := recorder.Body.String(), strings.Repeat("x", downstreamStreamSlots+1)+"y"; got != want {
		t.Fatalf("downstream body = %q, want %q", got, want)
	}
}

func TestStreamedWritesFailOnceTheClientIsGone(t *testing.T) {
	i := newPendingTestInstance()
	i.ds_response = hookedWriter{ResponseRecorder: httptest.NewRecorder(), write: func([]byte) (int, error) {
		return 0, errors.New("client gone")
	}}
	respHandle, _ := i.responses.New()
	bodyHandle, _ := i.bodies.NewBuffer()
	if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), 1); status != XqdStatusOK {
		t.Fatalf("send_downstream status = %d", status)
	}
	writesFailWithin(t, i, bodyHandle)

	// Like production, the source is taken, and the append fails.
	srcHandle, _ := i.bodies.NewBuffer()
	if status := i.xqd_body_append(int32(bodyHandle), int32(srcHandle)); status != XqdErrInvalidHandle {
		t.Fatalf("body_append status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if i.bodies.Get(srcHandle) != nil {
		t.Fatal("the failed append kept its source")
	}
	if !i.finishDownstream() {
		t.Fatal("the response was not cut short")
	}
}

func TestStreamedWritesTakeAtMostOneChunk(t *testing.T) {
	i := newPendingTestInstance()
	i.memory = &Memory{ByteMemory(make([]byte, 64*1024))}
	recorder := httptest.NewRecorder()
	i.ds_response = recorder
	respHandle, _ := i.responses.New()
	bodyHandle, _ := i.bodies.NewBuffer()
	if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), 1); status != XqdStatusOK {
		t.Fatalf("send_downstream status = %d", status)
	}
	large := strings.Repeat("0123456789abcdef", 1024)
	addr, size := writeStr(t, i, 1000, large)
	if status := i.xqd_body_write(int32(bodyHandle), addr, size, BodyWriteEndBack, 900); status != XqdStatusOK {
		t.Fatalf("body_write status = %d", status)
	}
	if got := i.memory.Uint32(900); got != downstreamStreamChunkMax {
		t.Fatalf("body_write took %d bytes, want %d", got, downstreamStreamChunkMax)
	}
	closeBody(t, i, int32(bodyHandle))
	i.finishDownstream()
	if got := recorder.Body.String(); got != large[:downstreamStreamChunkMax] {
		t.Fatalf("downstream body has %d bytes, want %d", len(got), downstreamStreamChunkMax)
	}
}

func TestWriterPanicClosesTheBodyItWasSending(t *testing.T) {
	for _, mode := range sendModes {
		t.Run(mode.name, func(t *testing.T) {
			i := newPendingTestInstance()
			i.ds_response = hookedWriter{ResponseRecorder: httptest.NewRecorder(), write: func([]byte) (int, error) {
				panic("response writer bug")
			}}
			respHandle, _ := i.responses.New()
			source := &bodyLengthReadCloser{Reader: strings.NewReader("body")}
			bodyHandle, _ := i.bodies.NewReader(source)
			if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), mode.stream); status != XqdStatusOK {
				t.Fatalf("send_downstream status = %d", status)
			}
			if !i.finishDownstream() {
				t.Fatal("the response was not cut short")
			}
			if source.closed == 0 {
				t.Fatal("the body being sent was left open")
			}
		})
	}
}

func TestOnlyOneFinalResponse(t *testing.T) {
	i := newPendingTestInstance()
	i.ds_response = httptest.NewRecorder()
	respHandle, _ := i.responses.New()
	bodyHandle, _ := i.bodies.NewBuffer()
	if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), 0); status != XqdStatusOK {
		t.Fatalf("send_downstream status = %d", status)
	}

	// Like production, the second response fails before taking any handle.
	respHandle, _ = i.responses.New()
	bodyHandle, _ = i.bodies.NewBuffer()
	if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), 0); status != XqdError {
		t.Fatalf("second send_downstream status = %d, want %d", status, XqdError)
	}
	if i.responses.Get(respHandle) == nil || i.bodies.Get(bodyHandle) == nil {
		t.Fatal("the second send took its handles")
	}

	phid, pr := i.pendingRequests.New()
	cancelled := false
	pr.cancel = func() { cancelled = true }
	func() {
		defer func() {
			if _, trapped := recover().(guestTrap); !trapped {
				t.Fatal("a pending response after the response did not trap")
			}
		}()
		i.xqd_resp_send_downstream_pending(int32(phid))
	}()
	if !cancelled {
		t.Fatal("the pending request that could not be sent kept going")
	}
	i.finishDownstream()
}

func TestEarlyHintsLikeProduction(t *testing.T) {
	const link = "</style.css>; rel=preload; as=style"
	for _, tc := range []struct {
		name      string
		http2     bool
		noHints   bool
		link      string
		wantHints int
	}{
		{"HTTP/2", true, false, link, 1},
		{"HTTP/1.1", false, false, link, 0},
		{"no-early-hints", true, true, link, 0},
		{"without headers", true, false, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				i := newPendingTestInstance()
				i.ds_request, i.ds_response = r, w
				hintHandle, hint := i.responses.New()
				hint.StatusCode = http.StatusEarlyHints
				if tc.link != "" {
					hint.Header = http.Header{"Link": {tc.link}}
				}
				hintBody, body := i.bodies.NewBuffer()
				_, _ = body.Write([]byte("not sent"))
				if status := i.xqd_resp_send_downstream(int32(hintHandle), int32(hintBody), 0); status != XqdStatusOK {
					t.Errorf("103 send_downstream status = %d", status)
				}
				finalHandle, _ := i.responses.New()
				finalBody, body := i.bodies.NewBuffer()
				_, _ = body.Write([]byte("final"))
				if status := i.xqd_resp_send_downstream(int32(finalHandle), int32(finalBody), 0); status != XqdStatusOK {
					t.Errorf("final send_downstream status = %d", status)
				}
				i.finishDownstream()
			}))
			server.EnableHTTP2 = tc.http2
			server.StartTLS()
			defer server.Close()

			var hints []textproto.MIMEHeader
			trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
				if code == http.StatusEarlyHints {
					hints = append(hints, header)
				}
				return nil
			}}
			req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(t.Context(), trace), "GET", server.URL, nil)
			if tc.noHints {
				req.Header.Set("No-Early-Hints", "1")
			}
			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if tc.http2 != (resp.ProtoMajor == 2) {
				t.Fatalf("client spoke %s", resp.Proto)
			}
			if len(hints) != tc.wantHints {
				t.Fatalf("got %d early hints, want %d", len(hints), tc.wantHints)
			}
			if len(hints) == 1 && hints[0].Get("Link") != link {
				t.Fatalf("early hints Link = %q, want %q", hints[0].Get("Link"), link)
			}
			if resp.StatusCode != http.StatusOK || string(body) != "final" {
				t.Fatalf("final response = %d %q, want 200 %q", resp.StatusCode, body, "final")
			}
			if got := resp.Header.Get("Link"); got != "" {
				t.Fatalf("final response has the early hints' Link %q", got)
			}
		})
	}
}

// Like production, the handle stays open but takes nothing.
func TestStreamedEarlyHintsBodyIsDropped(t *testing.T) {
	i := newPendingTestInstance()
	i.ds_request = httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	recorder := httptest.NewRecorder()
	i.ds_response = recorder
	respHandle, resp := i.responses.New()
	resp.StatusCode = http.StatusEarlyHints
	bodyHandle, _ := i.bodies.NewBuffer()
	if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), 1); status != XqdStatusOK {
		t.Fatalf("103 send_downstream status = %d", status)
	}
	writesFailWithin(t, i, bodyHandle)
	srcHandle, _ := i.bodies.NewBuffer()
	if status := i.xqd_body_append(int32(bodyHandle), int32(srcHandle)); status != XqdErrInvalidHandle {
		t.Fatalf("body_append status = %d, want %d", status, XqdErrInvalidHandle)
	}
	closeBody(t, i, int32(bodyHandle))

	respHandle, _ = i.responses.New()
	bodyHandle, body := i.bodies.NewBuffer()
	_, _ = body.Write([]byte("final"))
	if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), 0); status != XqdStatusOK {
		t.Fatalf("final send_downstream status = %d", status)
	}
	if i.finishDownstream() {
		t.Fatal("the response was cut short")
	}
	if recorder.Code != http.StatusOK || recorder.Body.String() != "final" {
		t.Fatalf("downstream = %d %q, want 200 %q", recorder.Code, recorder.Body.String(), "final")
	}
}

// writesFailWithin expects the guest's writes to fail with Badf soon.
func writesFailWithin(t *testing.T, i *Instance, handle int) {
	t.Helper()
	addr, size := writeStr(t, i, 1000, "x")
	for attempt := 0; ; attempt++ {
		status := within(t, "body_write", func() int32 { return i.xqd_body_write(int32(handle), addr, size, BodyWriteEndBack, 900) })
		if status == XqdErrInvalidHandle {
			return
		}
		if status != XqdStatusOK || attempt > 4*downstreamStreamSlots {
			t.Fatalf("body_write status = %d after %d writes, want Badf", status, attempt)
		}
	}
}

func TestResponsesWithoutABodyDropIt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		status int
	}{
		{"204", http.MethodGet, http.StatusNoContent},
		{"304", http.MethodGet, http.StatusNotModified},
		{"HEAD", http.MethodHead, http.StatusOK},
	} {
		for _, mode := range []string{"buffered", "streaming", "pending"} {
			t.Run(tc.name+" "+mode, func(t *testing.T) {
				i := newPendingTestInstance()
				i.ds_request = httptest.NewRequest(tc.method, "http://example.com/", nil)
				recorder := httptest.NewRecorder()
				i.ds_response = recorder
				source := &bodyLengthReadCloser{Reader: strings.NewReader("dropped")}
				if mode == "pending" {
					phid, pr := i.pendingRequests.New()
					pr.Complete(&http.Response{StatusCode: tc.status, Body: source}, nil)
					if status := i.xqd_resp_send_downstream_pending(int32(phid)); status != XqdStatusOK {
						t.Fatalf("send_downstream_pending status = %d", status)
					}
				} else {
					respHandle, resp := i.responses.New()
					resp.StatusCode = tc.status
					bodyHandle, _ := i.bodies.NewReader(source)
					stream := map[string]int32{"buffered": 0, "streaming": 1}[mode]
					if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), stream); status != XqdStatusOK {
						t.Fatalf("send_downstream status = %d", status)
					}
					if stream == 1 {
						// Production drops the body, so the guest's writes fail.
						writesFailWithin(t, i, bodyHandle)
					}
				}
				if i.finishDownstream() {
					t.Fatal("the response was cut short")
				}
				if recorder.Code != tc.status || recorder.Body.Len() != 0 {
					t.Fatalf("downstream = %d %q, want %d without a body", recorder.Code, recorder.Body.String(), tc.status)
				}
				if source.Reader.(*strings.Reader).Len() != len("dropped") || source.closed == 0 {
					t.Fatal("the body was read, or left open")
				}
			})
		}
	}
}

func TestWriterPanicOnTheHeadReleasesTheStream(t *testing.T) {
	i := newPendingTestInstance()
	i.ds_response = hookedWriter{ResponseRecorder: httptest.NewRecorder(), writeHeader: func(int) {
		panic("response writer bug")
	}}
	respHandle, _ := i.responses.New()
	source := &bodyLengthReadCloser{Reader: strings.NewReader("held")}
	bodyHandle, _ := i.bodies.NewReader(source)
	if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), 1); status != XqdStatusOK {
		t.Fatalf("send_downstream status = %d", status)
	}
	writesFailWithin(t, i, bodyHandle)
	if !i.finishDownstream() {
		t.Fatal("the response was not cut short")
	}
	if source.closed == 0 {
		t.Fatal("the held body was left open")
	}
}

func TestPendingBodyFailureCutsTheResponseShort(t *testing.T) {
	resp := serveDownstream(t, func(i *Instance) {
		phid, pr := i.pendingRequests.New()
		body := io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(io.ErrUnexpectedEOF))
		pr.Complete(&http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(body)}, nil)
		if status := i.xqd_resp_send_downstream_pending(int32(phid)); status != XqdStatusOK {
			t.Errorf("send_downstream_pending status = %d", status)
		}
	})
	body, err := io.ReadAll(resp.Body)
	if string(body) != "partial" || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("client read %q, %v, want %q and an unexpected EOF", body, err, "partial")
	}
}

func TestGuestExitEndsARequestBodyThePendingResponseWaitsFor(t *testing.T) {
	i := newStreamTestInstance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	recorder := httptest.NewRecorder()
	i.ds_response = recorder
	reqHandle, _ := i.requests.New()
	bodyHandle, _ := i.bodies.NewBuffer()
	if status := i.xqd_req_send_async_streaming(int32(reqHandle), int32(bodyHandle), streamBackendAddr, int32(len("origin")), streamRespOut); status != XqdStatusOK {
		t.Fatalf("send_async_streaming status = %d", status)
	}
	if status := i.xqd_resp_send_downstream_pending(int32(i.memory.Uint32(streamRespOut))); status != XqdStatusOK {
		t.Fatalf("send_downstream_pending status = %d", status)
	}

	// The guest exits without finishing the request body.
	within(t, "the response", i.finishDownstream)
	if recorder.Code < 500 {
		t.Fatalf("status = %d, want an error for the unfinished request", recorder.Code)
	}
}

func TestGuestExitEndsACacheFillTheResponseReads(t *testing.T) {
	i := newPendingTestInstance()
	i.cache = NewCache()
	recorder := httptest.NewRecorder()
	i.ds_response = recorder
	key := []byte("filling")
	obj := i.cache.Insert(key, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute)})
	insertHandle := i.newCacheInsertBody(obj, key)
	writeBody(t, i, insertHandle, "partial")
	readHandle, _ := i.bodies.NewReader(io.NopCloser(&cacheBodyReader{cache: obj}))
	respHandle, _ := i.responses.New()
	if status := i.xqd_resp_send_downstream(int32(respHandle), int32(readHandle), 0); status != XqdStatusOK {
		t.Fatalf("send_downstream status = %d", status)
	}

	// The guest exits without finishing the insert.
	if !within(t, "the response", i.finishDownstream) {
		t.Fatal("the body of an abandoned insert was not cut short")
	}
	if got := recorder.Body.String(); got != "partial" {
		t.Fatalf("downstream body = %q, want %q", got, "partial")
	}
}

func TestStoreInsertsRejectAStreamingBody(t *testing.T) {
	i := newPendingTestInstance()
	i.ds_response = httptest.NewRecorder()
	respHandle, _ := i.responses.New()
	bodyHandle, body := i.bodies.NewBuffer()
	_, _ = body.Write([]byte("sent to the client"))
	if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), 1); status != XqdStatusOK {
		t.Fatalf("send_downstream status = %d", status)
	}

	// Production takes the body, which fails on a streaming handle.
	i.kvStores = &KVStoreHandles{}
	store := NewKVStore("store")
	storeHandle := i.kvStores.New(store)
	keyAddr, keySize := writeStr(t, i, 100, "key")
	if status := i.xqd_object_store_insert(int32(storeHandle), keyAddr, keySize, int32(bodyHandle)); status != XqdErrInvalidHandle {
		t.Fatalf("object_store_insert status = %d, want %d", status, XqdErrInvalidHandle)
	}
	closeBody(t, i, int32(bodyHandle))
	i.finishDownstream()
	if stored, _ := store.Lookup("key"); stored != nil {
		t.Fatal("the streaming body was stored")
	}
}

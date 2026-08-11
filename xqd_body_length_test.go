package fastlike

import (
	"context"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func newBodyLengthTestInstance() *Instance {
	return &Instance{
		bodies: &BodyHandles{},
		memory: &Memory{ByteMemory(make([]byte, 1024))},
		abilog: log.New(io.Discard, "", 0),
	}
}

type bodyLengthReadCloser struct {
	io.Reader
	closed int
}

func (r *bodyLengthReadCloser) Close() error {
	r.closed++
	return nil
}

func TestBodyKnownLengthReportsEmptyNewBody(t *testing.T) {
	i := newBodyLengthTestInstance()
	if status := i.xqd_body_new(0); status != XqdStatusOK {
		t.Fatalf("body_new status = %d, want %d", status, XqdStatusOK)
	}
	handle := int32(i.memory.Uint32(0))

	if status := i.xqd_body_known_length(handle, 8); status != XqdStatusOK {
		t.Fatalf("known_length status = %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint64(8); got != 0 {
		t.Fatalf("known length = %d, want 0", got)
	}
}

func TestBodyKnownLengthTracksPrependAndAppend(t *testing.T) {
	i := newBodyLengthTestInstance()
	dstHandle, dst := i.bodies.NewBuffer()
	if _, err := dst.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	srcHandle, src := i.bodies.NewBuffer()
	if _, err := src.Write([]byte("cde")); err != nil {
		t.Fatal(err)
	}

	if status := i.xqd_body_append(int32(dstHandle), int32(srcHandle)); status != XqdStatusOK {
		t.Fatalf("body_append status = %d, want %d", status, XqdStatusOK)
	}
	if _, err := i.memory.WriteAt([]byte("xy"), 100); err != nil {
		t.Fatal(err)
	}
	if status := i.xqd_body_write(int32(dstHandle), 100, 2, BodyWriteEndFront, 200); status != XqdStatusOK {
		t.Fatalf("front body_write status = %d, want %d", status, XqdStatusOK)
	}

	if status := i.xqd_body_known_length(int32(dstHandle), 208); status != XqdStatusOK {
		t.Fatalf("known_length status = %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint64(208); got != 7 {
		t.Fatalf("known length = %d, want 7", got)
	}
	if status := i.xqd_body_known_length(int32(srcHandle), 216); status != XqdErrInvalidHandle {
		t.Fatalf("known_length on consumed source = %d, want %d", status, XqdErrInvalidHandle)
	}
}

func TestBodyKnownLengthIsAbsentForStreamingBody(t *testing.T) {
	i := newBodyLengthTestInstance()
	handle, body := i.bodies.NewBuffer()
	if _, err := body.Write([]byte("already-buffered")); err != nil {
		t.Fatal(err)
	}
	body.isStreaming = true

	if status := i.xqd_body_known_length(int32(handle), 8); status != XqdErrNone {
		t.Fatalf("known_length status = %d, want %d", status, XqdErrNone)
	}
}

func TestBodyKnownLengthTracksUnreadBytes(t *testing.T) {
	i := newBodyLengthTestInstance()
	handle, body := i.bodies.NewBuffer()
	if _, err := body.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 2)
	if n, err := body.Read(buf); err != nil || n != len(buf) {
		t.Fatalf("body read = (%d, %v), want (2, nil)", n, err)
	}
	if status := i.xqd_body_known_length(int32(handle), 8); status != XqdStatusOK {
		t.Fatalf("known_length status = %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint64(8); got != 1 {
		t.Fatalf("known unread length = %d, want 1", got)
	}
}

func TestReqSendDoesNotEmitNegativeContentLengthForUnknownBody(t *testing.T) {
	i := &Instance{
		requests:   &RequestHandles{},
		responses:  &ResponseHandles{},
		bodies:     &BodyHandles{},
		backends:   map[string]*Backend{},
		memory:     &Memory{ByteMemory(make([]byte, 1024))},
		abilog:     log.New(io.Discard, "", 0),
		ds_context: context.Background(),
	}
	var contentLength string
	i.addBackend("origin", &Backend{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentLength = r.Header.Get("Content-Length")
		w.WriteHeader(http.StatusNoContent)
	})})
	reqHandle, _ := i.requests.New()
	bodyHandle, _ := i.bodies.NewReader(io.NopCloser(strings.NewReader("unknown-length")))
	backendAddr, backendSize := writeStr(t, i, 100, "origin")

	if status := i.xqd_req_send(int32(reqHandle), int32(bodyHandle), backendAddr, backendSize, 200, 204); status != XqdStatusOK {
		t.Fatalf("req_send status = %d, want %d", status, XqdStatusOK)
	}
	if contentLength != "" {
		t.Fatalf("Content-Length = %q, want no explicit header for unknown body", contentLength)
	}
}

func TestAutomaticFramingOmitsZeroLengthForMethodsWithoutPayload(t *testing.T) {
	tests := []struct {
		method string
		want   string
	}{
		{method: http.MethodGet},
		{method: http.MethodHead},
		{method: http.MethodConnect},
		{method: http.MethodDelete},
		{method: http.MethodTrace},
		{method: http.MethodPost, want: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, "http://example.com/", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Length", "guest-value")
			_, body := (&BodyHandles{}).NewBuffer()
			applyAutomaticBodyLength(req, body)
			if got := req.Header.Get("Content-Length"); got != tt.want {
				t.Fatalf("Content-Length = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBodyCloseAndAbandonInvalidateHandles(t *testing.T) {
	i := newBodyLengthTestInstance()
	closedHandle, _ := i.bodies.NewBuffer()
	if status := i.xqd_body_close(int32(closedHandle)); status != XqdStatusOK {
		t.Fatalf("body_close status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_body_close(int32(closedHandle)); status != XqdErrInvalidHandle {
		t.Fatalf("second body_close status = %d, want %d", status, XqdErrInvalidHandle)
	}

	abandonedHandle, _ := i.bodies.NewBuffer()
	if status := i.xqd_body_abandon(int32(abandonedHandle)); status != XqdStatusOK {
		t.Fatalf("body_abandon status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_body_known_length(int32(abandonedHandle), 8); status != XqdErrInvalidHandle {
		t.Fatalf("known_length on abandoned handle = %d, want %d", status, XqdErrInvalidHandle)
	}
}

func TestBodyTrailerNamesGetHasStableCursorOrder(t *testing.T) {
	i := newBodyLengthTestInstance()
	handle, body := i.bodies.NewBuffer()
	body.trailersReady = true
	body.trailers = http.Header{
		"Z-Key": {"z"},
		"A-Key": {"a"},
		"M-Key": {"m"},
	}
	want := []byte("A-Key\x00M-Key\x00Z-Key\x00")

	for attempt := 0; attempt < 32; attempt++ {
		if status := i.xqd_body_trailer_names_get(int32(handle), 100, 64, 0, 200, 208); status != XqdStatusOK {
			t.Fatalf("trailer_names_get status = %d, want %d", status, XqdStatusOK)
		}
		got := i.memory.Data()[100 : 100+i.memory.Uint32(208)]
		if !slices.Equal(got, want) {
			t.Fatalf("trailer names = %q, want stable order %q", got, want)
		}
	}
}

func TestBodyAppendTransfersSourceCloser(t *testing.T) {
	i := newBodyLengthTestInstance()
	dstHandle, _ := i.bodies.NewBuffer()
	srcReader := &bodyLengthReadCloser{Reader: strings.NewReader("source")}
	srcHandle, _ := i.bodies.NewReader(srcReader)

	if status := i.xqd_body_append(int32(dstHandle), int32(srcHandle)); status != XqdStatusOK {
		t.Fatalf("body_append status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_body_close(int32(dstHandle)); status != XqdStatusOK {
		t.Fatalf("body_close status = %d, want %d", status, XqdStatusOK)
	}
	if got := srcReader.closed; got != 1 {
		t.Fatalf("source close count = %d, want 1", got)
	}
}

func TestBodyReadInvalidDestinationDoesNotConsumeBody(t *testing.T) {
	i := newBodyLengthTestInstance()
	handle, body := i.bodies.NewBuffer()
	if _, err := body.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}

	if status := i.xqd_body_read(int32(handle), 1023, 2, 100); status != XqdError {
		t.Fatalf("body_read with invalid destination status = %d, want %d", status, XqdError)
	}
	if status := i.xqd_body_read(int32(handle), 200, 3, 300); status != XqdStatusOK {
		t.Fatalf("body_read with valid destination status = %d, want %d", status, XqdStatusOK)
	}
	if got := string(i.memory.Data()[200 : 200+i.memory.Uint32(300)]); got != "abc" {
		t.Fatalf("body after rejected read = %q, want %q", got, "abc")
	}
}

func TestBodyReadInvalidCountPointerDoesNotConsumeBody(t *testing.T) {
	i := newBodyLengthTestInstance()
	handle, body := i.bodies.NewBuffer()
	if _, err := body.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}

	if status := i.xqd_body_read(int32(handle), 200, 3, 1022); status != XqdError {
		t.Fatalf("body_read with invalid count pointer status = %d, want %d", status, XqdError)
	}
	if status := i.xqd_body_read(int32(handle), 200, 3, 300); status != XqdStatusOK {
		t.Fatalf("body_read with valid count pointer status = %d, want %d", status, XqdStatusOK)
	}
	if got := string(i.memory.Data()[200 : 200+i.memory.Uint32(300)]); got != "abc" {
		t.Fatalf("body after rejected read = %q, want %q", got, "abc")
	}
}

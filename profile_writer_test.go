package fastlike

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTraceWriterStatusFromExplicitWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	tw := newTraceResponseWriter(rec)
	tw.WriteHeader(http.StatusTeapot)
	_, _ = io.WriteString(tw, "hi")

	if got := tw.Status(); got != http.StatusTeapot {
		t.Errorf("status: got %d, want %d", got, http.StatusTeapot)
	}
	if got := tw.BytesWritten(); got != 2 {
		t.Errorf("bytes: got %d, want 2", got)
	}
	if tw.HeaderFlushed() == nil {
		t.Error("HeaderFlushed should be set after WriteHeader")
	}
	if tw.Hijacked() != nil {
		t.Error("Hijacked should be nil when no hijack happened")
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("underlying recorder did not see WriteHeader: %d", rec.Code)
	}
}

func TestTraceWriterImplicitWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	tw := newTraceResponseWriter(rec)
	_, _ = io.WriteString(tw, "no header")

	if got := tw.Status(); got != http.StatusOK {
		t.Errorf("status default: got %d, want 200", got)
	}
	if rec.Body.String() != "no header" {
		t.Errorf("body did not pass through: %q", rec.Body.String())
	}
}

func TestTraceWriterHonorsFirstWriteHeaderOnly(t *testing.T) {
	rec := httptest.NewRecorder()
	tw := newTraceResponseWriter(rec)
	tw.WriteHeader(http.StatusCreated)
	tw.WriteHeader(http.StatusGone)
	if got := tw.Status(); got != http.StatusCreated {
		t.Errorf("status: got %d, want 201 (first call wins)", got)
	}
}

func TestTraceWriterFlushPreserved(t *testing.T) {
	// httptest.ResponseRecorder satisfies http.Flusher.
	rec := httptest.NewRecorder()
	tw := newTraceResponseWriter(rec)

	f, ok := tw.(http.Flusher)
	if !ok {
		t.Fatal("wrapped recorder should still satisfy http.Flusher")
	}
	_, _ = io.WriteString(tw, "burst")
	f.Flush()
	if rec.Flushed != true {
		t.Error("Flush did not reach the underlying recorder")
	}
	if tw.HeaderFlushed() == nil {
		t.Error("HeaderFlushed should be set after explicit Flush")
	}
}

type minimalWriter struct {
	hdr  http.Header
	body bytes.Buffer
	code int
}

func (m *minimalWriter) Header() http.Header {
	if m.hdr == nil {
		m.hdr = http.Header{}
	}
	return m.hdr
}
func (m *minimalWriter) Write(b []byte) (int, error) { return m.body.Write(b) }
func (m *minimalWriter) WriteHeader(c int)           { m.code = c }

func TestTraceWriterDoesNotForgeFlusher(t *testing.T) {
	w := newTraceResponseWriter(&minimalWriter{})
	if _, ok := w.(http.Flusher); ok {
		t.Fatal("wrapper must not claim http.Flusher when underlying writer does not satisfy it")
	}
	if _, ok := w.(http.Hijacker); ok {
		t.Fatal("wrapper must not claim http.Hijacker when underlying writer does not satisfy it")
	}
	if _, ok := w.(io.ReaderFrom); ok {
		t.Fatal("wrapper must not claim io.ReaderFrom when underlying writer does not satisfy it")
	}
	if _, ok := w.(http.Pusher); ok {
		t.Fatal("wrapper must not claim http.Pusher when underlying writer does not satisfy it")
	}
}

type hijackableWriter struct {
	*minimalWriter
	hijackCalled bool
}

func (h *hijackableWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijackCalled = true
	c1, c2 := net.Pipe()
	_ = c2.Close()
	return c1, bufio.NewReadWriter(bufio.NewReader(strings.NewReader("")), bufio.NewWriter(io.Discard)), nil
}

func TestTraceWriterHijackForwardsAndRecords(t *testing.T) {
	hw := &hijackableWriter{minimalWriter: &minimalWriter{}}
	tw := newTraceResponseWriter(hw)

	hj, ok := tw.(http.Hijacker)
	if !ok {
		t.Fatal("wrapper should expose Hijacker when underlying does")
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		t.Fatalf("hijack failed: %v", err)
	}
	_ = conn.Close()

	if !hw.hijackCalled {
		t.Error("hijack did not reach the underlying writer")
	}
	if tw.Hijacked() == nil {
		t.Error("Hijacked() should report a non-nil offset after hijack")
	}
	if tw.Status() != http.StatusSwitchingProtocols {
		t.Errorf("Status after hijack without WriteHeader: got %d, want 101", tw.Status())
	}
}

type readerFromWriter struct {
	*minimalWriter
}

func (rfw *readerFromWriter) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(&rfw.body, r)
}

func TestTraceWriterReadFromForwardsAndCounts(t *testing.T) {
	rfw := &readerFromWriter{minimalWriter: &minimalWriter{}}
	tw := newTraceResponseWriter(rfw)

	rf, ok := tw.(io.ReaderFrom)
	if !ok {
		t.Fatal("wrapper should expose io.ReaderFrom when underlying does")
	}
	src := strings.NewReader("twelve bytes")
	n, err := rf.ReadFrom(src)
	if err != nil {
		t.Fatalf("ReadFrom failed: %v", err)
	}
	if n != 12 {
		t.Errorf("ReadFrom returned %d, want 12", n)
	}
	if tw.BytesWritten() != 12 {
		t.Errorf("BytesWritten after ReadFrom: got %d, want 12", tw.BytesWritten())
	}
	if tw.Status() != http.StatusOK {
		t.Errorf("Status implicit after ReadFrom: got %d, want 200", tw.Status())
	}
}

type pusherWriter struct {
	*minimalWriter
	pushed string
}

func (pw *pusherWriter) Push(target string, _ *http.PushOptions) error {
	pw.pushed = target
	return nil
}

func TestTraceWriterPushForwards(t *testing.T) {
	pw := &pusherWriter{minimalWriter: &minimalWriter{}}
	tw := newTraceResponseWriter(pw)

	p, ok := tw.(http.Pusher)
	if !ok {
		t.Fatal("wrapper should expose http.Pusher when underlying does")
	}
	if err := p.Push("/x", nil); err != nil {
		t.Fatalf("Push failed: %v", err)
	}
	if pw.pushed != "/x" {
		t.Errorf("push target not forwarded: %q", pw.pushed)
	}
}

type allCapsWriter struct {
	*minimalWriter
}

func (a *allCapsWriter) Flush()                                       {}
func (a *allCapsWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, nil }
func (a *allCapsWriter) ReadFrom(r io.Reader) (int64, error)          { return io.Copy(&a.body, r) }
func (a *allCapsWriter) Push(_ string, _ *http.PushOptions) error     { return nil }

func TestTraceWriterAllCapsWrapper(t *testing.T) {
	w := newTraceResponseWriter(&allCapsWriter{minimalWriter: &minimalWriter{}})
	if _, ok := w.(http.Flusher); !ok {
		t.Error("Flusher missing on all-caps wrapper")
	}
	if _, ok := w.(http.Hijacker); !ok {
		t.Error("Hijacker missing on all-caps wrapper")
	}
	if _, ok := w.(io.ReaderFrom); !ok {
		t.Error("ReaderFrom missing on all-caps wrapper")
	}
	if _, ok := w.(http.Pusher); !ok {
		t.Error("Pusher missing on all-caps wrapper")
	}
}

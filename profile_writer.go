package fastlike

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// traceResponseWriter wraps an http.ResponseWriter so the profile recorder
// can observe the status code, response byte count, header-flush moment, and
// hijack moment without changing the guest-visible behavior of the response
// writer. It is constructed by newTraceResponseWriter, which returns one of
// 16 pre-built wrapper types whose method set exactly matches the
// interfaces the underlying writer satisfied. This is the same approach
// httpsnoop takes; inlined here to avoid a dependency on a third-party
// package for one file.
//
// The wrapper is safe for concurrent observation reads (Status,
// BytesWritten, HeaderFlushed, Hijacked) by code outside the request
// goroutine; the recording side runs only from the request goroutine.
type traceResponseWriter struct {
	inner http.ResponseWriter

	start             time.Time
	status            atomic.Int32 // 0 means WriteHeader never called explicitly
	bytes             atomic.Int64
	headerFlushNanos  atomic.Int64 // 0 means never flushed; first non-zero wins
	hijackNanos       atomic.Int64
	headerFlushSet    atomic.Bool
	hijackSet         atomic.Bool
	wroteHeaderCalled atomic.Bool
}

// newTraceResponseWriter wraps w so observations land on the returned
// responseObserver while preserving every optional interface w satisfies.
// The returned value is also an http.ResponseWriter so callers can use it
// directly in place of w.
func newTraceResponseWriter(w http.ResponseWriter) responseObserver {
	base := &traceResponseWriter{
		inner: w,
		start: time.Now(),
	}
	_, hasFlusher := w.(http.Flusher)
	_, hasHijacker := w.(http.Hijacker)
	_, hasReaderFrom := w.(io.ReaderFrom)
	_, hasPusher := w.(http.Pusher)
	return pickWrapper(base, hasFlusher, hasHijacker, hasReaderFrom, hasPusher)
}

func (t *traceResponseWriter) Header() http.Header { return t.inner.Header() }

func (t *traceResponseWriter) WriteHeader(status int) {
	if !t.wroteHeaderCalled.Swap(true) {
		t.status.Store(int32(status))
		t.markHeaderFlush()
	}
	t.inner.WriteHeader(status)
}

func (t *traceResponseWriter) Write(b []byte) (int, error) {
	if !t.wroteHeaderCalled.Swap(true) {
		t.status.Store(int32(http.StatusOK))
		t.markHeaderFlush()
	}
	n, err := t.inner.Write(b)
	t.bytes.Add(int64(n))
	return n, err
}

func (t *traceResponseWriter) Status() int {
	s := t.status.Load()
	if s == 0 && t.wroteHeaderCalled.Load() {
		return http.StatusOK
	}
	return int(s)
}

func (t *traceResponseWriter) BytesWritten() int64 { return t.bytes.Load() }

func (t *traceResponseWriter) HeaderFlushed() *int64 {
	if !t.headerFlushSet.Load() {
		return nil
	}
	v := t.headerFlushNanos.Load()
	return &v
}

func (t *traceResponseWriter) Hijacked() *int64 {
	if !t.hijackSet.Load() {
		return nil
	}
	v := t.hijackNanos.Load()
	return &v
}

func (t *traceResponseWriter) markHeaderFlush() {
	if t.headerFlushSet.Load() {
		return
	}
	t.headerFlushNanos.Store(time.Since(t.start).Nanoseconds())
	t.headerFlushSet.Store(true)
}

func (t *traceResponseWriter) markHijack() {
	if t.hijackSet.Load() {
		return
	}
	t.hijackNanos.Store(time.Since(t.start).Nanoseconds())
	t.hijackSet.Store(true)
	if !t.wroteHeaderCalled.Load() {
		t.status.Store(int32(http.StatusSwitchingProtocols))
	}
}

func (t *traceResponseWriter) flushUnderlying() {
	if f, ok := t.inner.(http.Flusher); ok {
		t.markHeaderFlush()
		f.Flush()
	}
}

func (t *traceResponseWriter) hijackUnderlying() (net.Conn, *bufio.ReadWriter, error) {
	h := t.inner.(http.Hijacker)
	conn, brw, err := h.Hijack()
	if err == nil {
		t.markHijack()
	}
	return conn, brw, err
}

func (t *traceResponseWriter) readFromUnderlying(src io.Reader) (int64, error) {
	rf := t.inner.(io.ReaderFrom)
	if !t.wroteHeaderCalled.Swap(true) {
		t.status.Store(int32(http.StatusOK))
		t.markHeaderFlush()
	}
	n, err := rf.ReadFrom(src)
	t.bytes.Add(n)
	return n, err
}

func (t *traceResponseWriter) pushUnderlying(target string, opts *http.PushOptions) error {
	return t.inner.(http.Pusher).Push(target, opts)
}

// pickWrapper returns the smallest concrete wrapper type that exposes
// exactly the optional interfaces present on the underlying writer. The 16
// types below are intentionally hand-rolled rather than generated so the
// dispatch is grep-able.
func pickWrapper(t *traceResponseWriter, f, h, r, p bool) responseObserver {
	mask := 0
	if f {
		mask |= 1
	}
	if h {
		mask |= 2
	}
	if r {
		mask |= 4
	}
	if p {
		mask |= 8
	}
	switch mask {
	case 0:
		return twBase{t}
	case 1:
		return twF{t}
	case 2:
		return twH{t}
	case 3:
		return twFH{t}
	case 4:
		return twR{t}
	case 5:
		return twFR{t}
	case 6:
		return twHR{t}
	case 7:
		return twFHR{t}
	case 8:
		return twP{t}
	case 9:
		return twFP{t}
	case 10:
		return twHP{t}
	case 11:
		return twFHP{t}
	case 12:
		return twRP{t}
	case 13:
		return twFRP{t}
	case 14:
		return twHRP{t}
	case 15:
		return twFHRP{t}
	}
	return twBase{t}
}

type (
	twBase struct{ *traceResponseWriter }
	twF    struct{ *traceResponseWriter }
	twH    struct{ *traceResponseWriter }
	twFH   struct{ *traceResponseWriter }
	twR    struct{ *traceResponseWriter }
	twFR   struct{ *traceResponseWriter }
	twHR   struct{ *traceResponseWriter }
	twFHR  struct{ *traceResponseWriter }
	twP    struct{ *traceResponseWriter }
	twFP   struct{ *traceResponseWriter }
	twHP   struct{ *traceResponseWriter }
	twFHP  struct{ *traceResponseWriter }
	twRP   struct{ *traceResponseWriter }
	twFRP  struct{ *traceResponseWriter }
	twHRP  struct{ *traceResponseWriter }
	twFHRP struct{ *traceResponseWriter }
)

func (w twF) Flush()    { w.flushUnderlying() }
func (w twFH) Flush()   { w.flushUnderlying() }
func (w twFR) Flush()   { w.flushUnderlying() }
func (w twFHR) Flush()  { w.flushUnderlying() }
func (w twFP) Flush()   { w.flushUnderlying() }
func (w twFHP) Flush()  { w.flushUnderlying() }
func (w twFRP) Flush()  { w.flushUnderlying() }
func (w twFHRP) Flush() { w.flushUnderlying() }

func (w twH) Hijack() (net.Conn, *bufio.ReadWriter, error)    { return w.hijackUnderlying() }
func (w twFH) Hijack() (net.Conn, *bufio.ReadWriter, error)   { return w.hijackUnderlying() }
func (w twHR) Hijack() (net.Conn, *bufio.ReadWriter, error)   { return w.hijackUnderlying() }
func (w twFHR) Hijack() (net.Conn, *bufio.ReadWriter, error)  { return w.hijackUnderlying() }
func (w twHP) Hijack() (net.Conn, *bufio.ReadWriter, error)   { return w.hijackUnderlying() }
func (w twFHP) Hijack() (net.Conn, *bufio.ReadWriter, error)  { return w.hijackUnderlying() }
func (w twHRP) Hijack() (net.Conn, *bufio.ReadWriter, error)  { return w.hijackUnderlying() }
func (w twFHRP) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.hijackUnderlying() }

func (w twR) ReadFrom(src io.Reader) (int64, error)    { return w.readFromUnderlying(src) }
func (w twFR) ReadFrom(src io.Reader) (int64, error)   { return w.readFromUnderlying(src) }
func (w twHR) ReadFrom(src io.Reader) (int64, error)   { return w.readFromUnderlying(src) }
func (w twFHR) ReadFrom(src io.Reader) (int64, error)  { return w.readFromUnderlying(src) }
func (w twRP) ReadFrom(src io.Reader) (int64, error)   { return w.readFromUnderlying(src) }
func (w twFRP) ReadFrom(src io.Reader) (int64, error)  { return w.readFromUnderlying(src) }
func (w twHRP) ReadFrom(src io.Reader) (int64, error)  { return w.readFromUnderlying(src) }
func (w twFHRP) ReadFrom(src io.Reader) (int64, error) { return w.readFromUnderlying(src) }

func (w twP) Push(target string, opts *http.PushOptions) error { return w.pushUnderlying(target, opts) }
func (w twFP) Push(target string, opts *http.PushOptions) error {
	return w.pushUnderlying(target, opts)
}
func (w twHP) Push(target string, opts *http.PushOptions) error {
	return w.pushUnderlying(target, opts)
}
func (w twFHP) Push(target string, opts *http.PushOptions) error {
	return w.pushUnderlying(target, opts)
}
func (w twRP) Push(target string, opts *http.PushOptions) error {
	return w.pushUnderlying(target, opts)
}
func (w twFRP) Push(target string, opts *http.PushOptions) error {
	return w.pushUnderlying(target, opts)
}
func (w twHRP) Push(target string, opts *http.PushOptions) error {
	return w.pushUnderlying(target, opts)
}
func (w twFHRP) Push(target string, opts *http.PushOptions) error {
	return w.pushUnderlying(target, opts)
}

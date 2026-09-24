package fastlike

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// backendBodyBuffer is how much of a backend response body may wait for the
// guest before the backend handler blocks.
const backendBodyBuffer = 64 * 1024

// errBetweenBytesTimeout fails a read that waited too long for the next chunk.
var errBetweenBytesTimeout = errors.New("backend between-bytes timeout")

var errBackendBodyClosed = errors.New("backend response body closed")

// backendBodyError ends a backend body that failed after the headers.
type backendBodyError struct {
	err error
}

func (e *backendBodyError) Error() string { return "backend response body: " + e.err.Error() }
func (e *backendBodyError) Unwrap() error { return e.err }

// bodyReadStatus maps a read failure like production, where only a
// truncated backend body is incomplete.
func bodyReadStatus(err error) int32 {
	var bodyErr *backendBodyError
	if errors.As(err, &bodyErr) && errors.Is(bodyErr.err, io.ErrUnexpectedEOF) {
		return XqdErrHttpIncomplete
	}
	return XqdError
}

// backendBody is the guest's end of a backend body that is still arriving.
type backendBody struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	chunks   [][]byte
	buffered int
	// end is what reads return after the chunks: io.EOF or a
	// *backendBodyError.
	end      error
	closed   bool
	trailers http.Header
	// changed wakes whoever waits on the body, and is only made for them.
	changed chan struct{}

	// Like production, the between-bytes timeout counts from the last chunk
	// the guest got, and only fails reads that find nothing.
	betweenBytes time.Duration
	lastChunk    time.Time
	timer        *time.Timer
}

func (b *backendBody) notifyLocked() {
	if b.changed != nil {
		close(b.changed)
		b.changed = nil
	}
}

func (b *backendBody) changedLocked() <-chan struct{} {
	if b.changed == nil {
		b.changed = make(chan struct{})
	}
	return b.changed
}

func (b *backendBody) stopTimerLocked() {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
}

func (b *backendBody) startBetweenBytesTimer(timeout time.Duration) {
	b.betweenBytes = timeout
	b.lastChunk = time.Now()
	b.timer = time.AfterFunc(timeout, func() {
		b.mu.Lock()
		b.notifyLocked()
		b.mu.Unlock()
	})
}

func (b *backendBody) timedOutLocked() bool {
	return b.timer != nil && time.Since(b.lastChunk) >= b.betweenBytes
}

// waitLocked waits, unlocked, for the body to change.
func (b *backendBody) waitLocked() error {
	changed := b.changedLocked()
	b.mu.Unlock()
	defer b.mu.Lock()
	select {
	case <-changed:
		return nil
	case <-b.ctx.Done():
		return b.ctx.Err()
	}
}

// Read returns at most one chunk, like production's body_read.
func (b *backendBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		switch {
		case b.closed:
			return 0, errBackendBodyClosed
		case len(b.chunks) > 0:
			n := copy(p, b.chunks[0])
			if n == len(b.chunks[0]) {
				b.chunks[0] = nil
				b.chunks = b.chunks[1:]
			} else {
				b.chunks[0] = b.chunks[0][n:]
			}
			b.buffered -= n
			if b.timer != nil {
				b.lastChunk = time.Now()
				b.timer.Reset(b.betweenBytes)
			}
			b.notifyLocked()
			return n, nil
		case b.end != nil:
			err := b.end
			// An HTTP/1 body reports its failure once, then ends.
			b.end = io.EOF
			return 0, err
		case b.timedOutLocked():
			return 0, errBetweenBytesTimeout
		}
		if err := b.waitLocked(); err != nil {
			return 0, err
		}
	}
}

// write blocks while the guest has too much left to read.
func (b *backendBody) write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		chunk := bytes.Clone(p[:min(len(p), backendBodyBuffer)])
		if err := b.queue(chunk); err != nil {
			return written, err
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

func (b *backendBody) queue(chunk []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.buffered >= backendBodyBuffer && !b.closed {
		if err := b.waitLocked(); err != nil {
			return err
		}
	}
	if b.closed {
		return errBackendBodyClosed
	}
	b.chunks = append(b.chunks, chunk)
	b.buffered += len(chunk)
	b.notifyLocked()
	return nil
}

func (b *backendBody) finish(end error, trailers http.Header) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.end = end
	b.trailers = trailers
	b.stopTimerLocked()
	b.notifyLocked()
}

// Close drops what the handler wrote and aborts what it is still sending.
func (b *backendBody) Close() error {
	b.cancel()
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		b.chunks, b.buffered = nil, 0
		b.stopTimerLocked()
		b.notifyLocked()
	}
	return nil
}

// Trailers is nil until the handler is done.
func (b *backendBody) Trailers() http.Header {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.trailers
}

func (b *backendBody) ended() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.closed && len(b.chunks) == 0 && b.end == io.EOF
}

// readyChannel is closed once a read would not block.
func (b *backendBody) readyChannel() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || len(b.chunks) > 0 || b.end != nil || b.timedOutLocked() {
		return closedStreamingReadyChannel()
	}
	return b.changedLocked()
}

// backendResponseWriter hands the send its response as soon as the headers
// are written, while the handler keeps writing the body.
type backendResponseWriter struct {
	req    *http.Request
	header http.Header
	body   *backendBody

	// ready is closed once resp or failure is set.
	ready   chan struct{}
	resp    *http.Response
	failure error

	betweenBytes   time.Duration
	autoDecompress uint32
	// transportErr is set by Fastlike's transport handler through the
	// request context.
	transportErr error

	bodyAllowed       bool
	declared          int64
	written           int64
	announcedTrailers []string
}

type backendResponseCtxKey struct{}

// captureBackendError fails the send with a transport error, or its body
// once the headers are out.
// It reports false outside a send, where the handler has to answer itself.
func captureBackendError(ctx context.Context, err error) bool {
	w, ok := ctx.Value(backendResponseCtxKey{}).(*backendResponseWriter)
	if ok {
		w.transportErr = err
	}
	return ok
}

// serveBackend runs handler for req in its own goroutine.
// The writer is ready once the headers are in, or the handler failed before
// sending them.
func serveBackend(handler http.Handler, req *http.Request, betweenBytes time.Duration, autoDecompress uint32) *backendResponseWriter {
	requestDecompression(req, autoDecompress)
	ctx, cancel := context.WithCancel(req.Context())
	w := &backendResponseWriter{
		header:         http.Header{},
		body:           &backendBody{ctx: req.Context(), cancel: cancel},
		ready:          make(chan struct{}),
		betweenBytes:   betweenBytes,
		autoDecompress: autoDecompress,
	}
	w.req = req.WithContext(context.WithValue(ctx, backendResponseCtxKey{}, w))
	go func() {
		defer func() { w.finish(recover()) }()
		// Like a server, the request body is closed once the handler returns.
		if w.req.Body != nil {
			defer func() { _ = w.req.Body.Close() }()
		}
		handler.ServeHTTP(w, w.req)
	}()
	return w
}

func (w *backendResponseWriter) Header() http.Header {
	return w.header
}

func (w *backendResponseWriter) WriteHeader(code int) {
	if w.resp != nil {
		return
	}
	if code < 100 || code > 999 {
		panic(fmt.Sprintf("invalid WriteHeader code %v", code))
	}
	// Like hyper, skip informational responses other than 101.
	if code < 200 && code != http.StatusSwitchingProtocols {
		return
	}
	header := w.header.Clone()
	w.announcedTrailers = slices.Clone(header["Trailer"])
	w.bodyAllowed = responseHasBody(w.req.Method, code)
	w.declared = -1
	length := int64(0)
	if w.bodyAllowed {
		w.declared = declaredContentLength(header)
		length = w.declared
	}
	w.resp = &http.Response{
		Status:        fmt.Sprintf("%03d %s", code, http.StatusText(code)),
		StatusCode:    code,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          w.body,
		ContentLength: length,
		Request:       w.req,
	}
	applyAutoDecompression(w.resp, w.autoDecompress)
	if w.betweenBytes > 0 {
		w.body.startBetweenBytesTimer(w.betweenBytes)
	}
	close(w.ready)
}

// Write rejects bodies the way net/http's server does.
func (w *backendResponseWriter) Write(p []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if !w.bodyAllowed {
		if w.req.Method == http.MethodHead {
			return len(p), nil
		}
		return 0, http.ErrBodyNotAllowed
	}
	if w.declared >= 0 && w.written+int64(len(p)) > w.declared {
		return 0, http.ErrContentLength
	}
	n, err := w.body.write(p)
	w.written += int64(n)
	return n, err
}

func (w *backendResponseWriter) Flush() {
	w.WriteHeader(http.StatusOK)
}

func (w *backendResponseWriter) finish(panicked any) {
	defer w.body.cancel()
	if w.resp == nil {
		switch {
		case panicked != nil:
			w.failure = fmt.Errorf("backend handler panic: %v", panicked)
		case w.transportErr != nil:
			w.failure = &cachingSendError{err: w.transportErr}
		default:
			w.WriteHeader(http.StatusOK)
		}
		if w.failure != nil {
			close(w.ready)
			return
		}
	}
	end := io.EOF
	switch {
	case w.transportErr != nil:
		end = &backendBodyError{err: w.transportErr}
	case panicked != nil, w.written < w.declared:
		// A server closes the connection here, which truncates the body.
		end = &backendBodyError{err: io.ErrUnexpectedEOF}
	}
	w.body.finish(end, w.trailers())
}

// trailers collects them the way httptest.ResponseRecorder does.
func (w *backendResponseWriter) trailers() http.Header {
	var trailers http.Header
	add := func(name string, values []string) {
		if trailers == nil {
			trailers = http.Header{}
		}
		trailers[name] = append(trailers[name], values...)
	}
	for _, declared := range w.announcedTrailers {
		for name := range strings.SplitSeq(declared, ",") {
			name = http.CanonicalHeaderKey(strings.TrimSpace(name))
			if values, ok := w.header[name]; ok {
				add(name, values)
			}
		}
	}
	for name, values := range w.header {
		if trimmed, ok := strings.CutPrefix(name, http.TrailerPrefix); ok {
			add(http.CanonicalHeaderKey(trimmed), values)
		}
	}
	return trailers
}

// responseHasBody follows hyper: answers to HEAD, 1xx, 204 and 304
// responses, and successful answers to CONNECT have no body.
func responseHasBody(method string, code int) bool {
	switch {
	case method == http.MethodHead, code < 200, code == http.StatusNoContent, code == http.StatusNotModified:
		return false
	case method == http.MethodConnect && code < 300:
		return false
	}
	return true
}

// declaredContentLength is -1 unless a single valid Content-Length is set.
func declaredContentLength(header http.Header) int64 {
	if !contentLengthIsValid(header) {
		return -1
	}
	length, err := strconv.ParseInt(header.Get("Content-Length"), 10, 64)
	if err != nil {
		return -1
	}
	return length
}

// gzipBody decompresses a backend body as the guest reads it.
// Like production, it decodes one gzip member and ignores truncation and
// bad checksums.
type gzipBody struct {
	src    io.ReadCloser
	srcErr error
	zr     *gzip.Reader
	err    error
}

// gzipSource remembers how the compressed body ended.
type gzipSource struct{ g *gzipBody }

func (s gzipSource) Read(p []byte) (int, error) {
	n, err := s.g.src.Read(p)
	if err != nil {
		s.g.srcErr = err
	}
	return n, err
}

func (g *gzipBody) Read(p []byte) (int, error) {
	if g.err != nil {
		return 0, g.err
	}
	if g.zr == nil {
		zr, err := gzip.NewReader(gzipSource{g})
		if err != nil {
			g.err = g.mapError(err)
			return 0, g.err
		}
		zr.Multistream(false)
		g.zr = zr
	}
	n, err := g.zr.Read(p)
	if err != nil {
		g.err = g.mapError(err)
	}
	return n, g.err
}

func (g *gzipBody) mapError(err error) error {
	switch {
	case g.srcErr != nil && g.srcErr != io.EOF:
		return g.srcErr
	case err == io.EOF, err == io.ErrUnexpectedEOF, err == gzip.ErrChecksum:
		return io.EOF
	default:
		return fmt.Errorf("gzip decompression: %w", err)
	}
}

func (g *gzipBody) Close() error {
	return g.src.Close()
}

func (g *gzipBody) Trailers() http.Header {
	if src, ok := g.src.(trailerBody); ok {
		return src.Trailers()
	}
	return nil
}

func (g *gzipBody) ended() bool {
	return g.err == io.EOF
}

func (g *gzipBody) readyChannel() <-chan struct{} {
	if src, ok := g.src.(readyBody); ok {
		return src.readyChannel()
	}
	return closedStreamingReadyChannel()
}

type trailerBody interface {
	Trailers() http.Header
}

// readyBody is a body whose reads can block.
type readyBody interface {
	readyChannel() <-chan struct{}
	// ended reports a clean end, where a chain moves on to its next part.
	ended() bool
}

// applyAutoDecompression decompresses gzip responses when the guest asked
// for it, matching production's header rules.
func applyAutoDecompression(resp *http.Response, autoDecompressEncodings uint32) {
	if autoDecompressEncodings&ContentEncodingsGzip == 0 {
		return
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "gzip" && encoding != "x-gzip" {
		return
	}
	resp.Body = &gzipBody{src: resp.Body}
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")
	resp.ContentLength = -1
}

// requestDecompression asks for gzip, overriding the guest, like production.
func requestDecompression(req *http.Request, autoDecompressEncodings uint32) {
	if autoDecompressEncodings&ContentEncodingsGzip != 0 {
		req.Header.Set("Accept-Encoding", "gzip")
	}
}

package fastlike

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const (
	// A connection stops being tracked once one request head grows past this, which real client heads never do.
	maxCapturedHeadBytes = 64 << 10

	// Chunk sizes and trailer lines are short, so anything longer means the scanner lost track of the stream.
	maxCapturedLineBytes = 8 << 10

	// Heads only pile up when the server drops requests without running the handler, so a small bound is plenty.
	maxCapturedHeads = 32

	// One outsized request should not hold on to its buffer for the rest of a long-lived connection.
	maxRetainedBufferBytes = 4 << 10
)

// CaptureOriginalHeaders records the raw head of every request srv accepts on ln, so guests read the downstream header names as the client wrote them.
// Serve the returned listener instead of ln.
//
// It also stops srv answering "OPTIONS *" by itself, which Fastly leaves to the guest, and which the capture needs to see reach the handler.
//
// Wrap the plain TCP listener, never a TLS one: the server needs to see the *tls.Conn to fill in the request's TLS state.
// Capture therefore covers cleartext HTTP/1 only, and requests over TLS or HTTP/2 fall back to a reconstruction.
func CaptureOriginalHeaders(srv *http.Server, ln net.Listener) net.Listener {
	srv.DisableGeneralOptionsHandler = true

	previous := srv.ConnContext
	srv.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		if previous != nil {
			ctx = previous(ctx, c)
		}
		if conn, ok := c.(*captureConn); ok {
			ctx = context.WithValue(ctx, originalHeadersCtxKey{}, conn.capture)
		}
		return ctx
	}
	return &captureListener{Listener: ln}
}

type captureListener struct {
	net.Listener
}

func (l *captureListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &captureConn{Conn: conn, capture: &headerCapture{}}, nil
}

// captureConn tees everything the server reads into the connection's scanner.
type captureConn struct {
	net.Conn
	capture *headerCapture
}

func (c *captureConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.capture.feed(p[:n])
	}
	return n, err
}

// ReadFrom keeps the server's sendfile and splice paths reachable, which the wrapper would otherwise hide.
func (c *captureConn) ReadFrom(r io.Reader) (int64, error) {
	if source, ok := c.Conn.(io.ReaderFrom); ok {
		return source.ReadFrom(r)
	}
	return io.Copy(c.Conn, r)
}

// CloseWrite preserves the half-close the server performs before hanging up on a bad request.
func (c *captureConn) CloseWrite() error {
	closer, ok := c.Conn.(interface{ CloseWrite() error })
	if !ok {
		return errors.ErrUnsupported
	}
	return closer.CloseWrite()
}

type captureState int

const (
	captureHead captureState = iota
	captureBody
	captureChunkSize
	captureChunkData
	captureChunkEnd
	captureTrailers
	captureStopped
)

// headerCapture follows one client connection and records the header names of every request that crosses it.
//
// Finding a request head means knowing where the previous request ended, so the scanner tracks body framing too.
// Anything it cannot follow stops the capture for that connection.
// Reporting no names is fine, since the guest falls back to the reconstruction, but reporting one request's names for another would not be.
type headerCapture struct {
	mu      sync.Mutex
	state   captureState
	buf     []byte // head or line bytes seen so far
	remain  int64  // body or chunk bytes left to skip
	pending [][]string
}

// feed consumes one read from the connection.
// The server reads on its own goroutine while a handler runs, so this races with claimOriginalHeaderNames.
func (c *headerCapture) feed(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for len(p) > 0 && c.state != captureStopped {
		switch c.state {
		case captureHead:
			p = c.feedHead(p)
		case captureBody, captureChunkData:
			p = c.skip(p)
		case captureChunkSize:
			p = c.feedChunkSize(p)
		case captureChunkEnd:
			p = c.feedChunkEnd(p)
		case captureTrailers:
			p = c.feedTrailers(p)
		}
	}
}

// claimOriginalHeaderNames hands a handler the oldest names nobody has claimed yet.
// Entries that cannot belong to r are dropped along the way, so the queue catches up instead of staying a request behind.
func (c *headerCapture) claimOriginalHeaderNames(r *http.Request) []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	for len(c.pending) > 0 {
		names := c.pending[0]
		c.pending = c.pending[1:]
		if capturedNamesMatch(names, r) {
			return names
		}
	}
	return nil
}

func (c *headerCapture) stop() []byte {
	c.state = captureStopped
	c.buf = nil
	return nil
}

// emptyBuf clears the buffer for whatever comes next, keeping it only while it stays a reasonable size.
func (c *headerCapture) emptyBuf() {
	if cap(c.buf) > maxRetainedBufferBytes {
		c.buf = nil
		return
	}
	c.buf = c.buf[:0]
}

func (c *headerCapture) feedHead(p []byte) []byte {
	if len(c.pending) >= maxCapturedHeads {
		return c.stop()
	}

	// The blank line closing the head can straddle two reads.
	scanFrom := max(len(c.buf)-3, 0)
	c.buf = append(c.buf, p...)
	if len(c.buf) > maxCapturedHeadBytes {
		return c.stop()
	}
	end := endOfHead(c.buf, scanFrom)
	if end < 0 {
		return nil
	}

	// The leftover has to outlive the buffer it came from.
	rest := bytes.Clone(c.buf[end:])
	names, framing, ok := parseRequestHead(c.buf[:end])
	if !ok {
		return c.stop()
	}
	c.pending = append(c.pending, names)
	c.emptyBuf()

	switch {
	case framing.tunnel:
		return c.stop()
	case framing.chunked:
		c.state = captureChunkSize
	case framing.length > 0:
		c.state = captureBody
		c.remain = framing.length
	default:
		c.state = captureHead
	}
	return rest
}

func (c *headerCapture) skip(p []byte) []byte {
	n := min(int64(len(p)), c.remain)
	c.remain -= n
	if c.remain == 0 {
		if c.state == captureChunkData {
			c.state = captureChunkEnd
		} else {
			c.state = captureHead
		}
	}
	return p[n:]
}

func (c *headerCapture) feedChunkSize(p []byte) []byte {
	line, rest, ok := c.feedLine(p)
	if !ok {
		return rest
	}
	if extension := strings.IndexByte(line, ';'); extension >= 0 {
		line = line[:extension]
	}
	size, err := strconv.ParseInt(strings.TrimSpace(line), 16, 64)
	if err != nil || size < 0 {
		return c.stop()
	}
	if size == 0 {
		c.state = captureTrailers
		return rest
	}
	c.state = captureChunkData
	c.remain = size
	return rest
}

func (c *headerCapture) feedChunkEnd(p []byte) []byte {
	line, rest, ok := c.feedLine(p)
	if !ok {
		return rest
	}
	if line != "" {
		return c.stop()
	}
	c.state = captureChunkSize
	return rest
}

func (c *headerCapture) feedTrailers(p []byte) []byte {
	line, rest, ok := c.feedLine(p)
	if !ok {
		return rest
	}
	if line == "" {
		c.state = captureHead
	}
	return rest
}

// feedLine buffers p until a line ends, then returns that line without its terminator along with whatever followed it.
// It reports false while the line is still incomplete.
func (c *headerCapture) feedLine(p []byte) (string, []byte, bool) {
	head, rest, found := bytes.Cut(p, []byte("\n"))
	c.buf = append(c.buf, head...)
	if len(c.buf) > maxCapturedLineBytes {
		c.stop()
		return "", nil, false
	}
	if !found {
		return "", nil, false
	}

	line := strings.TrimSuffix(string(c.buf), "\r")
	c.emptyBuf()
	return line, rest, true
}

// endOfHead returns the offset just past the blank line that closes a request head, or -1 while the head is incomplete.
// Bare newlines count as line endings, because Go's own parser accepts them.
func endOfHead(buf []byte, from int) int {
	for i := from; i < len(buf); i++ {
		if buf[i] != '\n' {
			continue
		}
		if i+1 < len(buf) && buf[i+1] == '\n' {
			return i + 2
		}
		if i+2 < len(buf) && buf[i+1] == '\r' && buf[i+2] == '\n' {
			return i + 3
		}
	}
	return -1
}

// requestFraming describes what follows a request head on the connection.
type requestFraming struct {
	length  int64
	chunked bool
	tunnel  bool // the connection stops carrying requests after this one
}

// parseRequestHead lists the header names of a request head in the order they appear, and works out how its body is framed.
// It reports false for anything that is not a plain HTTP/1 request, a stream that never was HTTP included.
func parseRequestHead(head []byte) ([]string, requestFraming, bool) {
	var framing requestFraming

	lines := strings.Split(string(head), "\n")
	method, version, ok := parseRequestLine(strings.TrimSuffix(lines[0], "\r"))
	if !ok {
		return nil, framing, false
	}

	var (
		names     = make([]string, 0, 16)
		lengths   []string
		encodings []string
		upgrade   bool
	)
	for _, line := range lines[1:] {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			break
		}
		if line[0] == ' ' || line[0] == '\t' {
			// A folded continuation belongs to the previous header.
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok || !validHTTPHeaderName([]byte(name)) {
			return nil, framing, false
		}
		names = append(names, name)

		switch {
		case strings.EqualFold(name, "content-length"):
			lengths = append(lengths, strings.TrimSpace(value))
		case strings.EqualFold(name, "transfer-encoding"):
			encodings = append(encodings, strings.TrimSpace(value))
		case strings.EqualFold(name, "upgrade"):
			upgrade = true
		}
	}

	if method == "CONNECT" || upgrade {
		framing.tunnel = true
		return names, framing, true
	}

	switch {
	case len(encodings) > 0:
		// Go rejects every request encoding but a lone chunked one, and ignores the header outright before HTTP/1.1.
		if version != "HTTP/1.1" || len(encodings) != 1 || !strings.EqualFold(encodings[0], "chunked") {
			return nil, framing, false
		}
		framing.chunked = true
	case len(lengths) > 0:
		// Go accepts a Content-Length repeated with the same value.
		for _, length := range lengths[1:] {
			if length != lengths[0] {
				return nil, framing, false
			}
		}
		length, err := strconv.ParseInt(lengths[0], 10, 64)
		if err != nil || length < 0 {
			return nil, framing, false
		}
		framing.length = length
	}
	return names, framing, true
}

// parseRequestLine reports the method and version of an HTTP/1 request line.
// Older versions frame their bodies differently, so the scanner treats them as unparsable.
func parseRequestLine(line string) (string, string, bool) {
	method, rest, ok := strings.Cut(line, " ")
	if !ok {
		return "", "", false
	}
	_, version, ok := strings.Cut(rest, " ")
	if !ok || version != "HTTP/1.1" && version != "HTTP/1.0" {
		return "", "", false
	}
	// Methods use the same token grammar as header names.
	if !validHTTPHeaderName([]byte(method)) {
		return "", "", false
	}
	return method, version, true
}

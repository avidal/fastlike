package fastlike

import (
	"bytes"
	"errors"
	"io"
	"maps"
	"net/http"
	"sync"
)

// Like production's streaming channel.
const (
	downstreamStreamSlots    = 8
	downstreamStreamChunkMax = 8 * 1024
)

var errUnfinishedStream = errors.New("streaming body not finished")

// sendDownstream lets the guest go on while the response is sent, like
// production.
func (i *Instance) sendDownstream(write func(w http.ResponseWriter) error) {
	done := make(chan struct{})
	i.ds_done = done
	w := i.ds_response
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				i.abilog.Printf("downstream response: PANIC: %v", r)
				i.ds_cutShort = true
			}
		}()
		if err := write(w); err != nil {
			i.abilog.Printf("downstream response cut short: %v", err)
			// Production would have sent this part already.
			flushResponse(w)
			i.ds_cutShort = true
		}
	}()
}

// finishDownstream reports whether the response was cut short.
// Like production, it drops what the guest left unfinished first, since the
// response may be waiting for it.
func (i *Instance) finishDownstream() bool {
	i.abandonUnfinishedBodies()
	if i.ds_done != nil {
		<-i.ds_done
	}
	return i.ds_cutShort
}

// Like hyper, production drops a body the response cannot carry without
// reading it.
func (i *Instance) downstreamHasBody(status int) bool {
	method := ""
	if i.ds_request != nil {
		method = i.ds_request.Method
	}
	return responseHasBody(method, status)
}

// sendEarlyHints forwards a 103 where production's h2o would.
// net/http keeps a 1xx's headers, so they are removed from the final response.
func (i *Instance) sendEarlyHints(headers http.Header) {
	if len(headers) == 0 || i.ds_request.ProtoMajor < 2 || i.ds_request.Header.Get("No-Early-Hints") == "1" {
		return
	}
	final := i.ds_response.Header().Clone()
	writeResponseHead(i.ds_response, http.StatusEarlyHints, headers)
	clear(i.ds_response.Header())
	maps.Copy(i.ds_response.Header(), final)
}

// writeResponseHead sends exactly the headers the guest set, without the
// Content-Type Go would sniff.
func writeResponseHead(w http.ResponseWriter, status int, headers http.Header) {
	for k, v := range headers {
		w.Header()[k] = v
	}
	if _, ok := headers["Content-Type"]; !ok {
		w.Header()["Content-Type"] = nil
	}
	w.WriteHeader(status)
}

// writeWholeResponse sends a response whose body the guest does not stream.
func writeWholeResponse(w http.ResponseWriter, status int, headers http.Header, hasBody bool, body io.Reader, trailers func() http.Header) error {
	writeResponseHead(w, status, headers)
	if !hasBody {
		return nil
	}
	if _, err := io.Copy(w, body); err != nil {
		return err
	}
	addTrailers(w, trailers())
	return nil
}

func addTrailers(w http.ResponseWriter, trailers http.Header) {
	for name, values := range trailers {
		key := http.TrailerPrefix + http.CanonicalHeaderKey(name)
		w.Header()[key] = append(w.Header()[key], values...)
	}
}

func flushResponse(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// flushingWriter flushes every write, as production streams each chunk.
type flushingWriter struct {
	http.ResponseWriter
}

func (w flushingWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	flushResponse(w.ResponseWriter)
	return n, err
}

func closeReader(r io.Reader) {
	if closer, ok := r.(io.Closer); ok {
		_ = closer.Close()
	}
}

// downstreamStream queues what the guest streams to the client.
type downstreamStream struct {
	mu       sync.Mutex
	queue    []io.Reader
	changed  chan struct{}
	finished bool
	stopped  bool

	// guest's trailers go out once the body is finished.
	guest *BodyHandle
}

func newDownstreamStream(held io.Reader, guest *BodyHandle) *downstreamStream {
	return &downstreamStream{queue: []io.Reader{held}, guest: guest}
}

func (s *downstreamStream) notifyLocked() {
	if s.changed != nil {
		close(s.changed)
		s.changed = nil
	}
}

func (s *downstreamStream) changedLocked() <-chan struct{} {
	if s.changed == nil {
		s.changed = make(chan struct{})
	}
	return s.changed
}

// waitLocked releases the lock while it waits for a change.
func (s *downstreamStream) waitLocked() {
	changed := s.changedLocked()
	s.mu.Unlock()
	<-changed
	s.mu.Lock()
}

// enqueue closes r if the stream stopped.
func (s *downstreamStream) enqueue(r io.Reader) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.queue) >= downstreamStreamSlots && !s.stopped {
		s.waitLocked()
	}
	if s.stopped {
		closeReader(r)
		return io.ErrClosedPipe
	}
	s.queue = append(s.queue, r)
	s.notifyLocked()
	return nil
}

func (s *downstreamStream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	p = p[:min(len(p), downstreamStreamChunkMax)]
	if err := s.enqueue(bytes.NewReader(bytes.Clone(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *downstreamStream) Append(src io.Reader) error {
	return s.enqueue(src)
}

// Close finishes the body after what is queued.
func (s *downstreamStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = true
	s.notifyLocked()
	return nil
}

// Abandon cuts the response short after what is queued.
func (s *downstreamStream) Abandon() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	s.notifyLocked()
	return nil
}

// drop makes the guest's writes fail instead of waiting, once the response
// takes no more.
func (s *downstreamStream) drop() {
	s.mu.Lock()
	queued := s.queue
	s.queue = nil
	s.stopped = true
	s.notifyLocked()
	s.mu.Unlock()
	for _, r := range queued {
		closeReader(r)
	}
}

// next returns nil once the guest finished the body.
func (s *downstreamStream) next() (io.Reader, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		switch {
		case len(s.queue) > 0:
			r := s.queue[0]
			s.queue[0] = nil
			s.queue = s.queue[1:]
			s.notifyLocked()
			return r, nil
		case s.finished:
			return nil, nil
		case s.stopped:
			return nil, errUnfinishedStream
		}
		s.waitLocked()
	}
}

func (s *downstreamStream) readyChannel() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) < downstreamStreamSlots || s.stopped {
		return closedStreamingReadyChannel()
	}
	return s.changedLocked()
}

// send drops the stream of a response without a body, like production
// drops the body.
func (s *downstreamStream) send(w http.ResponseWriter, status int, headers http.Header, hasBody bool) error {
	defer s.drop()
	writeResponseHead(w, status, headers)
	flushResponse(w)
	if !hasBody {
		return nil
	}
	buf := make([]byte, 32*1024)
	for {
		part, err := s.next()
		if err != nil {
			return err
		}
		if part == nil {
			break
		}
		if err := sendStreamPart(w, part, buf); err != nil {
			return err
		}
	}
	addTrailers(w, s.guest.currentTrailers())
	return nil
}

func sendStreamPart(w http.ResponseWriter, part io.Reader, buf []byte) error {
	defer closeReader(part)
	_, err := io.CopyBuffer(flushingWriter{w}, part, buf)
	return err
}

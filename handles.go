package fastlike

import (
	"bytes"
	"net/http"
)

type fastlyMeta struct{}

// RequestHandle is an http.Request with extra metadata
// Notably, the request body is ignored and instead the guest will provide a BodyHandle to use
type RequestHandle struct {
	*http.Request
	fastlyMeta *fastlyMeta

	// It is an error to try sending a request without an associated body handle
	hasBody bool
}

type RequestHandles struct {
	handles []*RequestHandle
}

func (rhs *RequestHandles) Get(id int) *RequestHandle {
	if id >= len(rhs.handles) {
		return nil
	}

	return rhs.handles[id]
}
func (rhs *RequestHandles) New() (int, *RequestHandle) {
	rh := &RequestHandle{Request: &http.Request{}}
	rhs.handles = append(rhs.handles, rh)
	return len(rhs.handles) - 1, rh
}

// ResponseHandle is an http.Response with extra metadata
// Notably, the response body is ignored and instead the guest will provide a BodyHandle to use
type ResponseHandle struct {
	*http.Response

	// It is an error to try sending a response without an associated body handle
	hasBody bool
}

type ResponseHandles struct {
	handles []*ResponseHandle
}

func (rhs *ResponseHandles) Get(id int) *ResponseHandle {
	if id >= len(rhs.handles) {
		return nil
	}

	return rhs.handles[id]
}
func (rhs *ResponseHandles) New() (int, *ResponseHandle) {
	rh := &ResponseHandle{Response: &http.Response{}}
	rhs.handles = append(rhs.handles, rh)
	return len(rhs.handles) - 1, rh
}

// BodyHandle is a bytes.Buffer..and that's about it.
// TODO: Should this have flags for readable/writable? Need more deep-diving on the ABI to see how
// exactly bodies are treated on the guest. We may be able to say that a BodyHandle is an
// io.ReadWriteCloser? Might just defer until if/when we implement streaming.
type BodyHandle struct {
	*bytes.Buffer
}

// Close implements io.Closer for a BodyHandle
func (b *BodyHandle) Close() error {
	return nil
}

type BodyHandles struct {
	handles []*BodyHandle
}

func (bhs *BodyHandles) Get(id int) *BodyHandle {
	if id >= len(bhs.handles) {
		return nil
	}

	return bhs.handles[id]
}
func (bhs *BodyHandles) New() (int, *BodyHandle) {
	bh := &BodyHandle{new(bytes.Buffer)}
	bhs.handles = append(bhs.handles, bh)
	return len(bhs.handles) - 1, bh
}

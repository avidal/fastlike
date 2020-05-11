package fastlike

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
)

type HttpVersion int32

// These line up with the fastly ABI
const (
	Http09 HttpVersion = 0
	Http10 HttpVersion = 1
	Http11 HttpVersion = 2
	Http2  HttpVersion = 3
	Http3  HttpVersion = 4
)

type fastlyMeta struct{}

type requestHandle struct {
	version HttpVersion
	method  string
	url     *url.URL
	headers http.Header

	fastlyMeta *fastlyMeta
}

type bodyHandle = bytes.Buffer

type responseHandle struct {
	version HttpVersion
	status  int
	headers http.Header
}

// RequestHandle is an http.Request with extra metadata
// Notably, the request body is ignored and instead the guest will provide a BodyHandle to use
type RequestHandle struct {
	r          *http.Request
	fastlyMeta *fastlyMeta

	// It is an error to try sending a request without an associated body handle
	hasBody bool
}

func (h *RequestHandle) SetVersion(version HttpVersion) {
	if version != Http11 {
		fmt.Printf("WARN: Ignoring version set %d\n", version)
	}
}

func (h *RequestHandle) GetVersion() HttpVersion {
	return Http11
}

// ResponseHandle is an http.Response with extra metadata
// Notably, the response body is ignored and instead the guest will provide a BodyHandle to use
type ResponseHandle struct {
	w *http.Response

	// It is an error to try sending a response without an associated body handle
	hasBody bool
}

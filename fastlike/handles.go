package fastlike

import (
	"bytes"
	"net/http"
	"net/url"
)

type httpVersion int32

// These line up with the fastly ABI
const (
	http09 httpVersion = 0
	http10 httpVersion = 1
	http11 httpVersion = 2
	http2  httpVersion = 3
	http3  httpVersion = 4
)

type fastlyMeta struct{}

type requestHandle struct {
	version httpVersion
	method  string
	url     *url.URL
	headers http.Header

	fastlyMeta *fastlyMeta
}

type bodyHandle = bytes.Buffer

type responseHandle struct {
	version httpVersion
	status  int
	headers http.Header
}

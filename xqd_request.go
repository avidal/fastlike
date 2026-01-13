package fastlike

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"time"
)

// SendErrorDetail represents the error details structure for send_v2/send_v3
// This matches the C struct layout expected by the guest
type SendErrorDetail struct {
	Tag           uint32
	Mask          uint32
	DnsErrorRcode uint16
	DnsErrorInfo  uint16
	TlsAlertId    uint8
	_             [3]uint8 // Padding to maintain alignment
}

// writeSendErrorDetail writes a SendErrorDetail struct to guest memory
func (i *Instance) writeSendErrorDetail(addr int32, detail *SendErrorDetail) error {
	// Write tag (4 bytes)
	i.memory.PutUint32(detail.Tag, int64(addr))

	// Write mask (4 bytes)
	i.memory.PutUint32(detail.Mask, int64(addr+4))

	// Write dns_error_rcode (2 bytes)
	i.memory.PutUint16(uint16(detail.DnsErrorRcode), int64(addr+8))

	// Write dns_error_info_code (2 bytes)
	i.memory.PutUint16(uint16(detail.DnsErrorInfo), int64(addr+10))

	// Write tls_alert_id (1 byte)
	i.memory.PutUint8(uint8(detail.TlsAlertId), int64(addr+12))

	// Padding bytes (3 bytes) are left as-is

	return nil
}

// createErrorDetailFromError converts a Go error to a SendErrorDetail struct.
// Returns SendErrorDetailOk if err is nil, otherwise returns a specific error type
// based on parsing the error (DNS errors, TLS errors, connection errors, etc.).
func createErrorDetailFromError(err error) *SendErrorDetail {
	if err == nil {
		return &SendErrorDetail{
			Tag:  SendErrorDetailOk,
			Mask: 0,
		}
	}

	// Parse the error to determine the specific error type
	errStr := err.Error()

	// Check for DNS errors using type assertion
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsTimeout {
			return &SendErrorDetail{
				Tag:  SendErrorDetailDnsTimeout,
				Mask: 0,
			}
		}
		return &SendErrorDetail{
			Tag:  SendErrorDetailDnsError,
			Mask: 0,
		}
	}

	// Check for connection refused
	if strings.Contains(errStr, "connection refused") {
		return &SendErrorDetail{
			Tag:  SendErrorDetailConnectionRefused,
			Mask: 0,
		}
	}

	// Check for connection reset or terminated
	if strings.Contains(errStr, "connection reset") || strings.Contains(errStr, "broken pipe") {
		return &SendErrorDetail{
			Tag:  SendErrorDetailConnectionTerminated,
			Mask: 0,
		}
	}

	// Check for timeout errors (i/o timeout, deadline exceeded, context canceled)
	if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded") || strings.Contains(errStr, "context deadline") {
		return &SendErrorDetail{
			Tag:  SendErrorDetailConnectionTimeout,
			Mask: 0,
		}
	}

	// Check for TLS certificate errors
	if strings.Contains(errStr, "certificate") || strings.Contains(errStr, "x509") {
		return &SendErrorDetail{
			Tag:  SendErrorDetailTlsCertificateError,
			Mask: 0,
		}
	}

	// Check for TLS protocol errors
	if strings.Contains(errStr, "tls:") || strings.Contains(errStr, "TLS") {
		return &SendErrorDetail{
			Tag:  SendErrorDetailTlsProtocolError,
			Mask: 0,
		}
	}

	// Check for "no route to host" or unreachable destination
	if strings.Contains(errStr, "no route to host") || strings.Contains(errStr, "unreachable") {
		return &SendErrorDetail{
			Tag:  SendErrorDetailDestinationIpUnroutable,
			Mask: 0,
		}
	}

	// Check for "no such host" errors
	if strings.Contains(errStr, "no such host") {
		return &SendErrorDetail{
			Tag:  SendErrorDetailDestinationNotFound,
			Mask: 0,
		}
	}

	// Return generic internal error for unrecognized errors
	return &SendErrorDetail{
		Tag:  SendErrorDetailInternalError,
		Mask: 0,
	}
}

// applyAutoDecompression checks if the response should be auto-decompressed and decompresses if needed.
// This modifies the response in-place, replacing the body with decompressed content and removing
// Content-Encoding and Content-Length headers. Currently only supports gzip/x-gzip encoding.
// Returns an error if decompression fails (but still modifies the response to remove encoding headers).
func applyAutoDecompression(resp *http.Response, autoDecompressEncodings uint32) error {
	// Check if gzip auto-decompression is enabled in the bitfield
	if (autoDecompressEncodings & ContentEncodingsGzip) == 0 {
		return nil // Auto-decompression not enabled for gzip
	}

	// Check if the response is gzip-encoded (supports both "gzip" and "x-gzip")
	encoding := resp.Header.Get("Content-Encoding")
	if encoding != "gzip" && encoding != "x-gzip" {
		return nil // Not gzip-encoded
	}

	// Read the compressed body
	compressedBody, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return err
	}

	// Create a gzip reader to decompress the body
	gzReader, err := gzip.NewReader(bytes.NewReader(compressedBody))
	if err != nil {
		// If we can't create a gzip reader, restore the original body but remove headers
		// This ensures the guest sees the raw compressed data without encoding indicators
		resp.Body = io.NopCloser(bytes.NewReader(compressedBody))
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Content-Length")
		return err
	}
	defer func() { _ = gzReader.Close() }()

	decompressedBody, err := io.ReadAll(gzReader)
	if err != nil {
		// If decompression fails, restore the original body but remove headers
		resp.Body = io.NopCloser(bytes.NewReader(compressedBody))
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Content-Length")
		return err
	}

	// Replace the response body with the decompressed version
	resp.Body = io.NopCloser(bytes.NewReader(decompressedBody))

	// Remove Content-Encoding and Content-Length headers (they're now invalid)
	// The guest will see uncompressed data without encoding indicators
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")

	return nil
}

// xqd_req_version_get retrieves the HTTP protocol version for the request.
// Returns XqdErrInvalidHandle if the handle is invalid, XqdStatusOK on success.
func (i *Instance) xqd_req_version_get(handle int32, version_out int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		i.abilog.Printf("req_version_get: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("req_version_get: handle=%d version=%d", handle, r.version)
	i.memory.PutUint32(uint32(r.version), int64(version_out))
	return XqdStatusOK
}

// xqd_req_version_set sets the HTTP protocol version for the request.
// Only HTTP/0.9, HTTP/1.0, and HTTP/1.1 are supported.
// Returns XqdErrInvalidHandle, XqdErrInvalidArgument, or XqdStatusOK.
func (i *Instance) xqd_req_version_set(handle int32, version int32) int32 {
	i.abilog.Printf("req_version_set: handle=%d version=%d", handle, version)

	r := i.requests.Get(int(handle))
	if r == nil {
		i.abilog.Printf("req_version_set: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	// Validate that the version is one of the supported HTTP versions
	if version != Http09 && version != Http10 && version != Http11 {
		i.abilog.Printf("req_version_set: invalid version %d", version)
		return XqdErrInvalidArgument
	}

	// Store the version in the request handle
	r.version = version
	return XqdStatusOK
}

// xqd_req_cache_override_set sets cache override parameters for the request.
// This is a no-op for local testing since we don't implement caching.
// Returns XqdErrInvalidHandle if the handle is invalid, XqdStatusOK otherwise.
func (i *Instance) xqd_req_cache_override_set(handle int32, tag int32, ttl int32, swr int32) int32 {
	// We don't actually *do* anything with cache overrides, since we don't have or need a cache.

	if i.requests.Get(int(handle)) == nil {
		i.abilog.Printf("req_cache_override_set: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	return XqdStatusOK
}

// xqd_req_cache_override_v2_set sets cache override parameters including surrogate keys.
// This is a no-op for local testing since we don't implement caching.
// Returns XqdErrInvalidHandle if the handle is invalid, XqdStatusOK otherwise.
func (i *Instance) xqd_req_cache_override_v2_set(handle int32, tag int32, ttl int32, swr int32, sk int32, sk_len int32) int32 {
	// We don't actually *do* anything with cache overrides, since we don't have or need a cache.

	if i.requests.Get(int(handle)) == nil {
		i.abilog.Printf("req_cache_override_v2_set: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	return XqdStatusOK
}

// xqd_req_method_get retrieves the HTTP method for the request.
// Writes the method string to guest memory at addr and the number of bytes written to nwritten_out.
// Returns XqdErrInvalidHandle if the handle is invalid, XqdErrBufferLength if buffer is too small, or XqdStatusOK on success.
func (i *Instance) xqd_req_method_get(handle int32, addr int32, maxlen int32, nwritten_out int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		i.abilog.Printf("req_method_get: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	if int(maxlen) < len(r.Method) {
		return XqdErrBufferLength
	}

	i.abilog.Printf("req_method_get: handle=%d method=%q", handle, r.Method)

	nwritten, err := i.memory.WriteAt([]byte(r.Method), int64(addr))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	return XqdStatusOK
}

// xqd_req_method_set sets the HTTP method for the request.
// Reads the method string from guest memory at addr and validates it against standard HTTP methods.
// Returns XqdErrInvalidHandle if handle is invalid, XqdErrHttpParse if method is invalid, or XqdStatusOK on success.
func (i *Instance) xqd_req_method_set(handle int32, addr int32, size int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	method := make([]byte, size)
	_, err := i.memory.ReadAt(method, int64(addr))
	if err != nil {
		return XqdError
	}

	// Validate that the method is one of the standard HTTP methods (case-insensitive)
	// We use null-terminated concatenation as a simple string set for validation
	validMethods := strings.Join([]string{
		http.MethodGet,
		http.MethodHead,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodConnect,
		http.MethodOptions,
		http.MethodTrace,
	}, "\x00")

	methodUpper := strings.ToUpper(string(method))
	if !strings.Contains(validMethods, methodUpper) {
		i.abilog.Printf("req_method_set: invalid method=%q", method)
		return XqdErrHttpParse
	}

	i.abilog.Printf("req_method_set: handle=%d method=%q", handle, method)

	// Store the method in uppercase (HTTP methods are case-sensitive and should be uppercase)
	r.Method = methodUpper
	return XqdStatusOK
}

// xqd_req_uri_set sets the URI for the request.
// Reads the URI string from guest memory at addr, parses it, and assigns it to the request.
// Returns XqdErrInvalidHandle if handle is invalid, XqdError if parsing fails, or XqdStatusOK on success.
func (i *Instance) xqd_req_uri_set(handle int32, addr int32, size int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	buf := make([]byte, size)
	nread, err := i.memory.ReadAt(buf, int64(addr))
	if err != nil {
		return XqdError
	}
	if nread < int(size) {
		return XqdError
	}

	u, err := url.Parse(string(buf))
	if err != nil {
		i.abilog.Printf("req_uri_set: parse error uri=%q got=%s", buf, err.Error())
		return XqdError
	}

	i.abilog.Printf("req_uri_set: handle=%d uri=%q", handle, u)

	r.URL = u
	return XqdStatusOK
}

// xqd_req_header_names_get retrieves all header names for the request.
// Uses cursor-based pagination to support large header sets. Header names are sorted alphabetically.
// Returns XqdErrInvalidHandle if handle is invalid, XqdErrBufferLength if buffer is too small, or XqdStatusOK on success.
func (i *Instance) xqd_req_header_names_get(handle int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
	i.abilog.Printf("req_header_names_get: handle=%d cursor=%d", handle, cursor)

	r := i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	// Collect all header names from the request
	names := []string{}
	for headerName := range r.Header {
		names = append(names, headerName)
	}

	// Sort names alphabetically for consistent ordering (Go's map iteration is non-deterministic)
	sort.Strings(names)

	return xqd_multivalue(i.memory, names, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

// xqd_req_header_remove removes a header from the request.
// Reads the header name from guest memory and deletes it from the request headers.
// Returns XqdErrInvalidHandle if handle is invalid, XqdErrInvalidArgument if header name is too long or not found, or XqdStatusOK on success.
func (i *Instance) xqd_req_header_remove(handle int32, name_addr int32, name_size int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	// Validate header name length (MAX_HEADER_NAME_LEN = 65535)
	if name_size > 65535 {
		i.abilog.Printf("req_header_remove: header name too long: %d bytes (max 65535)\n", name_size)
		return XqdErrInvalidArgument
	}

	name := make([]byte, name_size)
	_, err := i.memory.ReadAt(name, int64(name_addr))
	if err != nil {
		return XqdError
	}

	header := http.CanonicalHeaderKey(string(name))

	// Check if the header exists before removing
	if r.Header.Get(header) == "" {
		i.abilog.Printf("req_header_remove: header %q not found\n", header)
		return XqdErrInvalidArgument
	}

	r.Header.Del(header)

	return XqdStatusOK
}

// xqd_req_header_insert sets a header value, replacing any existing values.
// Reads the header name and value from guest memory and sets the header using http.Header.Set.
// Returns XqdErrInvalidHandle if handle is invalid, XqdErrInvalidArgument if header name is too long, or XqdStatusOK on success.
func (i *Instance) xqd_req_header_insert(handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	// Validate header name length (MAX_HEADER_NAME_LEN = 65535)
	if name_size > 65535 {
		i.abilog.Printf("req_header_insert: header name too long: %d bytes (max 65535)\n", name_size)
		return XqdErrInvalidArgument
	}

	name := make([]byte, name_size)
	_, err := i.memory.ReadAt(name, int64(name_addr))
	if err != nil {
		return XqdError
	}

	value := make([]byte, value_size)
	_, err = i.memory.ReadAt(value, int64(value_addr))
	if err != nil {
		return XqdError
	}

	header := http.CanonicalHeaderKey(string(name))

	i.abilog.Printf("req_header_insert: handle=%d header=%q value=%q", handle, header, string(value))

	if r.Header == nil {
		r.Header = http.Header{}
	}

	r.Header.Set(header, string(value))

	return XqdStatusOK
}

// xqd_req_header_append appends a header value to existing values.
// Reads the header name and value from guest memory and adds the value using http.Header.Add.
// Returns XqdErrInvalidHandle if handle is invalid, XqdErrInvalidArgument if header name is too long, or XqdStatusOK on success.
func (i *Instance) xqd_req_header_append(handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	// Validate header name length (MAX_HEADER_NAME_LEN = 65535)
	if name_size > 65535 {
		i.abilog.Printf("req_header_append: header name too long: %d bytes (max 65535)\n", name_size)
		return XqdErrInvalidArgument
	}

	name := make([]byte, name_size)
	_, err := i.memory.ReadAt(name, int64(name_addr))
	if err != nil {
		return XqdError
	}

	value := make([]byte, value_size)
	_, err = i.memory.ReadAt(value, int64(value_addr))
	if err != nil {
		return XqdError
	}

	header := http.CanonicalHeaderKey(string(name))

	i.abilog.Printf("req_header_append: handle=%d header=%q value=%q", handle, header, string(value))

	if r.Header == nil {
		r.Header = http.Header{}
	}

	r.Header.Add(header, string(value))

	return XqdStatusOK
}

// xqd_req_header_value_get retrieves the first value of a header.
// Reads the header name from guest memory and writes the first value to addr.
// Returns XqdErrInvalidHandle if handle is invalid, XqdErrInvalidArgument if header name is too long or not found, XqdErrBufferLength if buffer is too small, or XqdStatusOK on success.
func (i *Instance) xqd_req_header_value_get(handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, nwritten_out int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	// Validate header name length (MAX_HEADER_NAME_LEN = 65535)
	if name_size > 65535 {
		i.abilog.Printf("req_header_value_get: header name too long: %d bytes (max 65535)\n", name_size)
		i.memory.PutUint32(0, int64(nwritten_out))
		return XqdErrInvalidArgument
	}

	buf := make([]byte, name_size)
	_, err := i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	header := http.CanonicalHeaderKey(string(buf))

	i.abilog.Printf("req_header_value_get: handle=%d header=%q\n", handle, header)

	// Check if header exists (Go's Get() returns "" for both missing and empty values)
	values, exists := r.Header[header]
	if !exists || len(values) == 0 {
		i.abilog.Printf("req_header_value_get: header %q not found\n", header)
		i.memory.PutUint32(0, int64(nwritten_out))
		return XqdErrInvalidArgument
	}

	value := values[0]

	// Always write the length needed
	i.memory.PutUint32(uint32(len(value)), int64(nwritten_out))

	// Check if buffer is large enough
	if int(maxlen) < len(value) {
		return XqdErrBufferLength
	}

	// Write the value to memory
	_, err = i.memory.WriteAt([]byte(value), int64(addr))
	if err != nil {
		return XqdError
	}

	return XqdStatusOK
}

// xqd_req_header_values_get retrieves all values for a specific header.
// Uses cursor-based pagination to support headers with multiple values. Values are sorted alphabetically.
// Returns XqdErrInvalidHandle if handle is invalid, XqdErrBufferLength if buffer is too small, or XqdStatusOK on success.
func (i *Instance) xqd_req_header_values_get(handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	buf := make([]byte, name_size)
	_, err := i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	header := http.CanonicalHeaderKey(string(buf))

	i.abilog.Printf("req_header_values_get: handle=%d header=%q cursor=%d\n", handle, header, cursor)

	values, ok := r.Header[header]
	if !ok {
		values = []string{}
	}

	// Sort the values otherwise cursors don't work
	sort.Strings(values[:])

	return xqd_multivalue(i.memory, values, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

// xqd_req_header_values_set sets multiple values for a header.
// Reads null-terminated values from guest memory and adds them all to the specified header.
// The values in memory are formatted as: "value1\0value2\0value3\0"
// Returns XqdErrInvalidHandle if handle is invalid, XqdError on memory read failure, or XqdStatusOK on success.
func (i *Instance) xqd_req_header_values_set(handle int32, name_addr int32, name_size int32, values_addr int32, values_size int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	// Read the header name
	buf := make([]byte, name_size)
	_, err := i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	header := http.CanonicalHeaderKey(string(buf))

	// Read the null-terminated values list
	// We read (values_size - 1) bytes to exclude the trailing null terminator
	buf = make([]byte, values_size-1)
	_, err = i.memory.ReadAt(buf, int64(values_addr))
	if err != nil {
		return XqdError
	}

	// Split on null bytes to get individual values
	values := bytes.Split(buf, []byte("\x00"))

	i.abilog.Printf("req_header_values_set: handle=%d header=%q values=%q\n", handle, header, values)

	if r.Header == nil {
		r.Header = http.Header{}
	}

	for _, v := range values {
		r.Header.Add(header, string(v))
	}

	return XqdStatusOK
}

// xqd_req_uri_get retrieves the full URI for the request.
// Writes the URI string to guest memory at addr and the number of bytes written to nwritten_out.
// Returns XqdErrInvalidHandle if handle is invalid, XqdErrBufferLength if buffer is too small, or XqdStatusOK on success.
func (i *Instance) xqd_req_uri_get(handle int32, addr int32, maxlen int32, nwritten_out int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	uri := r.URL.String()
	i.abilog.Printf("req_uri_get: handle=%d uri=%q", handle, uri)

	// Always write the length needed
	i.memory.PutUint32(uint32(len(uri)), int64(nwritten_out))

	// Check if buffer is large enough
	if int(maxlen) < len(uri) {
		return XqdErrBufferLength
	}

	// Write the URI to memory
	_, err := i.memory.WriteAt([]byte(uri), int64(addr))
	if err != nil {
		return XqdError
	}

	return XqdStatusOK
}

// xqd_req_new creates a new request handle.
// Allocates a new request handle and writes its ID to guest memory at handle_out.
// Always returns XqdStatusOK.
func (i *Instance) xqd_req_new(handle_out int32) int32 {
	rhid, _ := i.requests.New()
	i.abilog.Printf("req_new: handle=%d", rhid)
	i.memory.PutUint32(uint32(rhid), int64(handle_out))
	return XqdStatusOK
}

// xqd_req_send sends a synchronous HTTP request to a backend.
// Blocks until the backend responds, then writes response and body handles to guest memory.
// Automatically adds cdn-loop header for loop detection and applies auto-decompression if configured.
// Returns XqdErrInvalidHandle if handles are invalid, XqdErrHttpUserInvalid if URL is not set, or XqdStatusOK on success.
func (i *Instance) xqd_req_send(rhandle int32, bhandle int32, backend_addr, backend_size int32, wh_out int32, bh_out int32) int32 {
	// sends the request described by (rh, bh) to the backend
	// expects a response handle and response body handle
	r := i.requests.Get(int(rhandle))
	if r == nil {
		i.abilog.Printf("req_send: invalid request handle=%d", rhandle)
		return XqdErrInvalidHandle
	}

	b := i.bodies.Get(int(bhandle))
	if b == nil {
		i.abilog.Printf("req_send: invalid body handle=%d", bhandle)
		return XqdErrInvalidHandle
	}

	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backend := string(buf)

	// Check if URL is set
	if r.URL == nil {
		i.abilog.Printf("req_send: URL not set for request handle %d", rhandle)
		return XqdErrHttpUserInvalid
	}

	i.abilog.Printf("req_send: handle=%d body=%d backend=%q uri=%q", rhandle, bhandle, backend, r.URL)

	req, err := http.NewRequestWithContext(i.ds_context, r.Method, r.URL.String(), b)
	if err != nil {
		return XqdErrHttpUserInvalid
	}

	req.Header = r.Header.Clone()

	// Ensure headers are initialized
	if req.Header == nil {
		req.Header = http.Header{}
	}

	// Add a CDN-Loop header for loop detection (checked at ingress to prevent infinite loops)
	req.Header.Add("cdn-loop", "fastlike")

	// Apply framing mode - validate and potentially filter framing headers
	effectiveMode := validateAndApplyFramingMode(req.Header, r.framingHeadersMode, func(format string, args ...interface{}) {
		i.abilog.Printf("req_send: "+format, args...)
	})

	// Set Content-Length only in automatic mode (when framing headers are managed by the library)
	if effectiveMode == FramingHeadersModeAutomatic {
		// Set Content-Length if not already set or if ContentLength field is uninitialized
		// This ensures the backend receives proper content length information
		if req.Header.Get("content-length") == "" {
			req.Header.Add("content-length", fmt.Sprintf("%d", b.Size()))
			req.ContentLength = b.Size()
		} else if req.ContentLength <= 0 {
			req.ContentLength = b.Size()
		}
	} else {
		// Manual mode: use the Content-Length from headers if present
		if cl := req.Header.Get("Content-Length"); cl != "" {
			var contentLength int64
			fmt.Sscanf(cl, "%d", &contentLength)
			req.ContentLength = contentLength
		}
	}

	// If the backend is geolocation, we select the geobackend explicitly
	var handler http.Handler
	if backend == "geolocation" {
		handler = geoHandler(i.geolookup)
	} else {
		handler = i.getBackendHandler(backend)
	}

	// Use httptest.ResponseRecorder to capture the response from the handler
	// This provides an http.ResponseWriter interface and allows us to extract the *http.Response
	// NOTE: The Handler interface is useful for embedders who want to process requests locally
	// or easily adapt to an http.RoundTripper for external requests
	wr := httptest.NewRecorder()

	// Pause CPU time tracking during the blocking HTTP request
	i.pauseExecution()

	// Run the handler in a goroutine so we can monitor context cancellation
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(wr, req)
		close(done)
	}()

	// Wait for either the request to complete or context to be cancelled
	if i.ds_context != nil {
		select {
		case <-done:
			// Request completed normally
			i.resumeExecution()
		case <-i.ds_context.Done():
			// Context cancelled during request - return error and let epoch cause trap
			i.abilog.Printf("req_send: context cancelled during request")
			i.resumeExecution()
			// Wait a bit for the handler to finish (best effort cleanup)
			select {
			case <-done:
			case <-time.After(10 * time.Millisecond):
			}
			// Return error - wasm will trap on epoch when it continues executing
			return XqdError
		}
	} else {
		// No context, just wait for completion
		<-done
		i.resumeExecution()
	}

	w := wr.Result()

	// Apply auto-decompression if enabled
	_ = applyAutoDecompression(w, r.autoDecompressEncodings)

	// Convert the response into an (rh, bh) pair, put them in the list, and write out the handles
	whid, wh := i.responses.New()
	wh.Status = w.Status
	wh.StatusCode = w.StatusCode
	wh.Header = w.Header.Clone()
	wh.Body = w.Body

	bhid, _ := i.bodies.NewReader(wh.Body)

	i.abilog.Printf("req_send: response handle=%d body=%d", whid, bhid)

	i.memory.PutUint32(uint32(whid), int64(wh_out))
	i.memory.PutUint32(uint32(bhid), int64(bh_out))

	return XqdStatusOK
}

// xqd_req_close marks a request handle to close the connection after use.
// Sets the Close flag on the request, indicating connection should not be reused.
// Returns XqdErrInvalidHandle if handle is invalid, or XqdStatusOK on success.
func (i *Instance) xqd_req_close(handle int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		i.abilog.Printf("req_close: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("req_close: handle=%d", handle)
	r.Close = true
	return XqdStatusOK
}

// xqd_req_send_async sends an asynchronous HTTP request to a backend.
// Initiates the request in a goroutine and immediately returns a pending request handle.
// The guest can poll or wait on the pending handle to retrieve the response later.
// Returns XqdErrInvalidHandle if handles are invalid, XqdErrHttpUserInvalid if URL is not set, or XqdStatusOK on success.
func (i *Instance) xqd_req_send_async(rhandle int32, bhandle int32, backend_addr, backend_size int32, ph_out int32) int32 {
	// Validate request handle
	r := i.requests.Get(int(rhandle))
	if r == nil {
		i.abilog.Printf("req_send_async: invalid request handle=%d", rhandle)
		return XqdErrInvalidHandle
	}

	// Validate body handle
	b := i.bodies.Get(int(bhandle))
	if b == nil {
		i.abilog.Printf("req_send_async: invalid body handle=%d", bhandle)
		return XqdErrInvalidHandle
	}

	// Read backend name
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}
	backend := string(buf)

	// Check if URL is set
	if r.URL == nil {
		i.abilog.Printf("req_send_async: URL not set for request handle %d", rhandle)
		return XqdErrHttpUserInvalid
	}

	i.abilog.Printf("req_send_async: handle=%d body=%d backend=%q uri=%q", rhandle, bhandle, backend, r.URL)

	// Create a pending request handle
	phid, pendingReq := i.pendingRequests.New()

	// Launch goroutine to perform the request asynchronously
	go func(ctx context.Context, req *http.Request, body *BodyHandle, backendName string, pr *PendingRequest, autoDecompress uint32, framingMode FramingHeadersMode) {
		// Build the request
		httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), body)
		if err != nil {
			pr.Complete(nil, err)
			return
		}

		httpReq.Header = req.Header.Clone()
		if httpReq.Header == nil {
			httpReq.Header = http.Header{}
		}

		// Add CDN-Loop header for loop detection
		httpReq.Header.Add("cdn-loop", "fastlike")

		// Apply framing mode - validate and potentially filter framing headers
		effectiveMode := validateAndApplyFramingMode(httpReq.Header, framingMode, nil)

		// Set Content-Length only in automatic mode
		if effectiveMode == FramingHeadersModeAutomatic {
			if httpReq.Header.Get("content-length") == "" {
				httpReq.Header.Add("content-length", fmt.Sprintf("%d", body.Size()))
				httpReq.ContentLength = body.Size()
			} else if httpReq.ContentLength <= 0 {
				httpReq.ContentLength = body.Size()
			}
		} else {
			// Manual mode: use the Content-Length from headers if present
			if cl := httpReq.Header.Get("Content-Length"); cl != "" {
				var contentLength int64
				fmt.Sscanf(cl, "%d", &contentLength)
				httpReq.ContentLength = contentLength
			}
		}

		// Get the backend handler
		var handler http.Handler
		if backendName == "geolocation" {
			handler = geoHandler(i.geolookup)
		} else {
			handler = i.getBackendHandler(backendName)
		}

		// Execute the request
		wr := httptest.NewRecorder()
		handler.ServeHTTP(wr, httpReq)
		resp := wr.Result()

		// Apply auto-decompression if enabled
		_ = applyAutoDecompression(resp, autoDecompress)

		// Mark the pending request as complete
		pr.Complete(resp, nil)
	}(i.ds_context, r.Request, b, backend, pendingReq, r.autoDecompressEncodings, r.framingHeadersMode)

	// Write the pending request handle to guest memory
	i.memory.PutUint32(uint32(phid), int64(ph_out))
	i.abilog.Printf("req_send_async: pending handle=%d", phid)

	return XqdStatusOK
}

// xqd_req_send_async_streaming sends an asynchronous HTTP request with streaming body support.
// Similar to xqd_req_send_async but allows the guest to continue writing to the body handle while the request is in flight.
// Uses a pipe to stream data from guest writes to the backend request.
// Returns XqdErrInvalidHandle if handles are invalid, XqdErrHttpUserInvalid if URL is not set, or XqdStatusOK on success.
func (i *Instance) xqd_req_send_async_streaming(rhandle int32, bhandle int32, backend_addr, backend_size int32, ph_out int32) int32 {
	// Validate request handle
	r := i.requests.Get(int(rhandle))
	if r == nil {
		i.abilog.Printf("req_send_async_streaming: invalid request handle=%d", rhandle)
		return XqdErrInvalidHandle
	}

	// Validate body handle
	b := i.bodies.Get(int(bhandle))
	if b == nil {
		i.abilog.Printf("req_send_async_streaming: invalid body handle=%d", bhandle)
		return XqdErrInvalidHandle
	}

	// Read backend name
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}
	backend := string(buf)

	// Check if URL is set
	if r.URL == nil {
		i.abilog.Printf("req_send_async_streaming: URL not set for request handle %d", rhandle)
		return XqdErrHttpUserInvalid
	}

	i.abilog.Printf("req_send_async_streaming: handle=%d body=%d backend=%q uri=%q", rhandle, bhandle, backend, r.URL)

	// Read initial body content
	initialBody, _ := io.ReadAll(b)

	// Create pipe for streaming
	pipeReader, pipeWriter := io.Pipe()

	// Convert body handle to streaming mode
	b.isStreaming = true
	b.streamingWriter = pipeWriter
	b.streamingChan = make(chan []byte, 128) // Buffered channel for backpressure (128 * 8KB ~= 1MB buffer)
	b.streamingDone = make(chan struct{})
	b.streamingWritten = 0

	// Start goroutine to drain channel to pipe
	go func() {
		defer close(b.streamingDone)

		// First write the initial body
		if len(initialBody) > 0 {
			_, _ = pipeWriter.Write(initialBody)
		}

		pipeOpen := true
		// Then drain the channel, writing chunks to the pipe (or discarding if pipe closed)
		for chunk := range b.streamingChan {
			if chunk == nil {
				// Sentinel value to close
				break
			}
			if pipeOpen {
				_, err := pipeWriter.Write(chunk)
				if err != nil {
					// Pipe closed by backend, but keep draining channel to prevent blocking
					_ = pipeWriter.Close()
					pipeOpen = false
				}
			}
			// If pipe is closed, just discard the data (backend finished early)
		}

		// Close pipe if still open
		if pipeOpen {
			_ = pipeWriter.Close()
		}
	}()

	// Create pending request handle
	phid, pendingReq := i.pendingRequests.New()

	// Launch goroutine to perform the async request
	go func(ctx context.Context, req *http.Request, body io.Reader, backendName string, pr *PendingRequest, autoDecompress uint32, framingMode FramingHeadersMode) {
		// Build the request with the pipe reader as body
		httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), body)
		if err != nil {
			pr.Complete(nil, err)
			return
		}

		httpReq.Header = req.Header.Clone()
		if httpReq.Header == nil {
			httpReq.Header = http.Header{}
		}

		// Add CDN-Loop header
		httpReq.Header.Add("cdn-loop", "fastlike")

		// Apply framing mode - for streaming, Content-Length is unknown but Transfer-Encoding may be set
		validateAndApplyFramingMode(httpReq.Header, framingMode, nil)

		// Get backend handler
		var handler http.Handler
		if backendName == "geolocation" {
			handler = geoHandler(i.geolookup)
		} else {
			handler = i.getBackendHandler(backendName)
		}

		// Execute request
		wr := httptest.NewRecorder()
		handler.ServeHTTP(wr, httpReq)
		resp := wr.Result()

		// Apply auto-decompression if enabled
		_ = applyAutoDecompression(resp, autoDecompress)

		// Mark pending request as complete
		pr.Complete(resp, nil)
	}(i.ds_context, r.Request, pipeReader, backend, pendingReq, r.autoDecompressEncodings, r.framingHeadersMode)

	// Write pending request handle to guest memory
	i.memory.PutUint32(uint32(phid), int64(ph_out))
	i.abilog.Printf("req_send_async_streaming: pending handle=%d", phid)

	return XqdStatusOK
}

// xqd_req_send_async_v2 sends an asynchronous HTTP request with optional streaming.
// Delegates to xqd_req_send_async_streaming if streaming is non-zero, otherwise calls xqd_req_send_async.
// Returns the same status codes as the delegated function.
func (i *Instance) xqd_req_send_async_v2(rhandle int32, bhandle int32, backend_addr, backend_size int32, streaming int32, ph_out int32) int32 {
	if streaming != 0 {
		return i.xqd_req_send_async_streaming(rhandle, bhandle, backend_addr, backend_size, ph_out)
	}
	return i.xqd_req_send_async(rhandle, bhandle, backend_addr, backend_size, ph_out)
}

// xqd_pending_req_poll checks if an async request has completed without blocking.
// If complete, writes response and body handles to guest memory and sets is_done_out to 1.
// If not complete, sets is_done_out to 0 and writes HandleInvalid for response handles.
// Returns XqdErrInvalidHandle if pending handle is invalid, XqdError if request failed, or XqdStatusOK.
func (i *Instance) xqd_pending_req_poll(phandle int32, is_done_out int32, wh_out int32, bh_out int32) int32 {
	// Get the pending request
	pr := i.pendingRequests.Get(int(phandle))
	if pr == nil {
		i.abilog.Printf("pending_req_poll: invalid pending handle=%d", phandle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("pending_req_poll: pending handle=%d", phandle)

	// Check if ready (non-blocking)
	if pr.IsReady() {
		// Request is complete, get the response
		resp, err := pr.Wait() // Won't block since IsReady returned true
		if err != nil {
			i.abilog.Printf("pending_req_poll: request failed, err=%s", err.Error())
			i.memory.PutUint32(1, int64(is_done_out))
			// Return invalid handles to signal error
			i.memory.PutUint32(HandleInvalid, int64(wh_out))
			i.memory.PutUint32(HandleInvalid, int64(bh_out))
			return XqdError
		}

		// Convert the response into (wh, bh) pair
		whid, wh := i.responses.New()
		wh.Status = resp.Status
		wh.StatusCode = resp.StatusCode
		wh.Header = resp.Header.Clone()
		wh.Body = resp.Body

		bhid, _ := i.bodies.NewReader(wh.Body)

		i.abilog.Printf("pending_req_poll: response ready, handle=%d body=%d", whid, bhid)

		// Mark as done and write handles
		i.memory.PutUint32(1, int64(is_done_out))
		i.memory.PutUint32(uint32(whid), int64(wh_out))
		i.memory.PutUint32(uint32(bhid), int64(bh_out))
		return XqdStatusOK
	}

	// Not done yet
	i.abilog.Printf("pending_req_poll: not ready yet")
	i.memory.PutUint32(0, int64(is_done_out))
	i.memory.PutUint32(HandleInvalid, int64(wh_out))
	i.memory.PutUint32(HandleInvalid, int64(bh_out))
	return XqdStatusOK
}

// xqd_pending_req_poll_v2 checks if an async request has completed with error detail support.
// Currently delegates to xqd_pending_req_poll, ignoring the error_detail_out parameter.
// Returns the same status codes as xqd_pending_req_poll.
func (i *Instance) xqd_pending_req_poll_v2(phandle int32, error_detail_out int32, is_done_out int32, wh_out int32, bh_out int32) int32 {
	// For now, ignore error_detail_out and just call the base version
	// In the future, this could populate detailed error information
	return i.xqd_pending_req_poll(phandle, is_done_out, wh_out, bh_out)
}

// xqd_pending_req_wait blocks until an async request completes.
// Pauses CPU time tracking while waiting, then writes response and body handles to guest memory.
// Returns XqdErrInvalidHandle if pending handle is invalid, XqdError if request failed, or XqdStatusOK on success.
func (i *Instance) xqd_pending_req_wait(phandle int32, wh_out int32, bh_out int32) int32 {
	// Get the pending request
	pr := i.pendingRequests.Get(int(phandle))
	if pr == nil {
		i.abilog.Printf("pending_req_wait: invalid pending handle=%d", phandle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("pending_req_wait: pending handle=%d, blocking until complete", phandle)

	// Pause CPU time tracking while waiting for the async request
	i.pauseExecution()
	// Block until the request completes
	resp, err := pr.Wait()
	// Resume CPU time tracking after the wait completes
	i.resumeExecution()

	if err != nil {
		i.abilog.Printf("pending_req_wait: request failed, err=%s", err.Error())
		// Return invalid handles to signal error
		i.memory.PutUint32(HandleInvalid, int64(wh_out))
		i.memory.PutUint32(HandleInvalid, int64(bh_out))
		return XqdError
	}

	// Convert the response into (wh, bh) pair
	whid, wh := i.responses.New()
	wh.Status = resp.Status
	wh.StatusCode = resp.StatusCode
	wh.Header = resp.Header.Clone()
	wh.Body = resp.Body

	bhid, _ := i.bodies.NewReader(wh.Body)

	i.abilog.Printf("pending_req_wait: response complete, handle=%d body=%d", whid, bhid)

	// Write handles
	i.memory.PutUint32(uint32(whid), int64(wh_out))
	i.memory.PutUint32(uint32(bhid), int64(bh_out))
	return XqdStatusOK
}

// xqd_pending_req_wait_v2 blocks until an async request completes with error detail support.
// Currently delegates to xqd_pending_req_wait, ignoring the error_detail_out parameter.
// Returns the same status codes as xqd_pending_req_wait.
func (i *Instance) xqd_pending_req_wait_v2(phandle int32, error_detail_out int32, wh_out int32, bh_out int32) int32 {
	// For now, ignore error_detail_out and just call the base version
	// In the future, this could populate detailed error information
	return i.xqd_pending_req_wait(phandle, wh_out, bh_out)
}

// xqd_pending_req_select blocks until the first of multiple async requests completes.
// Takes an array of pending request handles and returns the index of the first one to complete.
// Pauses CPU time tracking while waiting. Uses goroutines to monitor all pending requests simultaneously.
// Returns XqdErrInvalidArgument if handle list is empty, XqdErrInvalidHandle if any handle is invalid, XqdError if request failed, or XqdStatusOK on success.
func (i *Instance) xqd_pending_req_select(phandles_addr int32, phandles_len int32, done_idx_out int32, wh_out int32, bh_out int32) int32 {
	if phandles_len == 0 {
		i.abilog.Printf("pending_req_select: empty handle list")
		return XqdErrInvalidArgument
	}

	i.abilog.Printf("pending_req_select: selecting from %d pending requests", phandles_len)

	// Read the array of pending request handles from guest memory
	// The array is stored as contiguous int32 values (4 bytes each)
	handles := make([]int32, phandles_len)
	for idx := int32(0); idx < phandles_len; idx++ {
		handleOffset := phandles_addr + idx*4 // Each handle is 4 bytes
		handle := i.memory.Uint32(int64(handleOffset))
		handles[idx] = int32(handle)
	}

	// Build a list of pending requests with their channels for monitoring
	type selectCase struct {
		index   int
		channel <-chan struct{}
		pr      *PendingRequest
	}

	cases := make([]selectCase, 0, len(handles))
	for idx, handle := range handles {
		pr := i.pendingRequests.Get(int(handle))
		if pr == nil {
			i.abilog.Printf("pending_req_select: invalid handle=%d at index=%d", handle, idx)
			return XqdErrInvalidHandle
		}
		cases = append(cases, selectCase{
			index:   idx,
			channel: pr.done,
			pr:      pr,
		})
	}

	// Use goroutines to implement dynamic select over multiple channels
	// Go doesn't support dynamic select natively without reflection,
	// so we spawn a goroutine per channel that signals when its request completes
	doneCh := make(chan int, len(cases))
	for _, c := range cases {
		go func(idx int, ch <-chan struct{}) {
			<-ch
			doneCh <- idx
		}(c.index, c.channel)
	}

	// Pause CPU time tracking while waiting for the first request to complete
	i.pauseExecution()
	// Wait for the first one to complete
	doneIndex := <-doneCh
	// Resume CPU time tracking after a request completes
	i.resumeExecution()

	pr := cases[doneIndex].pr

	i.abilog.Printf("pending_req_select: request at index %d completed first", doneIndex)

	// Get the response
	resp, err := pr.Wait()
	if err != nil {
		i.abilog.Printf("pending_req_select: request failed, err=%s", err.Error())
		i.memory.PutUint32(uint32(doneIndex), int64(done_idx_out))
		i.memory.PutUint32(HandleInvalid, int64(wh_out))
		i.memory.PutUint32(HandleInvalid, int64(bh_out))
		return XqdError
	}

	// Convert the response into (wh, bh) pair
	whid, wh := i.responses.New()
	wh.Status = resp.Status
	wh.StatusCode = resp.StatusCode
	wh.Header = resp.Header.Clone()
	wh.Body = resp.Body

	bhid, _ := i.bodies.NewReader(wh.Body)

	i.abilog.Printf("pending_req_select: response handle=%d body=%d", whid, bhid)

	// Write the results
	i.memory.PutUint32(uint32(doneIndex), int64(done_idx_out))
	i.memory.PutUint32(uint32(whid), int64(wh_out))
	i.memory.PutUint32(uint32(bhid), int64(bh_out))

	return XqdStatusOK
}

// xqd_pending_req_select_v2 blocks until the first of multiple async requests completes with error detail support.
// Currently delegates to xqd_pending_req_select, ignoring the error_detail_out parameter.
// Returns the same status codes as xqd_pending_req_select.
func (i *Instance) xqd_pending_req_select_v2(phandles_addr int32, phandles_len int32, error_detail_out int32, done_idx_out int32, wh_out int32, bh_out int32) int32 {
	// For now, ignore error_detail_out and just call the base version
	// In the future, this could populate detailed error information
	return i.xqd_pending_req_select(phandles_addr, phandles_len, done_idx_out, wh_out, bh_out)
}

// xqd_req_send_v2 sends a synchronous HTTP request with error detail support.
// Similar to xqd_req_send but populates a SendErrorDetail struct in guest memory with error information.
// Monitors context cancellation during the request and returns XqdError if cancelled.
// Returns XqdErrInvalidHandle if handles are invalid, XqdErrHttpUserInvalid if URL is not set, or XqdStatusOK on success.
func (i *Instance) xqd_req_send_v2(rhandle int32, bhandle int32, backend_addr, backend_size int32, error_detail_out int32, wh_out int32, bh_out int32) int32 {
	// Validate request handle
	r := i.requests.Get(int(rhandle))
	if r == nil {
		i.abilog.Printf("req_send_v2: invalid request handle=%d", rhandle)
		return XqdErrInvalidHandle
	}

	// Validate body handle
	b := i.bodies.Get(int(bhandle))
	if b == nil {
		i.abilog.Printf("req_send_v2: invalid body handle=%d", bhandle)
		return XqdErrInvalidHandle
	}

	// Read backend name
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		errorDetail := createErrorDetailFromError(err)
		_ = i.writeSendErrorDetail(error_detail_out, errorDetail)
		return XqdError
	}

	backend := string(buf)

	// Check if URL is set
	if r.URL == nil {
		i.abilog.Printf("req_send_v2: URL not set for request handle %d", rhandle)
		errorDetail := createErrorDetailFromError(fmt.Errorf("URL not set"))
		_ = i.writeSendErrorDetail(error_detail_out, errorDetail)
		return XqdErrHttpUserInvalid
	}

	i.abilog.Printf("req_send_v2: handle=%d body=%d backend=%q uri=%q", rhandle, bhandle, backend, r.URL)

	// Build the HTTP request
	req, err := http.NewRequestWithContext(i.ds_context, r.Method, r.URL.String(), b)
	if err != nil {
		errorDetail := createErrorDetailFromError(err)
		_ = i.writeSendErrorDetail(error_detail_out, errorDetail)
		return XqdErrHttpUserInvalid
	}

	req.Header = r.Header.Clone()

	if req.Header == nil {
		req.Header = http.Header{}
	}

	// Add CDN-Loop header for loop detection
	req.Header.Add("cdn-loop", "fastlike")

	// Apply framing mode - validate and potentially filter framing headers
	effectiveMode := validateAndApplyFramingMode(req.Header, r.framingHeadersMode, func(format string, args ...interface{}) {
		i.abilog.Printf("req_send_v2: "+format, args...)
	})

	// Set Content-Length only in automatic mode
	if effectiveMode == FramingHeadersModeAutomatic {
		if req.Header.Get("content-length") == "" {
			req.Header.Add("content-length", fmt.Sprintf("%d", b.Size()))
			req.ContentLength = b.Size()
		} else if req.ContentLength <= 0 {
			req.ContentLength = b.Size()
		}
	} else {
		// Manual mode: use the Content-Length from headers if present
		if cl := req.Header.Get("Content-Length"); cl != "" {
			var contentLength int64
			fmt.Sscanf(cl, "%d", &contentLength)
			req.ContentLength = contentLength
		}
	}

	// Get the backend handler
	var handler http.Handler
	if backend == "geolocation" {
		handler = geoHandler(i.geolookup)
	} else {
		handler = i.getBackendHandler(backend)
	}

	// Check if context is already cancelled before making the request
	if i.ds_context != nil {
		select {
		case <-i.ds_context.Done():
			// Context was cancelled (timeout/cancellation), panic to cause interrupt trap
			i.abilog.Printf("req_send_v2: context cancelled before request")
			panic("wasm trap: interrupt")
		default:
			// Context not cancelled, proceed
		}
	}

	// Execute the request
	wr := httptest.NewRecorder()

	// Pause CPU time tracking during the blocking HTTP request
	i.pauseExecution()

	// Run the handler in a goroutine so we can monitor context cancellation
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(wr, req)
		close(done)
	}()

	// Wait for either the request to complete or context to be cancelled
	if i.ds_context != nil {
		i.abilog.Printf("req_send_v2: waiting with context cancellation support")
		select {
		case <-done:
			// Request completed normally
			i.abilog.Printf("req_send_v2: request completed normally")
			i.resumeExecution()
		case <-i.ds_context.Done():
			// Context was cancelled during request
			i.abilog.Printf("req_send_v2: context cancelled during request")
			i.resumeExecution()
			// Wait a bit for the handler to finish (best effort cleanup)
			select {
			case <-done:
				i.abilog.Printf("req_send_v2: handler finished after cancel")
			case <-time.After(10 * time.Millisecond):
				i.abilog.Printf("req_send_v2: handler still running after cancel timeout")
			}
			return XqdError
		}
	} else {
		// No context, just wait for completion
		i.abilog.Printf("req_send_v2: waiting without context (ds_context is nil)")
		<-done
		i.resumeExecution()
	}

	w := wr.Result()

	// Apply auto-decompression if enabled
	_ = applyAutoDecompression(w, r.autoDecompressEncodings)

	// Convert the response into an (rh, bh) pair
	whid, wh := i.responses.New()
	wh.Status = w.Status
	wh.StatusCode = w.StatusCode
	wh.Header = w.Header.Clone()
	wh.Body = w.Body

	bhid, _ := i.bodies.NewReader(wh.Body)

	i.abilog.Printf("req_send_v2: response handle=%d body=%d", whid, bhid)

	// Write success error detail
	successDetail := &SendErrorDetail{
		Tag:  SendErrorDetailOk,
		Mask: 0,
	}
	_ = i.writeSendErrorDetail(error_detail_out, successDetail)

	// Write output handles
	i.memory.PutUint32(uint32(whid), int64(wh_out))
	i.memory.PutUint32(uint32(bhid), int64(bh_out))

	return XqdStatusOK
}

// xqd_req_send_v3 sends a synchronous HTTP request with error detail support.
// Identical to xqd_req_send_v2 for local testing (the difference in production is cache override behavior).
// Returns the same status codes as xqd_req_send_v2.
func (i *Instance) xqd_req_send_v3(rhandle int32, bhandle int32, backend_addr, backend_size int32, error_detail_out int32, wh_out int32, bh_out int32) int32 {
	// send_v3 is the same as send_v2 for now
	// The main difference is that send_v3 skips cache override entirely,
	// but since we don't implement caching, they're functionally identical
	i.abilog.Printf("req_send_v3: delegating to send_v2")
	return i.xqd_req_send_v2(rhandle, bhandle, backend_addr, backend_size, error_detail_out, wh_out, bh_out)
}

// xqd_req_downstream_client_ddos_detected checks if the client is flagged for DDoS
// Always returns 0 (false) for local testing
func (i *Instance) xqd_req_downstream_client_ddos_detected(is_ddos_out int32) int32 {
	i.abilog.Printf("req_downstream_client_ddos_detected")

	// Always return false (0) for local testing
	// In production Fastly environment, this would check actual DDoS detection flags
	i.memory.PutUint32(0, int64(is_ddos_out))
	return XqdStatusOK
}

// xqd_req_fastly_key_is_valid checks if the request has a valid Fastly purge key
// Always returns 0 (false) since we don't have keys to validate
func (i *Instance) xqd_req_fastly_key_is_valid(is_valid_out int32) int32 {
	i.abilog.Printf("req_fastly_key_is_valid")

	// Always return false (0) since there are no keys to compare against
	i.memory.PutUint32(0, int64(is_valid_out))
	return XqdStatusOK
}

// xqd_req_downstream_compliance_region returns the compliance region for the downstream request
func (i *Instance) xqd_req_downstream_compliance_region(addr int32, maxlen int32, nwritten_out int32) int32 {
	i.abilog.Printf("req_downstream_compliance_region")

	// Get the compliance region (configured via WithComplianceRegion option)
	region := i.complianceRegion

	// Check if the buffer is large enough
	if int32(len(region)) > maxlen {
		// Write the required length
		i.memory.PutUint32(uint32(len(region)), int64(nwritten_out))
		return XqdErrBufferLength
	}

	// Write the region string to guest memory
	nwritten, err := i.memory.WriteAt([]byte(region), int64(addr))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	return XqdStatusOK
}

// xqd_req_on_behalf_of sets the service ID for cache operations (multi-tenant caching)
// In local testing, we just store the service name but don't actually use it
func (i *Instance) xqd_req_on_behalf_of(handle int32, service_addr int32, service_size int32) int32 {
	// Validate request handle
	r := i.requests.Get(int(handle))
	if r == nil {
		i.abilog.Printf("req_on_behalf_of: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	// Read service name from guest memory
	buf := make([]byte, service_size)
	_, err := i.memory.ReadAt(buf, int64(service_addr))
	if err != nil {
		return XqdError
	}

	serviceName := string(buf)
	i.abilog.Printf("req_on_behalf_of: handle=%d service=%q", handle, serviceName)

	// Store the service name in request metadata
	// In a full implementation, this would affect cache key generation
	// For now, we just track it for logging purposes
	if r.Header == nil {
		r.Header = http.Header{}
	}
	r.Header.Set("X-Fastlike-On-Behalf-Of", serviceName)

	return XqdStatusOK
}

// xqd_req_framing_headers_mode_set controls how framing headers (Content-Length, Transfer-Encoding) are set.
// Mode 0 (Automatic): The HTTP library sets framing headers automatically.
// Mode 1 (ManuallyFromHeaders): User-provided framing headers are preserved and used.
func (i *Instance) xqd_req_framing_headers_mode_set(handle int32, mode int32) int32 {
	// Validate request handle
	r := i.requests.Get(int(handle))
	if r == nil {
		i.abilog.Printf("req_framing_headers_mode_set: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("req_framing_headers_mode_set: handle=%d mode=%d", handle, mode)

	// Validate mode value
	if mode != int32(FramingHeadersModeAutomatic) && mode != int32(FramingHeadersModeManuallyFromHeaders) {
		i.abilog.Printf("req_framing_headers_mode_set: invalid mode %d", mode)
		return XqdErrInvalidArgument
	}

	r.framingHeadersMode = FramingHeadersMode(mode)
	return XqdStatusOK
}

// xqd_req_auto_decompress_response_set controls automatic decompression of responses
// This sets a bitfield indicating which Content-Encodings should be auto-decompressed
func (i *Instance) xqd_req_auto_decompress_response_set(handle int32, encodings int32) int32 {
	// Validate request handle
	r := i.requests.Get(int(handle))
	if r == nil {
		i.abilog.Printf("req_auto_decompress_response_set: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("req_auto_decompress_response_set: handle=%d encodings=%d", handle, encodings)

	// Store the auto-decompress encodings in the request metadata
	r.autoDecompressEncodings = uint32(encodings)

	return XqdStatusOK
}

// xqd_req_register_dynamic_backend registers a backend dynamically at runtime
// This function reads the backend configuration from guest memory and creates a new backend
func (i *Instance) xqd_req_register_dynamic_backend(name_prefix_addr int32, name_prefix_size int32, target_addr int32, target_size int32, backend_config_mask int32, backend_config_addr int32) int32 {
	i.abilog.Printf("req_register_dynamic_backend: name_prefix_addr=%d target_addr=%d mask=%d", name_prefix_addr, target_addr, backend_config_mask)

	// Check for reserved bit - if set, return error
	if (uint32(backend_config_mask) & BackendConfigOptionsReserved) != 0 {
		i.abilog.Printf("req_register_dynamic_backend: reserved bit is set")
		return XqdErrInvalidArgument
	}

	// Read the backend name prefix
	namePrefix := make([]byte, name_prefix_size)
	_, err := i.memory.ReadAt(namePrefix, int64(name_prefix_addr))
	if err != nil {
		i.abilog.Printf("req_register_dynamic_backend: failed to read name prefix")
		return XqdError
	}

	// Read the target URL
	targetBuf := make([]byte, target_size)
	_, err = i.memory.ReadAt(targetBuf, int64(target_addr))
	if err != nil {
		i.abilog.Printf("req_register_dynamic_backend: failed to read target")
		return XqdError
	}

	targetURL := string(targetBuf)
	backendName := string(namePrefix)

	i.abilog.Printf("req_register_dynamic_backend: name=%q target=%q", backendName, targetURL)

	// Check if backend already exists
	if i.backendExists(backendName) {
		i.abilog.Printf("req_register_dynamic_backend: backend %q already exists", backendName)
		return XqdErrInvalidArgument
	}

	// Read the dynamic backend config struct from memory
	var config DynamicBackendConfig

	// Read the entire struct (it's laid out contiguously in memory)
	// The struct is 96 bytes = 4+4+4+4+4+4+4+4+4+4+4+4+4+4+4+4+4+4+4+4+4+4+4+4
	configBuf := make([]byte, 96)
	_, err = i.memory.ReadAt(configBuf, int64(backend_config_addr))
	if err != nil {
		i.abilog.Printf("req_register_dynamic_backend: failed to read backend config")
		return XqdError
	}

	// Parse the struct fields manually (little-endian)
	offset := 0
	config.HostOverride = int32(i.memory.Uint32(int64(backend_config_addr) + int64(offset)))
	offset += 4
	config.HostOverrideLen = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.ConnectTimeoutMs = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.FirstByteTimeoutMs = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.BetweenBytesTimeoutMs = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.SSLMinVersion = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.SSLMaxVersion = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.CertHostname = int32(i.memory.Uint32(int64(backend_config_addr) + int64(offset)))
	offset += 4
	config.CertHostnameLen = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.CACert = int32(i.memory.Uint32(int64(backend_config_addr) + int64(offset)))
	offset += 4
	config.CACertLen = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.Ciphers = int32(i.memory.Uint32(int64(backend_config_addr) + int64(offset)))
	offset += 4
	config.CiphersLen = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.SNIHostname = int32(i.memory.Uint32(int64(backend_config_addr) + int64(offset)))
	offset += 4
	config.SNIHostnameLen = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.ClientCertificate = int32(i.memory.Uint32(int64(backend_config_addr) + int64(offset)))
	offset += 4
	config.ClientCertificateLen = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.ClientKey = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.HTTPKeepaliveTimeMs = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.TCPKeepaliveEnable = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.TCPKeepaliveIntervalSecs = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.TCPKeepaliveProbes = i.memory.Uint32(int64(backend_config_addr) + int64(offset))
	offset += 4
	config.TCPKeepaliveTimeSecs = i.memory.Uint32(int64(backend_config_addr) + int64(offset))

	// Parse the target URL
	u, err := url.Parse(targetURL)
	if err != nil {
		i.abilog.Printf("req_register_dynamic_backend: failed to parse target URL %q: %v", targetURL, err)
		return XqdErrInvalidArgument
	}

	// Create a new Backend struct
	backend := &Backend{
		Name:      backendName,
		URL:       u,
		IsDynamic: true,
		Handler:   nil, // Will use defaultBackend or create a handler
	}

	// Apply configuration options based on the mask
	if (uint32(backend_config_mask) & BackendConfigOptionsHostOverride) != 0 {
		if config.HostOverrideLen > 0 && config.HostOverrideLen <= 1024 {
			hostOverrideBuf := make([]byte, config.HostOverrideLen)
			_, err := i.memory.ReadAt(hostOverrideBuf, int64(config.HostOverride))
			if err == nil {
				backend.OverrideHost = string(hostOverrideBuf)
				i.abilog.Printf("req_register_dynamic_backend: override_host=%q", backend.OverrideHost)
			}
		}
	}

	if (uint32(backend_config_mask) & BackendConfigOptionsConnectTimeout) != 0 {
		backend.ConnectTimeoutMs = config.ConnectTimeoutMs
	}

	if (uint32(backend_config_mask) & BackendConfigOptionsFirstByteTimeout) != 0 {
		backend.FirstByteTimeoutMs = config.FirstByteTimeoutMs
	}

	if (uint32(backend_config_mask) & BackendConfigOptionsBetweenBytesTimeout) != 0 {
		backend.BetweenBytesTimeoutMs = config.BetweenBytesTimeoutMs
	}

	if (uint32(backend_config_mask) & BackendConfigOptionsUseSSL) != 0 {
		backend.UseSSL = true
	}

	if (uint32(backend_config_mask) & BackendConfigOptionsSSLMinVersion) != 0 {
		backend.SSLMinVersion = config.SSLMinVersion
	}

	if (uint32(backend_config_mask) & BackendConfigOptionsSSLMaxVersion) != 0 {
		backend.SSLMaxVersion = config.SSLMaxVersion
	}

	if (uint32(backend_config_mask) & BackendConfigOptionsKeepalive) != 0 {
		backend.HTTPKeepaliveTimeMs = config.HTTPKeepaliveTimeMs
		backend.TCPKeepaliveEnable = config.TCPKeepaliveEnable != 0
		backend.TCPKeepaliveTimeMs = config.TCPKeepaliveTimeSecs * 1000
		backend.TCPKeepaliveIntervalMs = config.TCPKeepaliveIntervalSecs * 1000
		backend.TCPKeepaliveProbes = config.TCPKeepaliveProbes
	}

	// Create a simple http.Handler for the backend
	// For local testing, we use http.DefaultTransport to make actual HTTP requests
	backend.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Update the request URL to point to the backend
		r.URL.Scheme = backend.URL.Scheme
		r.URL.Host = backend.URL.Host

		// Apply host override if configured
		if backend.OverrideHost != "" {
			r.Host = backend.OverrideHost
			r.Header.Set("Host", backend.OverrideHost)
		}

		// Use http.DefaultTransport to make the actual request
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = fmt.Fprintf(w, "Backend request failed: %v", err)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		// Copy the response
		for k, v := range resp.Header {
			for _, vv := range v {
				w.Header().Add(k, vv)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})

	// Register the backend
	i.addBackend(backendName, backend)

	i.abilog.Printf("req_register_dynamic_backend: successfully registered backend %q", backendName)
	return XqdStatusOK
}

// xqd_req_inspect performs NGWAF (Web Application Firewall) inspection
// Returns UNSUPPORTED in local testing environments as NGWAF is not available
func (i *Instance) xqd_req_inspect(
	req int32,
	body int32,
	insp_info_mask int32,
	insp_info int32,
	buf int32,
	buf_len int32,
) int32 {
	i.abilog.Printf("req_inspect: NGWAF not available in local testing")
	// NGWAF (Next-Gen Web Application Firewall) is only available in Fastly production
	// Return unsupported for local testing
	return XqdErrUnsupported
}

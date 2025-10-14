package fastlike

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
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

// createErrorDetailFromError converts a Go error to a SendErrorDetail
func createErrorDetailFromError(err error) *SendErrorDetail {
	if err == nil {
		return &SendErrorDetail{
			Tag:  SendErrorDetailOk,
			Mask: 0,
		}
	}

	// For now, we return a generic internal error
	// In a more sophisticated implementation, we could parse the error
	// to determine the specific error type
	return &SendErrorDetail{
		Tag:  SendErrorDetailInternalError,
		Mask: 0,
	}
}

// applyAutoDecompression checks if the response should be auto-decompressed and does so
// This modifies the response in-place if decompression is needed
func applyAutoDecompression(resp *http.Response, autoDecompressEncodings uint32) error {
	// Check if gzip auto-decompression is enabled
	if (autoDecompressEncodings & ContentEncodingsGzip) == 0 {
		// Auto-decompression not enabled
		return nil
	}

	// Check if the response has gzip or x-gzip encoding
	encoding := resp.Header.Get("Content-Encoding")
	if encoding != "gzip" && encoding != "x-gzip" {
		// Not gzip encoded
		return nil
	}

	// Read the compressed body
	compressedBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	// Decompress the body
	gzReader, err := gzip.NewReader(bytes.NewReader(compressedBody))
	if err != nil {
		// If we can't create a gzip reader, return the original compressed body
		// but still remove the Content-Encoding header
		resp.Body = io.NopCloser(bytes.NewReader(compressedBody))
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Content-Length")
		return err
	}
	defer gzReader.Close()

	decompressedBody, err := io.ReadAll(gzReader)
	if err != nil {
		// If we can't decompress, return the original compressed body
		// but still remove the Content-Encoding header
		resp.Body = io.NopCloser(bytes.NewReader(compressedBody))
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Content-Length")
		return err
	}

	// Replace the response body with the decompressed version
	resp.Body = io.NopCloser(bytes.NewReader(decompressedBody))

	// Remove Content-Encoding and Content-Length headers as per the spec
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")

	return nil
}

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

func (i *Instance) xqd_req_cache_override_set(handle int32, tag int32, ttl int32, swr int32) int32 {
	// We don't actually *do* anything with cache overrides, since we don't have or need a cache.

	if i.requests.Get(int(handle)) == nil {
		i.abilog.Printf("req_cache_override_set: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	return XqdStatusOK
}

func (i *Instance) xqd_req_cache_override_v2_set(handle int32, tag int32, ttl int32, swr int32, sk int32, sk_len int32) int32 {
	// We don't actually *do* anything with cache overrides, since we don't have or need a cache.

	if i.requests.Get(int(handle)) == nil {
		i.abilog.Printf("req_cache_override_v2_set: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	return XqdStatusOK
}

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

	// Make sure the method is in the set of valid http methods
	methods := strings.Join([]string{
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

	if !strings.Contains(methods, strings.ToUpper(string(method))) {
		i.abilog.Printf("req_method_set: invalid method=%q", method)
		return XqdErrHttpParse
	}

	i.abilog.Printf("req_method_set: handle=%d method=%q", handle, method)

	r.Method = strings.ToUpper(string(method))
	return XqdStatusOK
}

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
		return XqdErrHttpParse
	}

	i.abilog.Printf("req_uri_set: handle=%d uri=%q", handle, u)

	r.URL = u
	return XqdStatusOK
}

func (i *Instance) xqd_req_header_names_get(handle int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
	i.abilog.Printf("req_header_names_get: handle=%d cursor=%d", handle, cursor)

	r := i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	names := []string{}
	for n := range r.Header {
		names = append(names, n)
	}

	// these names are explicitly unsorted, so let's sort them ourselves
	sort.Strings(names[:])

	return xqd_multivalue(i.memory, names, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

func (i *Instance) xqd_req_header_remove(handle int32, name_addr int32, name_size int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	name := make([]byte, name_size)
	_, err := i.memory.ReadAt(name, int64(name_addr))
	if err != nil {
		return XqdError
	}

	r.Header.Del(string(name))

	return XqdStatusOK
}

func (i *Instance) xqd_req_header_insert(handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
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

func (i *Instance) xqd_req_header_append(handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
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

func (i *Instance) xqd_req_header_value_get(handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, nwritten_out int32) int32 {
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

	i.abilog.Printf("req_header_value_get: handle=%d header=%q\n", handle, header)

	value := r.Header.Get(header)
	nwritten, _ := i.memory.WriteAt([]byte(value), int64(addr))
	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))

	return XqdStatusOK
}

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

func (i *Instance) xqd_req_header_values_set(handle int32, name_addr int32, name_size int32, values_addr int32, values_size int32) int32 {
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

	// read values_size bytes from values_addr for a list of \0 terminated values for the header
	// but, read 1 less than that to avoid the trailing nul
	buf = make([]byte, values_size-1)
	_, err = i.memory.ReadAt(buf, int64(values_addr))
	if err != nil {
		return XqdError
	}

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

func (i *Instance) xqd_req_uri_get(handle int32, addr int32, maxlen int32, nwritten_out int32) int32 {
	r := i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	uri := r.URL.String()
	i.abilog.Printf("req_uri_get: handle=%d uri=%q", handle, uri)

	nwritten, err := i.memory.WriteAt([]byte(uri), int64(addr))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	return XqdStatusOK
}

func (i *Instance) xqd_req_new(handle_out int32) int32 {
	rhid, _ := i.requests.New()
	i.abilog.Printf("req_new: handle=%d", rhid)
	i.memory.PutUint32(uint32(rhid), int64(handle_out))
	return XqdStatusOK
}

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

	i.abilog.Printf("req_send: handle=%d body=%d backend=%q uri=%q", rhandle, bhandle, backend, r.URL)

	req, err := http.NewRequestWithContext(i.ds_context, r.Method, r.URL.String(), b)
	if err != nil {
		return XqdErrHttpUserInvalid
	}

	req.Header = r.Header.Clone()

	// TODO: Ensure we always have something in r.Header so we can avoid the nil check here
	if req.Header == nil {
		req.Header = http.Header{}
	}

	// Make sure to add a CDN-Loop header, which we can check (and block) at ingress
	req.Header.Add("cdn-loop", "fastlike")

	// TODO: Not sure if this is strictly necessary (or correct!)
	if req.Header.Get("content-length") == "" {
		req.Header.Add("content-length", fmt.Sprintf("%d", b.Size()))
		req.ContentLength = b.Size()
	} else if req.ContentLength <= 0 {
		req.ContentLength = b.Size()
	}

	// If the backend is geolocation, we select the geobackend explicitly
	var handler http.Handler
	if backend == "geolocation" {
		handler = geoHandler(i.geolookup)
	} else {
		handler = i.getBackendHandler(backend)
	}

	// TODO: Is there a better way to get an *http.Response from an http.Handler?
	// The Handler interface is useful for embedders, since often-times they'll be processing wasm
	// requests in the embedding application, and it's very easy to adapt an http.Handler to an
	// http.RoundTripper if they want it to go offsite.
	wr := httptest.NewRecorder()

	// Pause CPU time tracking during the blocking HTTP request
	i.pauseExecution()
	handler.ServeHTTP(wr, req)
	// Resume CPU time tracking after the request completes
	i.resumeExecution()

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

	i.abilog.Printf("req_send_async: handle=%d body=%d backend=%q uri=%q", rhandle, bhandle, backend, r.URL)

	// Create a pending request handle
	phid, pendingReq := i.pendingRequests.New()

	// Launch goroutine to perform the request asynchronously
	go func(ctx context.Context, req *http.Request, body *BodyHandle, backendName string, pr *PendingRequest, autoDecompress uint32) {
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

		// Set content-length if needed
		if httpReq.Header.Get("content-length") == "" {
			httpReq.Header.Add("content-length", fmt.Sprintf("%d", body.Size()))
			httpReq.ContentLength = body.Size()
		} else if httpReq.ContentLength <= 0 {
			httpReq.ContentLength = body.Size()
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
	}(i.ds_context, r.Request, b, backend, pendingReq, r.autoDecompressEncodings)

	// Write the pending request handle to guest memory
	i.memory.PutUint32(uint32(phid), int64(ph_out))
	i.abilog.Printf("req_send_async: pending handle=%d", phid)

	return XqdStatusOK
}

func (i *Instance) xqd_req_send_async_streaming(rhandle int32, bhandle int32, backend_addr, backend_size int32, ph_out int32) int32 {
	// For now, streaming is the same as non-streaming since we buffer everything anyway
	// In a more sophisticated implementation, this would enable true streaming
	return i.xqd_req_send_async(rhandle, bhandle, backend_addr, backend_size, ph_out)
}

func (i *Instance) xqd_req_send_async_v2(rhandle int32, bhandle int32, backend_addr, backend_size int32, streaming int32, ph_out int32) int32 {
	if streaming != 0 {
		return i.xqd_req_send_async_streaming(rhandle, bhandle, backend_addr, backend_size, ph_out)
	}
	return i.xqd_req_send_async(rhandle, bhandle, backend_addr, backend_size, ph_out)
}

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
			i.memory.PutUint32(0xFFFFFFFF, int64(wh_out))
			i.memory.PutUint32(0xFFFFFFFF, int64(bh_out))
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
	i.memory.PutUint32(0xFFFFFFFF, int64(wh_out))
	i.memory.PutUint32(0xFFFFFFFF, int64(bh_out))
	return XqdStatusOK
}

func (i *Instance) xqd_pending_req_poll_v2(phandle int32, is_done_out int32, wh_out int32, bh_out int32, error_detail_out int32) int32 {
	// For now, ignore error_detail_out and just call the base version
	// In the future, this could populate detailed error information
	return i.xqd_pending_req_poll(phandle, is_done_out, wh_out, bh_out)
}

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
		i.memory.PutUint32(0xFFFFFFFF, int64(wh_out))
		i.memory.PutUint32(0xFFFFFFFF, int64(bh_out))
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

func (i *Instance) xqd_pending_req_wait_v2(phandle int32, wh_out int32, bh_out int32, error_detail_out int32) int32 {
	// For now, ignore error_detail_out and just call the base version
	// In the future, this could populate detailed error information
	return i.xqd_pending_req_wait(phandle, wh_out, bh_out)
}

func (i *Instance) xqd_pending_req_select(phandles_addr int32, phandles_len int32, done_idx_out int32, wh_out int32, bh_out int32) int32 {
	if phandles_len == 0 {
		i.abilog.Printf("pending_req_select: empty handle list")
		return XqdErrInvalidArgument
	}

	i.abilog.Printf("pending_req_select: selecting from %d pending requests", phandles_len)

	// Read the list of pending request handles from guest memory
	handles := make([]int32, phandles_len)
	for idx := int32(0); idx < phandles_len; idx++ {
		handle := i.memory.Uint32(int64(phandles_addr + idx*4))
		handles[idx] = int32(handle)
	}

	// Build a list of channels to select on
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

	// Use reflection to build a dynamic select
	// Go doesn't support dynamic select natively, so we need to use reflect.Select
	// Or, we can use a simpler approach with goroutines

	// Simple approach: spawn goroutines to monitor each channel
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
		i.memory.PutUint32(0xFFFFFFFF, int64(wh_out))
		i.memory.PutUint32(0xFFFFFFFF, int64(bh_out))
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

func (i *Instance) xqd_pending_req_select_v2(phandles_addr int32, phandles_len int32, done_idx_out int32, wh_out int32, bh_out int32, error_detail_out int32) int32 {
	// For now, ignore error_detail_out and just call the base version
	// In the future, this could populate detailed error information
	return i.xqd_pending_req_select(phandles_addr, phandles_len, done_idx_out, wh_out, bh_out)
}

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

	// Set content-length if needed
	if req.Header.Get("content-length") == "" {
		req.Header.Add("content-length", fmt.Sprintf("%d", b.Size()))
		req.ContentLength = b.Size()
	} else if req.ContentLength <= 0 {
		req.ContentLength = b.Size()
	}

	// Get the backend handler
	var handler http.Handler
	if backend == "geolocation" {
		handler = geoHandler(i.geolookup)
	} else {
		handler = i.getBackendHandler(backend)
	}

	// Execute the request
	wr := httptest.NewRecorder()

	// Pause CPU time tracking during the blocking HTTP request
	i.pauseExecution()
	handler.ServeHTTP(wr, req)
	// Resume CPU time tracking after the request completes
	i.resumeExecution()

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

// xqd_req_framing_headers_mode_set controls how framing headers (Content-Length, Transfer-Encoding) are set
// Only supports automatic mode
func (i *Instance) xqd_req_framing_headers_mode_set(handle int32, mode int32) int32 {
	// Validate request handle
	r := i.requests.Get(int(handle))
	if r == nil {
		i.abilog.Printf("req_framing_headers_mode_set: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("req_framing_headers_mode_set: handle=%d mode=%d", handle, mode)

	// Mode 0 = Automatic (supported)
	// Mode 1 = ManuallyFromHeaders (not supported)
	if mode != 0 {
		i.abilog.Printf("req_framing_headers_mode_set: manual mode not supported")
		return XqdErrUnsupported
	}

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
			_, _ = w.Write([]byte(fmt.Sprintf("Backend request failed: %v", err)))
			return
		}
		defer resp.Body.Close()

		// Copy the response
		for k, v := range resp.Header {
			for _, vv := range v {
				w.Header().Add(k, vv)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	// Register the backend
	i.addBackend(backendName, backend)

	i.abilog.Printf("req_register_dynamic_backend: successfully registered backend %q", backendName)
	return XqdStatusOK
}

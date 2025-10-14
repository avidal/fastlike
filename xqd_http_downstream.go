package fastlike

import (
	"errors"
	"time"
)

// xqd_http_downstream_next_request creates a request promise for receiving an additional downstream request.
// In production Fastly, this enables session reuse where one wasm execution handles multiple requests.
// In local testing, this will never receive a request and will timeout.
//
// Signature: (options_mask: u32, options: *const NextRequestOptions) -> RequestPromiseHandle
func (i *Instance) xqd_http_downstream_next_request(options_mask int32, options_ptr int32) int32 {
	i.abilog.Printf("http_downstream_next_request: options_mask=%d", options_mask)

	// Create a new request promise handle
	promiseID, promise := i.requestPromises.New()

	// In local testing, we don't support session reuse, so we'll immediately fail the promise
	// with a timeout error. In production, this would register for the next downstream request.
	//
	// For now, we return the handle and let next_request_wait time out or the guest can
	// call next_request_abandon to cancel it.
	go func() {
		// Parse timeout from options if provided
		timeout := 10 * time.Second // default timeout
		if options_mask&0x1 != 0 {  // NextRequestOptionsMask::TIMEOUT
			// Read timeout_ms from options struct (first field, u32)
			timeoutMs := i.memory.Uint32(int64(options_ptr))
			if timeoutMs > 0 {
				timeout = time.Duration(timeoutMs) * time.Millisecond
			}
		}

		// Wait for timeout (simulating no new requests arriving)
		time.Sleep(timeout)

		// Complete with timeout error
		promise.Complete(nil, errors.New("no additional downstream requests in local testing"))
	}()

	return int32(promiseID)
}

// xqd_http_downstream_next_request_wait waits for the next downstream request to arrive.
// Returns the request and body handles when a request arrives.
//
// Signature: (handle: RequestPromiseHandle, req_handle_out: *mut RequestHandle, body_handle_out: *mut BodyHandle) -> FastlyStatus
func (i *Instance) xqd_http_downstream_next_request_wait(
	promise_handle int32,
	req_handle_out int32,
	body_handle_out int32,
) int32 {
	i.abilog.Printf("http_downstream_next_request_wait: promise_handle=%d", promise_handle)

	promise := i.requestPromises.Get(int(promise_handle))
	if promise == nil {
		i.abilog.Printf("http_downstream_next_request_wait: invalid promise handle")
		return XqdErrInvalidHandle
	}

	// Wait for the promise to complete (this will block until timeout or abandonment)
	req, err := promise.Wait()
	if err != nil {
		i.abilog.Printf("http_downstream_next_request_wait: %v", err)
		// Return error code for no more requests
		return XqdErrAgain // EWOULDBLOCK - no additional requests available
	}

	if req == nil {
		// No request received (timed out)
		return XqdErrAgain
	}

	// Create handles for the new request and body
	// (This code path won't execute in local testing since we never receive requests)
	reqID, reqHandle := i.requests.New()
	reqHandle.Request = req

	bodyID, _ := i.bodies.NewBuffer()

	i.memory.PutUint32(uint32(reqID), int64(req_handle_out))
	i.memory.PutUint32(uint32(bodyID), int64(body_handle_out))

	i.abilog.Printf("http_downstream_next_request_wait: req=%d body=%d", reqID, bodyID)
	return XqdStatusOK
}

// xqd_http_downstream_next_request_abandon cancels a pending request promise.
//
// Signature: (handle: RequestPromiseHandle) -> FastlyStatus
func (i *Instance) xqd_http_downstream_next_request_abandon(promise_handle int32) int32 {
	i.abilog.Printf("http_downstream_next_request_abandon: promise_handle=%d", promise_handle)

	promise := i.requestPromises.Get(int(promise_handle))
	if promise == nil {
		i.abilog.Printf("http_downstream_next_request_abandon: invalid promise handle")
		return XqdErrInvalidHandle
	}

	// Complete the promise with an abandonment error
	// This is safe to call multiple times (subsequent calls will be no-ops due to closed channel)
	go func() {
		defer func() {
			// Recover from panic if channel is already closed
			_ = recover()
		}()
		promise.Complete(nil, errors.New("request promise abandoned"))
	}()

	return XqdStatusOK
}

// xqd_http_downstream_original_header_names retrieves the original header names from the downstream request.
// This preserves the original casing and order of headers as received.
//
// Signature: (handle: RequestHandle, buf: *mut u8, buf_len: u32, cursor: u32, ending_cursor_out: *mut u32, nwritten_out: *mut u32) -> FastlyStatus
func (i *Instance) xqd_http_downstream_original_header_names(
	req_handle int32,
	buf_ptr int32,
	buf_len int32,
	cursor int32,
	ending_cursor_out int32,
	nwritten_out int32,
) int32 {
	i.abilog.Printf("http_downstream_original_header_names: req=%d cursor=%d", req_handle, cursor)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_original_header_names: invalid request handle")
		return XqdErrInvalidHandle
	}

	// Use the captured original headers
	headers := req.originalHeaders
	if len(headers) == 0 {
		// No original headers captured (this shouldn't happen for downstream requests)
		i.memory.PutUint32(0, int64(ending_cursor_out)) // -1 indicates end
		i.memory.PutUint32(0, int64(nwritten_out))
		return XqdStatusOK
	}

	// Write header names to buffer, separated by null bytes, starting from cursor position
	written := 0
	currentCursor := int(cursor)

	for currentCursor < len(headers) && written < int(buf_len) {
		headerName := headers[currentCursor]
		nullTerminated := headerName + "\x00"

		// Check if we have space for this header
		if written+len(nullTerminated) > int(buf_len) {
			break
		}

		// Write the header name with null terminator
		n, err := i.memory.WriteAt([]byte(nullTerminated), int64(buf_ptr)+int64(written))
		if err != nil {
			i.abilog.Printf("http_downstream_original_header_names: write error: %v", err)
			return XqdError
		}

		written += n
		currentCursor++
	}

	// Set ending cursor (next index to read, or -1 if done)
	var endingCursor uint32
	if currentCursor >= len(headers) {
		endingCursor = 0xFFFFFFFF // -1 as u32, indicates no more data
	} else {
		endingCursor = uint32(currentCursor)
	}

	i.memory.PutUint32(endingCursor, int64(ending_cursor_out))
	i.memory.PutUint32(uint32(written), int64(nwritten_out))

	i.abilog.Printf("http_downstream_original_header_names: wrote %d bytes, next cursor=%d", written, endingCursor)
	return XqdStatusOK
}

// xqd_http_downstream_original_header_count returns the number of headers in the original downstream request.
//
// Signature: (handle: RequestHandle, count_out: *mut u32) -> FastlyStatus
func (i *Instance) xqd_http_downstream_original_header_count(req_handle int32, count_out int32) int32 {
	i.abilog.Printf("http_downstream_original_header_count: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_original_header_count: invalid request handle")
		return XqdErrInvalidHandle
	}

	count := uint32(len(req.originalHeaders))
	i.memory.PutUint32(count, int64(count_out))

	i.abilog.Printf("http_downstream_original_header_count: count=%d", count)
	return XqdStatusOK
}

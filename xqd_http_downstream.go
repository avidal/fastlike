package fastlike

import (
	"crypto/tls"
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
		i.memory.PutUint32(0xFFFFFFFF, int64(ending_cursor_out)) // -1 indicates end
		i.memory.PutUint32(0, int64(nwritten_out))
		return XqdStatusOK
	}

	// Check if cursor is -1 (0xFFFFFFFF) or out of bounds, indicating no more data
	if cursor < 0 || int(cursor) >= len(headers) {
		i.memory.PutUint32(0xFFFFFFFF, int64(ending_cursor_out)) // -1 indicates end
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

// xqd_http_downstream_tls_cipher_openssl_name returns the OpenSSL cipher name for the downstream TLS connection.
// Returns XqdErrNone if TLS metadata is not available (e.g., request was not over TLS).
//
// Signature: (handle: RequestHandle, cipher_out: *mut u8, cipher_max_len: u32, nwritten_out: *mut u32) -> FastlyStatus
func (i *Instance) xqd_http_downstream_tls_cipher_openssl_name(
	req_handle int32,
	cipher_out int32,
	cipher_max_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Printf("http_downstream_tls_cipher_openssl_name: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_tls_cipher_openssl_name: invalid request handle")
		return XqdErrInvalidHandle
	}

	// Check if TLS state is available
	if req.tlsState == nil {
		i.abilog.Printf("http_downstream_tls_cipher_openssl_name: TLS metadata not available")
		i.memory.PutUint32(0, int64(nwritten_out))
		return XqdErrNone
	}

	// Get the cipher suite name
	cipherName := tls.CipherSuiteName(req.tlsState.CipherSuite)

	// Check buffer size
	if int32(len(cipherName)) > cipher_max_len {
		i.memory.PutUint32(uint32(len(cipherName)), int64(nwritten_out))
		return XqdErrBufferLength
	}

	// Write cipher name to guest memory
	nwritten, err := i.memory.WriteAt([]byte(cipherName), int64(cipher_out))
	if err != nil {
		i.abilog.Printf("http_downstream_tls_cipher_openssl_name: write error: %v", err)
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	i.abilog.Printf("http_downstream_tls_cipher_openssl_name: cipher=%s", cipherName)
	return XqdStatusOK
}

// xqd_http_downstream_tls_protocol returns the TLS protocol version for the downstream connection.
// Returns XqdErrNone if TLS metadata is not available.
//
// Signature: (handle: RequestHandle, protocol_out: *mut u8, protocol_max_len: u32, nwritten_out: *mut u32) -> FastlyStatus
func (i *Instance) xqd_http_downstream_tls_protocol(
	req_handle int32,
	protocol_out int32,
	protocol_max_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Printf("http_downstream_tls_protocol: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_tls_protocol: invalid request handle")
		return XqdErrInvalidHandle
	}

	// Check if TLS state is available
	if req.tlsState == nil {
		i.abilog.Printf("http_downstream_tls_protocol: TLS metadata not available")
		i.memory.PutUint32(0, int64(nwritten_out))
		return XqdErrNone
	}

	// Map TLS version to string
	var protocolName string
	switch req.tlsState.Version {
	case tls.VersionTLS10:
		protocolName = "TLSv1.0"
	case tls.VersionTLS11:
		protocolName = "TLSv1.1"
	case tls.VersionTLS12:
		protocolName = "TLSv1.2"
	case tls.VersionTLS13:
		protocolName = "TLSv1.3"
	default:
		protocolName = "unknown"
	}

	// Check buffer size
	if int32(len(protocolName)) > protocol_max_len {
		i.memory.PutUint32(uint32(len(protocolName)), int64(nwritten_out))
		return XqdErrBufferLength
	}

	// Write protocol name to guest memory
	nwritten, err := i.memory.WriteAt([]byte(protocolName), int64(protocol_out))
	if err != nil {
		i.abilog.Printf("http_downstream_tls_protocol: write error: %v", err)
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	i.abilog.Printf("http_downstream_tls_protocol: protocol=%s", protocolName)
	return XqdStatusOK
}

// xqd_http_downstream_tls_client_servername returns the SNI (Server Name Indication) from the TLS connection.
// Returns XqdErrNone if TLS metadata is not available.
//
// Signature: (handle: RequestHandle, sni_out: *mut u8, sni_max_len: u32, nwritten_out: *mut u32) -> FastlyStatus
func (i *Instance) xqd_http_downstream_tls_client_servername(
	req_handle int32,
	sni_out int32,
	sni_max_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Printf("http_downstream_tls_client_servername: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_tls_client_servername: invalid request handle")
		return XqdErrInvalidHandle
	}

	// Check if TLS state is available
	if req.tlsState == nil {
		i.abilog.Printf("http_downstream_tls_client_servername: TLS metadata not available")
		i.memory.PutUint32(0, int64(nwritten_out))
		return XqdErrNone
	}

	// Get the SNI server name
	serverName := req.tlsState.ServerName

	// Check buffer size
	if int32(len(serverName)) > sni_max_len {
		i.memory.PutUint32(uint32(len(serverName)), int64(nwritten_out))
		return XqdErrBufferLength
	}

	// Write server name to guest memory
	nwritten, err := i.memory.WriteAt([]byte(serverName), int64(sni_out))
	if err != nil {
		i.abilog.Printf("http_downstream_tls_client_servername: write error: %v", err)
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	i.abilog.Printf("http_downstream_tls_client_servername: sni=%s", serverName)
	return XqdStatusOK
}

// xqd_http_downstream_tls_client_hello returns the raw TLS Client Hello message.
// Returns XqdErrNone if TLS metadata is not available.
// Note: Go's crypto/tls does not expose the raw Client Hello, so this always returns not available.
//
// Signature: (handle: RequestHandle, chello_out: *mut u8, chello_max_len: u32, nwritten_out: *mut u32) -> FastlyStatus
func (i *Instance) xqd_http_downstream_tls_client_hello(
	req_handle int32,
	chello_out int32,
	chello_max_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Printf("http_downstream_tls_client_hello: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_tls_client_hello: invalid request handle")
		return XqdErrInvalidHandle
	}

	// Go's crypto/tls does not expose the raw Client Hello message
	// Always return not available
	i.abilog.Printf("http_downstream_tls_client_hello: Client Hello not available (not supported in Go)")
	i.memory.PutUint32(0, int64(nwritten_out))
	return XqdErrNone
}

// xqd_http_downstream_tls_raw_client_certificate returns the raw client certificate (DER-encoded).
// Returns XqdErrNone if TLS metadata is not available or no client certificate was provided.
//
// Signature: (handle: RequestHandle, cert_out: *mut u8, cert_max_len: u32, nwritten_out: *mut u32) -> FastlyStatus
func (i *Instance) xqd_http_downstream_tls_raw_client_certificate(
	req_handle int32,
	cert_out int32,
	cert_max_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Printf("http_downstream_tls_raw_client_certificate: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_tls_raw_client_certificate: invalid request handle")
		return XqdErrInvalidHandle
	}

	// Check if TLS state is available
	if req.tlsState == nil {
		i.abilog.Printf("http_downstream_tls_raw_client_certificate: TLS metadata not available")
		i.memory.PutUint32(0, int64(nwritten_out))
		return XqdErrNone
	}

	// Check if client certificates are available
	if len(req.tlsState.PeerCertificates) == 0 {
		i.abilog.Printf("http_downstream_tls_raw_client_certificate: No client certificate provided")
		i.memory.PutUint32(0, int64(nwritten_out))
		return XqdErrNone
	}

	// Get the first (leaf) certificate in DER format
	certDER := req.tlsState.PeerCertificates[0].Raw

	// Check buffer size
	if int32(len(certDER)) > cert_max_len {
		i.memory.PutUint32(uint32(len(certDER)), int64(nwritten_out))
		return XqdErrBufferLength
	}

	// Write certificate to guest memory
	nwritten, err := i.memory.WriteAt(certDER, int64(cert_out))
	if err != nil {
		i.abilog.Printf("http_downstream_tls_raw_client_certificate: write error: %v", err)
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	i.abilog.Printf("http_downstream_tls_raw_client_certificate: wrote %d bytes", nwritten)
	return XqdStatusOK
}

// xqd_http_downstream_tls_client_cert_verify_result returns the result of client certificate verification.
// Writes the verification result to verify_result_out pointer.
//
// Signature: (handle: RequestHandle, verify_result_out: *mut ClientCertVerifyResult) -> FastlyStatus
func (i *Instance) xqd_http_downstream_tls_client_cert_verify_result(req_handle int32, verify_result_out int32) int32 {
	i.abilog.Printf("http_downstream_tls_client_cert_verify_result: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_tls_client_cert_verify_result: invalid request handle")
		return XqdErrInvalidHandle
	}

	var result uint32

	// Check if TLS state is available or if no client certificate was provided
	if req.tlsState == nil || len(req.tlsState.PeerCertificates) == 0 {
		i.abilog.Printf("http_downstream_tls_client_cert_verify_result: No client certificate or TLS metadata not available")
		// Write CertificateMissing if no certificate was provided or TLS not available
		result = uint32(ClientCertVerifyResultCertificateMissing)
	} else {
		// In Go's TLS, if we reach here, the certificate was verified successfully
		// (unless VerifyConnection or VerifyPeerCertificate callbacks were used)
		// For simplicity, we return Ok if certificates are present and the connection succeeded
		i.abilog.Printf("http_downstream_tls_client_cert_verify_result: certificate verified")
		result = uint32(ClientCertVerifyResultOk)
	}

	// Write the result to the output pointer
	i.memory.PutUint32(result, int64(verify_result_out))
	return XqdStatusOK
}

// xqd_http_downstream_client_h2_fingerprint returns the HTTP/2 fingerprint for the downstream connection.
// Returns XqdErrNone if fingerprinting data is not available.
// Note: HTTP/2 fingerprinting is not implemented in Go's http library, so this always returns not available.
//
// Signature: (handle: RequestHandle, h2fp_out: *mut u8, h2fp_max_len: u32, nwritten_out: *mut u32) -> FastlyStatus
func (i *Instance) xqd_http_downstream_client_h2_fingerprint(
	req_handle int32,
	h2fp_out int32,
	h2fp_max_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Printf("http_downstream_client_h2_fingerprint: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_client_h2_fingerprint: invalid request handle")
		return XqdErrInvalidHandle
	}

	// HTTP/2 fingerprinting requires deep inspection of HTTP/2 frames and settings
	// Go's http library does not expose this level of detail
	i.abilog.Printf("http_downstream_client_h2_fingerprint: HTTP/2 fingerprint not available (not implemented)")
	i.memory.PutUint32(0, int64(nwritten_out))
	return XqdErrNone
}

// xqd_http_downstream_client_oh_fingerprint returns the ordered headers fingerprint for the downstream connection.
// Returns XqdErrNone if fingerprinting data is not available.
// Note: This function returns not available as ordered headers fingerprinting is not implemented.
//
// Signature: (handle: RequestHandle, ohfp_out: *mut u8, ohfp_max_len: u32, nwritten_out: *mut u32) -> FastlyStatus
func (i *Instance) xqd_http_downstream_client_oh_fingerprint(
	req_handle int32,
	ohfp_out int32,
	ohfp_max_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Printf("http_downstream_client_oh_fingerprint: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_client_oh_fingerprint: invalid request handle")
		return XqdErrInvalidHandle
	}

	// Ordered headers fingerprinting requires analyzing the exact order of HTTP headers
	// While we capture original header order, generating a fingerprint requires specific algorithms
	i.abilog.Printf("http_downstream_client_oh_fingerprint: ordered headers fingerprint not available (not implemented)")
	i.memory.PutUint32(0, int64(nwritten_out))
	return XqdErrNone
}

// xqd_http_downstream_tls_ja3_md5 returns the JA3 TLS fingerprint (MD5 hash) for the downstream connection.
// Returns XqdErrNone if fingerprinting data is not available.
// Note: JA3 fingerprinting requires access to raw TLS handshake data which Go's crypto/tls does not expose.
//
// Signature: (handle: RequestHandle, ja3_md5_out: *mut u8, nwritten_out: *mut usize) -> FastlyStatus
// The buffer must be at least 16 bytes (MD5 hash size)
func (i *Instance) xqd_http_downstream_tls_ja3_md5(req_handle int32, ja3_md5_out int32, nwritten_out int32) int32 {
	i.abilog.Printf("http_downstream_tls_ja3_md5: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_tls_ja3_md5: invalid request handle")
		return XqdErrInvalidHandle
	}

	// JA3 fingerprinting requires:
	// - TLS version
	// - Accepted ciphers
	// - List of extensions
	// - Elliptic curves
	// - Elliptic curve formats
	// Go's crypto/tls does not expose the raw Client Hello message needed to generate JA3
	i.abilog.Printf("http_downstream_tls_ja3_md5: JA3 fingerprint not available (not implemented)")
	i.memory.PutUint32(0, int64(nwritten_out))
	return XqdErrNone
}

// xqd_http_downstream_tls_ja4 returns the JA4 TLS fingerprint for the downstream connection.
// Returns XqdErrNone if fingerprinting data is not available.
// Note: JA4 fingerprinting requires access to raw TLS handshake data which Go's crypto/tls does not expose.
//
// Signature: (handle: RequestHandle, ja4_out: *mut u8, ja4_max_len: u32, nwritten_out: *mut u32) -> FastlyStatus
func (i *Instance) xqd_http_downstream_tls_ja4(
	req_handle int32,
	ja4_out int32,
	ja4_max_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Printf("http_downstream_tls_ja4: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_tls_ja4: invalid request handle")
		return XqdErrInvalidHandle
	}

	// JA4 is an updated version of JA3 with improved fingerprinting
	// Like JA3, it requires raw TLS handshake data which Go's crypto/tls does not expose
	i.abilog.Printf("http_downstream_tls_ja4: JA4 fingerprint not available (not implemented)")
	i.memory.PutUint32(0, int64(nwritten_out))
	return XqdErrNone
}

// xqd_http_downstream_client_request_id returns the unique request ID for the downstream connection.
// Returns XqdErrNone if the request ID is not available.
// Note: This is a stub that returns a test request ID.
//
// Signature: (handle: RequestHandle, reqid_out: *mut u8, reqid_max_len: u32, nwritten_out: *mut u32) -> FastlyStatus
func (i *Instance) xqd_http_downstream_client_request_id(
	req_handle int32,
	reqid_out int32,
	reqid_max_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Printf("http_downstream_client_request_id: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_client_request_id: invalid request handle")
		return XqdErrInvalidHandle
	}

	// Generate a test request ID
	// In production, this would be a unique identifier from Fastly's infrastructure
	requestID := "00000000-0000-0000-0000-000000000000"

	// Check buffer size
	if int32(len(requestID)) > reqid_max_len {
		i.memory.PutUint32(uint32(len(requestID)), int64(nwritten_out))
		return XqdErrBufferLength
	}

	// Write request ID to guest memory
	nwritten, err := i.memory.WriteAt([]byte(requestID), int64(reqid_out))
	if err != nil {
		i.abilog.Printf("http_downstream_client_request_id: write error: %v", err)
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	i.abilog.Printf("http_downstream_client_request_id: request_id=%s", requestID)
	return XqdStatusOK
}

// xqd_http_downstream_client_ip_addr returns the client IP address for the downstream connection.
// The buffer must be at least 16 bytes (for IPv6).
//
// Signature: (handle: RequestHandle, addr_octets_out: *mut u8, nwritten_out: *mut usize) -> FastlyStatus
func (i *Instance) xqd_http_downstream_client_ip_addr(req_handle int32, addr_octets_out int32, nwritten_out int32) int32 {
	i.abilog.Printf("http_downstream_client_ip_addr: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_client_ip_addr: invalid request handle")
		return XqdErrInvalidHandle
	}

	// Use the existing implementation from xqd_request.go
	// Get client IP from request's RemoteAddr
	return i.xqd_req_downstream_client_ip_addr(addr_octets_out, nwritten_out)
}

// xqd_http_downstream_server_ip_addr returns the server IP address for the downstream connection.
// The buffer must be at least 16 bytes (for IPv6).
//
// Signature: (handle: RequestHandle, addr_octets_out: *mut u8, nwritten_out: *mut usize) -> FastlyStatus
func (i *Instance) xqd_http_downstream_server_ip_addr(req_handle int32, addr_octets_out int32, nwritten_out int32) int32 {
	i.abilog.Printf("http_downstream_server_ip_addr: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_server_ip_addr: invalid request handle")
		return XqdErrInvalidHandle
	}

	// Use the existing implementation from xqd_request.go
	return i.xqd_req_downstream_server_ip_addr(addr_octets_out, nwritten_out)
}

// xqd_http_downstream_client_ddos_detected checks if DDoS attack was detected for this client.
// Writes 0 (false) or 1 (true) to ddos_detected_out pointer.
//
// Signature: (handle: RequestHandle, ddos_detected_out: *mut u32) -> FastlyStatus
func (i *Instance) xqd_http_downstream_client_ddos_detected(req_handle int32, ddos_detected_out int32) int32 {
	i.abilog.Printf("http_downstream_client_ddos_detected: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_client_ddos_detected: invalid request handle")
		return XqdErrInvalidHandle
	}

	// DDoS detection is not implemented in local testing
	// Always write 0 (false) to the output pointer
	i.memory.WriteUint32(ddos_detected_out, 0)
	i.abilog.Printf("http_downstream_client_ddos_detected: DDoS detection not available (always returns false)")
	return XqdStatusOK
}

// xqd_http_downstream_compliance_region returns the compliance region for the downstream connection.
// Returns XqdErrNone if compliance region is not available.
//
// Signature: (handle: RequestHandle, region_out: *mut u8, region_max_len: u32, nwritten_out: *mut u32) -> FastlyStatus
func (i *Instance) xqd_http_downstream_compliance_region(
	req_handle int32,
	region_out int32,
	region_max_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Printf("http_downstream_compliance_region: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_compliance_region: invalid request handle")
		return XqdErrInvalidHandle
	}

	// Use the configured compliance region
	region := i.complianceRegion

	// Check buffer size
	if int32(len(region)) > region_max_len {
		i.memory.PutUint32(uint32(len(region)), int64(nwritten_out))
		return XqdErrBufferLength
	}

	// Write region to guest memory
	nwritten, err := i.memory.WriteAt([]byte(region), int64(region_out))
	if err != nil {
		i.abilog.Printf("http_downstream_compliance_region: write error: %v", err)
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	i.abilog.Printf("http_downstream_compliance_region: region=%s", region)
	return XqdStatusOK
}

// xqd_http_downstream_fastly_key_is_valid checks if the request has a valid Fastly-Key for purging.
// Writes 0 (false) or 1 (true) to is_valid_out pointer.
//
// Signature: (handle: RequestHandle, is_valid_out: *mut u32) -> FastlyStatus
func (i *Instance) xqd_http_downstream_fastly_key_is_valid(req_handle int32, is_valid_out int32) int32 {
	i.abilog.Printf("http_downstream_fastly_key_is_valid: req=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		i.abilog.Printf("http_downstream_fastly_key_is_valid: invalid request handle")
		return XqdErrInvalidHandle
	}

	// In local testing, we don't validate Fastly-Key headers
	// Return 0 (false) to match Viceroy's behavior in local testing
	i.memory.WriteUint32(is_valid_out, 0)
	i.abilog.Printf("http_downstream_fastly_key_is_valid: Fastly-Key validation not available (always returns false)")
	return XqdStatusOK
}

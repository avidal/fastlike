package fastlike

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"strings"
)

// xqd_init initializes the XQD ABI and verifies the protocol version.
// The XQD ABI currently only supports version 1.
// Returns XqdErrUnsupported if the version is not supported, XqdStatusOK otherwise.
func (i *Instance) xqd_init(abiv int64) int32 {
	i.abilog.Printf("init: version=%d\n", abiv)
	const supportedABIVersion = 1
	if abiv != supportedABIVersion {
		return XqdErrUnsupported
	}

	return XqdStatusOK
}

// xqd_req_body_downstream_get converts the downstream HTTP request into a (request, body) handle pair.
// Captures TLS state, original header names, and sets up the request URL with scheme and host.
// This is typically the first XQD call made by guest programs to access the incoming HTTP request.
// Returns XqdStatusOK on success.
func (i *Instance) xqd_req_body_downstream_get(request_handle_out int32, body_handle_out int32) int32 {
	// Create a new request handle and clone the downstream request into it
	rhid, rh := i.requests.New()
	rh.Request = i.ds_request.Clone(context.Background())

	// The downstream request URL doesn't include host or scheme, so we populate them
	rh.URL.Host = i.ds_request.Host

	if i.secureFn(i.ds_request) {
		rh.URL.Scheme = "https"
		rh.Header.Set("fastly-ssl", "1")
	} else {
		rh.URL.Scheme = "http"
	}

	// Capture original header names in their original order for downstream_original_header_names
	// Convert to lowercase to match HTTP/2 convention (headers are case-insensitive)
	// Sort alphabetically since Go map iteration order is non-deterministic
	rh.originalHeaders = make([]string, 0, len(i.ds_request.Header))
	for name := range i.ds_request.Header {
		rh.originalHeaders = append(rh.originalHeaders, strings.ToLower(name))
	}
	// Sort to ensure consistent ordering
	sort.Strings(rh.originalHeaders)

	// Capture TLS connection state if the request was over TLS
	if i.ds_request.TLS != nil {
		rh.tlsState = i.ds_request.TLS
	}

	// Create a body handle by copying the downstream request body into a buffer.
	// NOTE: We use NewBuffer instead of NewReader to avoid a bug where subrequests
	// don't properly forward the body. This copies the entire body into memory,
	// which works around the issue but may not be ideal for very large bodies.
	bhid, bh := i.bodies.NewBuffer()
	if i.ds_request.Body != nil {
		_, _ = io.Copy(bh, i.ds_request.Body)
		_ = i.ds_request.Body.Close()
	}

	i.memory.PutUint32(uint32(rhid), int64(request_handle_out))
	i.memory.PutUint32(uint32(bhid), int64(body_handle_out))

	// Store the downstream request handle for implicit downstream request operations
	i.downstreamRequestHandle = int32(rhid)

	i.abilog.Printf("req_body_downstream_get: rh=%d bh=%d", rhid, bhid)

	return XqdStatusOK
}

// xqd_resp_send_downstream sends the response and body to the downstream client.
// Copies response headers and body to the output stream. Streaming mode is not currently supported.
// Respects the framing headers mode set on the response handle.
// Returns XqdErrInvalidHandle if handles are invalid, XqdErrUnsupported for streaming, XqdStatusOK on success.
func (i *Instance) xqd_resp_send_downstream(whandle int32, bhandle int32, stream int32) int32 {
	if stream != 0 {
		i.abilog.Printf("resp_send_downstream: streaming unsupported")
		return XqdErrUnsupported
	}

	w, b := i.responses.Get(int(whandle)), i.bodies.Get(int(bhandle))
	if w == nil {
		i.abilog.Printf("resp_send_downstream: invalid response handle %d", whandle)
		return XqdErrInvalidHandle
	} else if b == nil {
		i.abilog.Printf("resp_send_downstream: invalid body handle %d", bhandle)
		return XqdErrInvalidHandle
	}
	defer func() { _ = b.Close() }()

	// Clone headers so we don't modify the original
	headers := w.Header.Clone()

	// Apply framing mode - validate and potentially filter framing headers
	effectiveMode := validateAndApplyFramingMode(headers, w.framingHeadersMode, func(format string, args ...interface{}) {
		i.abilog.Printf("resp_send_downstream: "+format, args...)
	})

	i.abilog.Printf("resp_send_downstream: framing_mode=%d effective_mode=%d", w.framingHeadersMode, effectiveMode)

	for k, v := range headers {
		i.ds_response.Header()[k] = v
	}

	i.ds_response.WriteHeader(w.StatusCode)

	_, err := io.Copy(i.ds_response, b)
	if err != nil {
		i.abilog.Printf("resp_send_downstream: copy err, got %s", err.Error())
		return XqdError
	}

	return XqdStatusOK
}

// xqd_req_downstream_client_ip_addr extracts the client IP address from the downstream request.
// Parses the RemoteAddr field and writes the IP octets to guest memory.
// IPv4 addresses are returned in 4-byte format, IPv6 in 16-byte format.
// Returns XqdStatusOK on success or XqdError on failure.
func (i *Instance) xqd_req_downstream_client_ip_addr(octets_out int32, nwritten_out int32) int32 {
	// Extract IP from RemoteAddr (format is typically "IP:port")
	hostPort := strings.SplitN(i.ds_request.RemoteAddr, ":", 2)
	ip := net.ParseIP(hostPort[0])
	i.abilog.Printf("req_downstream_client_ip_addr: remoteaddr=%s, ip=%q\n", i.ds_request.RemoteAddr, ip)

	// If we can't parse the IP, return success with zero bytes written
	if ip == nil {
		return XqdStatusOK
	}

	// Convert IPv6-mapped IPv4 addresses (::ffff:x.x.x.x) to native IPv4 (4 bytes)
	// This ensures IPv4 addresses are always returned in 4-byte format
	if ipv4 := ip.To4(); ipv4 != nil {
		ip = ipv4
	}

	// Write the IP bytes to guest memory. net.IP is a byte slice that can be written directly.
	nwritten, err := i.memory.WriteAt(ip, int64(octets_out))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))

	return XqdStatusOK
}

// xqd_req_downstream_server_ip_addr returns the server's IP address that received the downstream request.
// For local testing, this returns 127.0.0.1. In a production Fastly environment, this would be
// the actual server IP that accepted the connection.
// Returns XqdStatusOK on success or XqdError on failure.
func (i *Instance) xqd_req_downstream_server_ip_addr(octets_out int32, nwritten_out int32) int32 {
	i.abilog.Printf("req_downstream_server_ip_addr")

	// Return localhost (127.0.0.1) for local testing
	// Use a byte slice to ensure proper 4-byte IPv4 format
	ip := []byte{127, 0, 0, 1}

	// Write the IP to memory
	nwritten, err := i.memory.WriteAt(ip, int64(octets_out))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	return XqdStatusOK
}

// xqd_uap_parse parses a user agent string into its component parts.
// Extracts family, major, minor, and patch version information and writes them to guest memory.
// Returns XqdStatusOK on success or XqdError if memory operations fail.
func (i *Instance) xqd_uap_parse(
	addr int32, size int32,
	family_out, family_maxlen, family_nwritten_out int32,
	major_out, major_maxlen, major_nwritten_out int32,
	minor_out, minor_maxlen, minor_nwritten_out int32,
	patch_out, patch_maxlen, patch_nwritten_out int32,
) int32 {
	buf := make([]byte, size)
	_, err := i.memory.ReadAt(buf, int64(addr))
	if err != nil {
		i.abilog.Printf("uap_parse: read err, got %s", err.Error())
		return XqdError
	}

	useragent := string(buf)
	i.abilog.Printf("uap_parse: useragent=%s\n", useragent)

	ua := i.uaparser(useragent)

	family_nwritten, err := i.memory.WriteAt([]byte(ua.Family), int64(family_out))
	if err != nil {
		i.abilog.Printf("uap_parse: family write err, got %s", err.Error())
		return XqdError
	}
	i.memory.PutUint32(uint32(family_nwritten), int64(family_nwritten_out))

	major_nwritten, err := i.memory.WriteAt([]byte(ua.Major), int64(major_out))
	if err != nil {
		i.abilog.Printf("uap_parse: major write err, got %s", err.Error())
		return XqdError
	}
	i.memory.PutUint32(uint32(major_nwritten), int64(major_nwritten_out))

	minor_nwritten, err := i.memory.WriteAt([]byte(ua.Minor), int64(minor_out))
	if err != nil {
		i.abilog.Printf("uap_parse: minor write err, got %s", err.Error())
		return XqdError
	}
	i.memory.PutUint32(uint32(minor_nwritten), int64(minor_nwritten_out))

	patch_nwritten, err := i.memory.WriteAt([]byte(ua.Patch), int64(patch_out))
	if err != nil {
		i.abilog.Printf("uap_parse: patch write err, got %s", err.Error())
		return XqdError
	}
	i.memory.PutUint32(uint32(patch_nwritten), int64(patch_nwritten_out))

	return XqdStatusOK
}

// logStubCall logs stub function calls with their arguments for debugging purposes.
// Used to track unimplemented or stubbed XQD ABI functions during development.
func logStubCall(logger *log.Logger, functionName string, args ...int32) {
	argStrings := []string{}
	for _, arg := range args {
		argStrings = append(argStrings, fmt.Sprintf("%d", arg))
	}

	logger.Printf("[STUB] %s: args=%q\n", functionName, argStrings)
}

// wasm1 creates a stub function that accepts one int32 argument.
// Returns a function that logs the call and returns XqdErrUnsupported (5).
// Used during development to identify unimplemented XQD functions.
func (i *Instance) wasm1(name string) func(a int32) int32 {
	return func(a int32) int32 {
		logStubCall(i.abilog, name, a)
		return XqdErrUnsupported
	}
}

// wasm6 creates a stub function that accepts six int32 arguments.
// Returns a function that logs the call and returns XqdErrUnsupported (5).
// Used during development to identify unimplemented XQD functions.
func (i *Instance) wasm6(name string) func(a, b, c, d, e, f int32) int32 {
	return func(a, b, c, d, e, f int32) int32 {
		logStubCall(i.abilog, name, a, b, c, d, e, f)
		return XqdErrUnsupported
	}
}

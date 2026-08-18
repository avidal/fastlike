package fastlike

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"sort"
)

// xqd_resp_new creates a new response handle and writes it to guest memory.
// Returns XqdStatusOK on success, or an error code if the handle cannot be created.
func (i *Instance) xqd_resp_new(handle_out int32) int32 {
	whid, _ := i.responses.New()
	i.abilog.Printf("resp_new handle=%d\n", whid)
	i.memory.PutUint32(uint32(whid), int64(handle_out))
	return XqdStatusOK
}

// xqd_resp_status_set sets the HTTP status code for a response handle.
// The status code must be in the range 100-999, otherwise returns XqdErrInvalidArgument.
func (i *Instance) xqd_resp_status_set(handle int32, status int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	// Validate HTTP status code (must be in range 100-999)
	if status < 100 || status > 999 {
		i.abilog.Printf("resp_status_set: invalid status code %d (must be 100-999)", status)
		return XqdErrInvalidArgument
	}

	i.abilog.Printf("resp_status_set: handle=%d status=%d", handle, status)

	w.StatusCode = int(status)
	w.Status = http.StatusText(w.StatusCode)
	return XqdStatusOK
}

// xqd_resp_status_get retrieves the HTTP status code from a response handle and writes it to guest memory.
// Returns XqdErrInvalidHandle if the handle is invalid, otherwise XqdStatusOK.
func (i *Instance) xqd_resp_status_get(handle int32, status_out int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_status_get: handle=%d status=%d", handle, w.StatusCode)
	// http_status is a u16 in the ABI; a 4-byte write would clobber the two
	// bytes following the guest's out-variable.
	i.memory.PutUint16(uint16(w.StatusCode), int64(status_out))
	return XqdStatusOK
}

// xqd_resp_version_set sets the HTTP protocol version for a response handle.
// Supports every version represented by the ABI: HTTP/0.9 through HTTP/3.
// Returns XqdErrInvalidArgument for unknown versions.
func (i *Instance) xqd_resp_version_set(handle int32, version int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	if version < Http09 || version > Http3 {
		i.abilog.Printf("resp_version_set: invalid version %d", version)
		return XqdErrInvalidArgument
	}

	i.abilog.Printf("resp_version_set: handle=%d version=%d", handle, version)

	// Store the version in the response handle
	w.version = version
	return XqdStatusOK
}

// xqd_resp_version_get retrieves the HTTP protocol version from a response handle and writes it to guest memory.
// Returns XqdErrInvalidHandle if the handle is invalid, otherwise XqdStatusOK.
func (i *Instance) xqd_resp_version_get(handle int32, version_out int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_version_get: handle=%d version=%d", handle, w.version)

	i.memory.PutUint32(uint32(w.version), int64(version_out))
	return XqdStatusOK
}

// xqd_resp_header_names_get retrieves response header names using cursor-based pagination.
// Returns a sorted list of header names, writing them to guest memory at the specified address.
func (i *Instance) xqd_resp_header_names_get(handle int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
	i.abilog.Printf("resp_header_names_get: handle=%d cursor=%d", handle, cursor)

	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	// Collect all header names from the response
	names := []string{}
	for headerName := range w.Header {
		names = append(names, headerName)
	}

	// Sort names alphabetically for consistent ordering (Go's map iteration is non-deterministic)
	sort.Strings(names)

	return xqd_multivalue(i.memory, names, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

// xqd_resp_header_value_get retrieves the first value of a specific response header.
// The header name is read from guest memory, and the first value is written back to guest memory.
// Returns XqdErrBufferLength if the buffer is too small, or XqdErrInvalidArgument if the
// name is too long or the header is absent.
func (i *Instance) xqd_resp_header_value_get(handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, nwritten_out int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	if !validHTTPHeaderNameSize(name_size) {
		i.abilog.Printf("resp_header_value_get: invalid header name length: %d bytes (max %d)\n", name_size, maxHTTPHeaderNameLen)
		return XqdErrInvalidArgument
	}

	buf := make([]byte, name_size)
	_, err := i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}
	if !validHTTPHeaderName(buf) {
		return XqdErrInvalidArgument
	}

	header := http.CanonicalHeaderKey(string(buf))

	i.abilog.Printf("resp_header_value_get: handle=%d header=%q\n", handle, header)

	values, exists := w.Header[header]
	if !exists || len(values) == 0 {
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

// xqd_resp_header_remove deletes a header from the response.
// Returns XqdErrInvalidArgument if the header name is too long or the header does not exist.
func (i *Instance) xqd_resp_header_remove(handle int32, name_addr int32, name_size int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	if !validHTTPHeaderNameSize(name_size) {
		i.abilog.Printf("resp_header_remove: invalid header name length: %d bytes (max %d)\n", name_size, maxHTTPHeaderNameLen)
		return XqdErrInvalidArgument
	}

	name := make([]byte, name_size)
	_, err := i.memory.ReadAt(name, int64(name_addr))
	if err != nil {
		return XqdError
	}
	if !validHTTPHeaderName(name) {
		return XqdErrInvalidArgument
	}

	header := http.CanonicalHeaderKey(string(name))

	// Map membership distinguishes an absent header from a present empty value.
	if _, exists := w.Header[header]; !exists {
		i.abilog.Printf("resp_header_remove: header %q not found\n", header)
		return XqdErrInvalidArgument
	}

	w.Header.Del(header)

	return XqdStatusOK
}

// xqd_resp_header_insert sets a response header, replacing any existing values for that header.
// Both the header name and value are read from guest memory. Returns XqdErrInvalidArgument if the name is too long.
func (i *Instance) xqd_resp_header_insert(handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	if !validHTTPHeaderNameSize(name_size) {
		i.abilog.Printf("resp_header_insert: invalid header name length: %d bytes (max %d)\n", name_size, maxHTTPHeaderNameLen)
		return XqdErrInvalidArgument
	}

	name := make([]byte, name_size)
	_, err := i.memory.ReadAt(name, int64(name_addr))
	if err != nil {
		return XqdError
	}
	if !validHTTPHeaderName(name) {
		return XqdErrInvalidArgument
	}

	value := make([]byte, value_size)
	_, err = i.memory.ReadAt(value, int64(value_addr))
	if err != nil {
		return XqdError
	}
	if !validHTTPHeaderValue(value) {
		return XqdErrInvalidArgument
	}

	header := http.CanonicalHeaderKey(string(name))

	i.abilog.Printf("resp_header_insert: handle=%d header=%q value=%q", handle, header, string(value))

	if httpHeaderNameCountAtLimit(len(w.Header)) {
		return XqdErrInvalidArgument
	}
	if w.Header == nil {
		w.Header = http.Header{}
	}

	w.Header.Set(header, string(value))

	return XqdStatusOK
}

// xqd_resp_header_append adds a value to a response header without replacing existing values.
// Both the header name and value are read from guest memory. Returns XqdErrInvalidArgument if the name is too long.
func (i *Instance) xqd_resp_header_append(handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	if !validHTTPHeaderNameSize(name_size) {
		i.abilog.Printf("resp_header_append: invalid header name length: %d bytes (max %d)\n", name_size, maxHTTPHeaderNameLen)
		return XqdErrInvalidArgument
	}

	name := make([]byte, name_size)
	_, err := i.memory.ReadAt(name, int64(name_addr))
	if err != nil {
		return XqdError
	}
	if !validHTTPHeaderName(name) {
		return XqdErrInvalidArgument
	}

	value := make([]byte, value_size)
	_, err = i.memory.ReadAt(value, int64(value_addr))
	if err != nil {
		return XqdError
	}
	if !validHTTPHeaderValue(value) {
		return XqdErrInvalidArgument
	}

	header := http.CanonicalHeaderKey(string(name))

	i.abilog.Printf("resp_header_append: handle=%d header=%q value=%q", handle, header, string(value))

	if httpHeaderNameCountAtLimit(len(w.Header)) {
		return XqdErrInvalidArgument
	}
	if w.Header == nil {
		w.Header = http.Header{}
	}

	w.Header.Add(header, string(value))

	return XqdStatusOK
}

// xqd_resp_header_values_get retrieves all values for a specific response header using cursor-based pagination.
// Values are returned in their stored order.
func (i *Instance) xqd_resp_header_values_get(handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}
	if !validHTTPHeaderNameSize(name_size) {
		return XqdErrInvalidArgument
	}

	buf := make([]byte, name_size)
	_, err := i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}
	if !validHTTPHeaderName(buf) {
		return XqdErrInvalidArgument
	}

	header := http.CanonicalHeaderKey(string(buf))

	i.abilog.Printf("resp_header_values_get: handle=%d header=%q cursor=%d\n", handle, header, cursor)

	// Get all values for this header (empty slice if not found)
	values, ok := w.Header[header]
	if !ok {
		values = []string{}
	}

	return xqd_multivalue(i.memory, values, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

// xqd_resp_header_values_set sets multiple values for a response header.
// The values are provided as a null-terminated list of strings in guest memory.
// Format in memory: "value1\0value2\0value3\0"
func (i *Instance) xqd_resp_header_values_set(handle int32, name_addr int32, name_size int32, values_addr int32, values_size int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}
	if !validHTTPHeaderNameSize(name_size) {
		return XqdErrInvalidArgument
	}

	// Read the header name
	buf := make([]byte, name_size)
	_, err := i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}
	if !validHTTPHeaderName(buf) {
		return XqdErrInvalidArgument
	}

	header := http.CanonicalHeaderKey(string(buf))

	// Read the values buffer. Values are separated and terminated by NUL
	// bytes. An empty buffer is the empty value list and clears the header.
	if values_size < 0 {
		return XqdErrInvalidArgument
	}
	values := make([][]byte, 0)
	if values_size > 0 {
		buf = make([]byte, values_size)
		_, err = i.memory.ReadAt(buf, int64(values_addr))
		if err != nil {
			return XqdError
		}

		parts := bytes.Split(buf, []byte("\x00"))
		values = parts[:len(parts)-1]
	}
	for _, value := range values {
		if !validHTTPHeaderValue(value) {
			return XqdErrInvalidArgument
		}
	}

	i.abilog.Printf("resp_header_values_set: handle=%d header=%q values=%q\n", handle, header, values)

	if httpHeaderNameCountAtLimit(len(w.Header)) {
		return XqdErrInvalidArgument
	}
	if w.Header == nil {
		w.Header = http.Header{}
	}

	w.Header.Del(header)

	for _, v := range values {
		w.Header.Add(header, string(v))
	}

	return XqdStatusOK
}

// xqd_resp_close consumes a response handle.
func (i *Instance) xqd_resp_close(handle int32) int32 {
	r := i.responses.Take(int(handle))
	if r == nil {
		i.abilog.Printf("resp_close: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_close: handle=%d", handle)
	return XqdStatusOK
}

// xqd_resp_framing_headers_mode_set controls how framing headers (Content-Length, Transfer-Encoding) are set.
// Mode 0 (Automatic): The HTTP library sets framing headers automatically.
// Mode 1 (ManuallyFromHeaders): User-provided framing headers are preserved and used.
func (i *Instance) xqd_resp_framing_headers_mode_set(handle int32, mode int32) int32 {
	// Validate response handle
	w := i.responses.Get(int(handle))
	if w == nil {
		i.abilog.Printf("resp_framing_headers_mode_set: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_framing_headers_mode_set: handle=%d mode=%d", handle, mode)

	// Validate mode value
	if mode != int32(FramingHeadersModeAutomatic) && mode != int32(FramingHeadersModeManuallyFromHeaders) {
		i.abilog.Printf("resp_framing_headers_mode_set: invalid mode %d", mode)
		return XqdErrInvalidArgument
	}

	w.framingHeadersMode = FramingHeadersMode(mode)
	return XqdStatusOK
}

// xqd_resp_http_keepalive_mode_set controls HTTP connection reuse (keepalive) mode.
// Mode 0 (Automatic) is supported: Go's http package handles keepalive automatically.
// Mode 1 (NoKeepalive) is not supported and returns XqdErrUnsupported.
func (i *Instance) xqd_resp_http_keepalive_mode_set(handle int32, mode int32) int32 {
	// Validate response handle
	w := i.responses.Get(int(handle))
	if w == nil {
		i.abilog.Printf("resp_http_keepalive_mode_set: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_http_keepalive_mode_set: handle=%d mode=%d", handle, mode)

	const (
		keepaliveModeAutomatic   = 0
		keepaliveModeNoKeepalive = 1
	)

	if mode != keepaliveModeAutomatic {
		i.abilog.Printf("resp_http_keepalive_mode_set: no-keepalive mode not supported")
		return XqdErrUnsupported
	}

	return XqdStatusOK
}

// xqd_resp_get_addr_dest_ip returns the destination IP address for the backend request.
// This extracts the IP from the response's RemoteAddr field and writes it to guest memory.
// IPv4 addresses are returned in 4-byte format, IPv6 in 16-byte format.
// Returns XqdErrNone if no remote address is available, XqdStatusOK on success.
func (i *Instance) xqd_resp_get_addr_dest_ip(handle int32, addr_octets_out int32, nwritten_out int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		i.abilog.Printf("resp_get_addr_dest_ip: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_get_addr_dest_ip: handle=%d", handle)

	// Check if remote address is available
	if w.RemoteAddr == "" {
		i.abilog.Printf("resp_get_addr_dest_ip: no remote address available")
		return XqdErrNone
	}

	// Parse the remote address (format is "IP:port")
	host, _, err := net.SplitHostPort(w.RemoteAddr)
	if err != nil {
		i.abilog.Printf("resp_get_addr_dest_ip: failed to parse remote address: %v", err)
		return XqdErrNone
	}

	// Parse the IP address
	ip := net.ParseIP(host)
	if ip == nil {
		i.abilog.Printf("resp_get_addr_dest_ip: failed to parse IP address: %s", host)
		return XqdErrNone
	}

	// Determine the IP format (IPv4 or IPv6)
	var octets []byte
	if ip.To4() != nil {
		// IPv4 address - return 4 bytes
		octets = ip.To4()
	} else {
		// IPv6 address - return 16 bytes
		octets = ip.To16()
	}

	// Write octets to guest memory (buffer must be at least 16 bytes)
	nwritten, err := i.memory.WriteAt(octets, int64(addr_octets_out))
	if err != nil {
		return XqdError
	}

	// Write the number of bytes written
	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	return XqdStatusOK
}

// xqd_resp_get_addr_dest_port returns the destination port for the backend request.
// This extracts the port from the response's RemoteAddr field and writes it to guest memory as u16.
// Returns XqdErrNone if no remote address is available, XqdStatusOK on success.
func (i *Instance) xqd_resp_get_addr_dest_port(handle int32, port_out int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		i.abilog.Printf("resp_get_addr_dest_port: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_get_addr_dest_port: handle=%d", handle)

	// Check if remote address is available
	if w.RemoteAddr == "" {
		i.abilog.Printf("resp_get_addr_dest_port: no remote address available")
		return XqdErrNone
	}

	// Parse the remote address (format is "IP:port")
	_, portStr, err := net.SplitHostPort(w.RemoteAddr)
	if err != nil {
		i.abilog.Printf("resp_get_addr_dest_port: failed to parse remote address: %v", err)
		return XqdErrNone
	}

	// Convert port string to integer
	var port int
	_, err = fmt.Sscanf(portStr, "%d", &port)
	if err != nil {
		i.abilog.Printf("resp_get_addr_dest_port: failed to parse port: %v", err)
		return XqdErrNone
	}

	// Write the port (as u16)
	i.memory.PutUint16(uint16(port), int64(port_out))
	return XqdStatusOK
}

// xqd_resp_send_informational_response sends an HTTP 1xx informational response.
// Only 103 (Early Hints) is supported. Other 1xx status codes return XqdErrInvalidArgument.
//
// Note: In local testing, 103 Early Hints responses are logged but not actually sent.
func (i *Instance) xqd_resp_send_informational_response(resp_handle int32, status int32) int32 {
	i.abilog.Printf("resp_send_informational_response: resp_handle=%d status=%d", resp_handle, status)

	// Only 103 Early Hints is supported
	if status != 103 {
		i.abilog.Printf("resp_send_informational_response: only 103 Early Hints is supported, got %d", status)
		return XqdErrInvalidArgument
	}

	// Get the response handle to read headers
	w := i.responses.Get(int(resp_handle))
	if w == nil {
		i.abilog.Printf("resp_send_informational_response: invalid handle %d", resp_handle)
		return XqdErrInvalidHandle
	}

	// Log the 103 response (not sent in local testing)
	i.log.Printf("103 Early Hints response logged but not sent to client")
	for name, values := range w.Header {
		for _, value := range values {
			i.log.Printf("  %s: %s", name, value)
		}
	}

	return XqdStatusOK
}

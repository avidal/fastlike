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
	i.memory.PutUint32(uint32(w.StatusCode), int64(status_out))
	return XqdStatusOK
}

// xqd_resp_version_set sets the HTTP protocol version for a response handle.
// Only HTTP/0.9, HTTP/1.0, and HTTP/1.1 are supported. Returns XqdErrInvalidArgument for unsupported versions.
func (i *Instance) xqd_resp_version_set(handle int32, version int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	// Validate that the version is one of the supported HTTP versions
	if version != Http09 && version != Http10 && version != Http11 {
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
// If the header doesn't exist, nwritten_out is set to 0 but XqdStatusOK is returned.
// Returns XqdErrBufferLength if the buffer is too small, XqdErrInvalidArgument if the name is too long.
func (i *Instance) xqd_resp_header_value_get(handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, nwritten_out int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	// Validate header name length (MAX_HEADER_NAME_LEN = 65535)
	if name_size > 65535 {
		i.abilog.Printf("resp_header_value_get: header name too long: %d bytes (max 65535)\n", name_size)
		i.memory.PutUint32(0, int64(nwritten_out))
		return XqdErrInvalidArgument
	}

	buf := make([]byte, name_size)
	_, err := i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	header := http.CanonicalHeaderKey(string(buf))

	i.abilog.Printf("resp_header_value_get: handle=%d header=%q\n", handle, header)

	// Get the first value for this header (returns "" if not found)
	value := w.Header.Get(header)

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

	// Validate header name length (MAX_HEADER_NAME_LEN = 65535)
	if name_size > 65535 {
		i.abilog.Printf("resp_header_remove: header name too long: %d bytes (max 65535)\n", name_size)
		return XqdErrInvalidArgument
	}

	name := make([]byte, name_size)
	_, err := i.memory.ReadAt(name, int64(name_addr))
	if err != nil {
		return XqdError
	}

	header := http.CanonicalHeaderKey(string(name))

	// Check if the header exists before removing
	if w.Header.Get(header) == "" {
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

	// Validate header name length (MAX_HEADER_NAME_LEN = 65535)
	if name_size > 65535 {
		i.abilog.Printf("resp_header_insert: header name too long: %d bytes (max 65535)\n", name_size)
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

	i.abilog.Printf("resp_header_insert: handle=%d header=%q value=%q", handle, header, string(value))

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

	// Validate header name length (MAX_HEADER_NAME_LEN = 65535)
	if name_size > 65535 {
		i.abilog.Printf("resp_header_append: header name too long: %d bytes (max 65535)\n", name_size)
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

	i.abilog.Printf("resp_header_append: handle=%d header=%q value=%q", handle, header, string(value))

	if w.Header == nil {
		w.Header = http.Header{}
	}

	w.Header.Add(header, string(value))

	return XqdStatusOK
}

// xqd_resp_header_values_get retrieves all values for a specific response header using cursor-based pagination.
// Returns a sorted list of header values for the specified header name.
func (i *Instance) xqd_resp_header_values_get(handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	buf := make([]byte, name_size)
	_, err := i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	header := http.CanonicalHeaderKey(string(buf))

	i.abilog.Printf("resp_header_values_get: handle=%d header=%q cursor=%d\n", handle, header, cursor)

	// Get all values for this header (empty slice if not found)
	values, ok := w.Header[header]
	if !ok {
		values = []string{}
	}

	// Sort the values for consistent ordering (required for cursor-based pagination)
	sort.Strings(values)

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

	i.abilog.Printf("resp_header_values_set: handle=%d header=%q values=%q\n", handle, header, values)

	if w.Header == nil {
		w.Header = http.Header{}
	}

	w.Header.Del(header)

	for _, v := range values {
		w.Header.Add(header, string(v))
	}

	return XqdStatusOK
}

// xqd_resp_close marks a response to have the Connection: close header semantics.
// This indicates that the connection should be closed after the response is sent.
func (i *Instance) xqd_resp_close(handle int32) int32 {
	r := i.responses.Get(int(handle))
	if r == nil {
		i.abilog.Printf("resp_close: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_close: handle=%d", handle)
	r.Close = true
	return XqdStatusOK
}

// xqd_resp_framing_headers_mode_set controls how framing headers (Content-Length, Transfer-Encoding) are set.
// Mode 0 (Automatic) is supported: Go's http package automatically sets these headers.
// Mode 1 (ManuallyFromHeaders) is not supported and returns XqdErrUnsupported.
func (i *Instance) xqd_resp_framing_headers_mode_set(handle int32, mode int32) int32 {
	// Validate response handle
	w := i.responses.Get(int(handle))
	if w == nil {
		i.abilog.Printf("resp_framing_headers_mode_set: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_framing_headers_mode_set: handle=%d mode=%d", handle, mode)

	const (
		framingModeAutomatic          = 0
		framingModeManuallyFromHeaders = 1
	)

	if mode != framingModeAutomatic {
		i.abilog.Printf("resp_framing_headers_mode_set: manual mode not supported")
		return XqdErrUnsupported
	}

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
		keepaliveModeAutomatic = 0
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

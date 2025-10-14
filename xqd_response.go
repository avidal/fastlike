package fastlike

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"sort"
)

func (i *Instance) xqd_resp_new(handle_out int32) int32 {
	whid, _ := i.responses.New()
	i.abilog.Printf("resp_new handle=%d\n", whid)
	i.memory.PutUint32(uint32(whid), int64(handle_out))
	return XqdStatusOK
}

func (i *Instance) xqd_resp_status_set(handle int32, status int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_status_set: handle=%d status=%d", handle, status)

	w.StatusCode = int(status)
	w.Status = http.StatusText(w.StatusCode)
	return XqdStatusOK
}

func (i *Instance) xqd_resp_status_get(handle int32, status_out int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_status_get: handle=%d status=%d", handle, w.StatusCode)
	i.memory.PutUint32(uint32(w.StatusCode), int64(status_out))
	return XqdStatusOK
}

func (i *Instance) xqd_resp_version_set(handle int32, version int32) int32 {
	i.abilog.Printf("resp_version_set: handle=%d version=%d", handle, version)

	if i.responses.Get(int(handle)) == nil {
		return XqdErrInvalidHandle
	}

	if int32(version) != Http11 {
		i.abilog.Printf("resp_version_set: unsupported version=%d", version)
	}

	return XqdStatusOK
}

func (i *Instance) xqd_resp_version_get(handle int32, version_out int32) int32 {
	if i.responses.Get(int(handle)) == nil {
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_version_get: handle=%d version=%d", handle, Http11)

	i.memory.PutUint32(uint32(Http11), int64(version_out))
	return XqdStatusOK
}

func (i *Instance) xqd_resp_header_names_get(handle int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
	i.abilog.Printf("resp_header_names_get: handle=%d cursor=%d", handle, cursor)

	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	names := []string{}
	for n := range w.Header {
		names = append(names, n)
	}

	// these names are explicitly unsorted, so let's sort them ourselves
	sort.Strings(names[:])

	return xqd_multivalue(i.memory, names, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

func (i *Instance) xqd_resp_header_remove(handle int32, name_addr int32, name_size int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	name := make([]byte, name_size)
	_, err := i.memory.ReadAt(name, int64(name_addr))
	if err != nil {
		return XqdError
	}

	w.Header.Del(string(name))

	return XqdStatusOK
}

func (i *Instance) xqd_resp_header_insert(handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
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

	i.abilog.Printf("resp_header_insert: handle=%d header=%q value=%q", handle, header, string(value))

	if w.Header == nil {
		w.Header = http.Header{}
	}

	w.Header.Set(header, string(value))

	return XqdStatusOK
}

func (i *Instance) xqd_resp_header_append(handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
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

	i.abilog.Printf("resp_header_append: handle=%d header=%q value=%q", handle, header, string(value))

	if w.Header == nil {
		w.Header = http.Header{}
	}

	w.Header.Add(header, string(value))

	return XqdStatusOK
}

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
	values, ok := w.Header[header]
	if !ok {
		values = []string{}
	}

	i.abilog.Printf("resp_header_values_get: handle=%d header=%q cursor=%d\n", handle, header, cursor)

	// Sort the values otherwise cursors don't work
	sort.Strings(values[:])

	return xqd_multivalue(i.memory, values, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

func (i *Instance) xqd_resp_header_values_set(handle int32, name_addr int32, name_size int32, values_addr int32, values_size int32) int32 {
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

	// read values_size bytes from values_addr for a list of \0 terminated values for the header
	// but, read 1 less than that to avoid the trailing nul
	buf = make([]byte, values_size-1)
	_, err = i.memory.ReadAt(buf, int64(values_addr))
	if err != nil {
		return XqdError
	}

	values := bytes.Split(buf, []byte("\x00"))

	i.abilog.Printf("resp_header_values_set: handle=%d header=%q values=%q\n", handle, header, values)

	if w.Header == nil {
		w.Header = http.Header{}
	}

	for _, v := range values {
		w.Header.Add(header, string(v))
	}

	return XqdStatusOK
}

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

// xqd_resp_framing_headers_mode_set controls how framing headers (Content-Length, Transfer-Encoding) are set
// Only supports automatic mode
func (i *Instance) xqd_resp_framing_headers_mode_set(handle int32, mode int32) int32 {
	// Validate response handle
	w := i.responses.Get(int(handle))
	if w == nil {
		i.abilog.Printf("resp_framing_headers_mode_set: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_framing_headers_mode_set: handle=%d mode=%d", handle, mode)

	// Mode 0 = Automatic (supported)
	// Mode 1 = ManuallyFromHeaders (not supported)
	if mode != 0 {
		i.abilog.Printf("resp_framing_headers_mode_set: manual mode not supported")
		return XqdErrUnsupported
	}

	return XqdStatusOK
}

// xqd_resp_http_keepalive_mode_set controls connection reuse mode
// Only supports automatic mode
func (i *Instance) xqd_resp_http_keepalive_mode_set(handle int32, mode int32) int32 {
	// Validate response handle
	w := i.responses.Get(int(handle))
	if w == nil {
		i.abilog.Printf("resp_http_keepalive_mode_set: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_http_keepalive_mode_set: handle=%d mode=%d", handle, mode)

	// Mode 0 = Automatic (supported)
	// Mode 1 = NoKeepalive (not supported)
	if mode != 0 {
		i.abilog.Printf("resp_http_keepalive_mode_set: no-keepalive mode not supported")
		return XqdErrUnsupported
	}

	return XqdStatusOK
}

// xqd_resp_get_addr_dest_ip returns the destination IP address for the backend request
func (i *Instance) xqd_resp_get_addr_dest_ip(handle int32, addr_octets_out int32, nwritten_out int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		i.abilog.Printf("resp_get_addr_dest_ip: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_get_addr_dest_ip: handle=%d", handle)

	// Get the remote address from response metadata
	if w.RemoteAddr == "" {
		i.abilog.Printf("resp_get_addr_dest_ip: no remote address available")
		return XqdErrNone
	}

	// Parse the remote address
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

	// Convert to 16-byte representation (IPv4 will be in IPv4-mapped IPv6 format)
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

// xqd_resp_get_addr_dest_port returns the destination port for the backend request
func (i *Instance) xqd_resp_get_addr_dest_port(handle int32, port_out int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		i.abilog.Printf("resp_get_addr_dest_port: invalid handle %d", handle)
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_get_addr_dest_port: handle=%d", handle)

	// Get the remote address from response metadata
	if w.RemoteAddr == "" {
		i.abilog.Printf("resp_get_addr_dest_port: no remote address available")
		return XqdErrNone
	}

	// Parse the remote address
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

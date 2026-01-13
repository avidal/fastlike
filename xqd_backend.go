package fastlike

import (
	"strconv"
)

// xqd_backend_exists checks if a backend with the given name exists
// Returns 1 if exists, 0 if not
func (i *Instance) xqd_backend_exists(backendNamePtr int32, backendNameLen int32, existsOut int32) int32 {
	// Read backend name from guest memory
	backendNameBuf := make([]byte, backendNameLen)
	_, err := i.memory.ReadAt(backendNameBuf, int64(backendNamePtr))
	if err != nil {
		return XqdError
	}

	backendName := string(backendNameBuf)
	i.abilog.Printf("backend_exists: name=%q", backendName)

	// Check if backend exists
	if i.backendExists(backendName) {
		i.memory.PutUint32(1, int64(existsOut))
	} else {
		i.memory.PutUint32(0, int64(existsOut))
	}

	return XqdStatusOK
}

// xqd_backend_is_healthy checks if a backend is healthy
// For now, we always return "unknown" health status
func (i *Instance) xqd_backend_is_healthy(backendNamePtr int32, backendNameLen int32, healthOut int32) int32 {
	backendName, err := i.readBackendName(backendNamePtr, backendNameLen)
	if err != nil {
		return XqdError
	}

	i.abilog.Printf("backend_is_healthy: name=%q", backendName)

	// Check if backend exists
	if !i.backendExists(backendName) {
		return XqdErrInvalidArgument
	}

	// Return BackendHealthUnknown (we don't track health)
	i.memory.PutUint32(BackendHealthUnknown, int64(healthOut))
	return XqdStatusOK
}

// xqd_backend_is_dynamic checks if a backend is dynamically registered
// Returns 1 if dynamic, 0 if static
func (i *Instance) xqd_backend_is_dynamic(backendNamePtr int32, backendNameLen int32, isDynamicOut int32) int32 {
	backendName, err := i.readBackendName(backendNamePtr, backendNameLen)
	if err != nil {
		return XqdError
	}

	i.abilog.Printf("backend_is_dynamic: name=%q", backendName)

	backend := i.getBackend(backendName)
	if backend == nil {
		return XqdErrInvalidArgument
	}

	// Return 1 if dynamic, 0 if static
	var isDynamic uint32
	if backend.IsDynamic {
		isDynamic = 1
	}
	i.memory.PutUint32(isDynamic, int64(isDynamicOut))

	return XqdStatusOK
}

// xqd_backend_get_host gets the host of a backend
func (i *Instance) xqd_backend_get_host(backend_addr int32, backend_size int32, value_out int32, value_max_len int32, nwritten_out int32) int32 {
	// Read backend name from guest memory
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_get_host: name=%q", backendName)

	// Get backend
	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	// Get the host from the URL
	host := b.URL.Host
	if host == "" {
		// If no explicit port, just use hostname
		host = b.URL.Hostname()
	}

	i.abilog.Printf("backend_get_host: name=%q host=%q", backendName, host)

	// Check buffer size
	hostLen := uint32(len(host))
	if hostLen > uint32(value_max_len) {
		i.memory.PutUint32(hostLen, int64(nwritten_out))
		return XqdErrBufferLength
	}

	// Write host to guest memory
	nwritten, err := i.memory.WriteAt([]byte(host), int64(value_out))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	return XqdStatusOK
}

// xqd_backend_get_override_host gets the override host of a backend
func (i *Instance) xqd_backend_get_override_host(backend_addr int32, backend_size int32, value_out int32, value_max_len int32, nwritten_out int32) int32 {
	// Read backend name from guest memory
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_get_override_host: name=%q", backendName)

	// Get backend
	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	// Check if override host is set
	if b.OverrideHost == "" {
		return XqdErrNone // Value not present
	}

	overrideHost := b.OverrideHost
	i.abilog.Printf("backend_get_override_host: name=%q override_host=%q", backendName, overrideHost)

	// Check buffer size
	hostLen := uint32(len(overrideHost))
	if hostLen > uint32(value_max_len) {
		i.memory.PutUint32(hostLen, int64(nwritten_out))
		return XqdErrBufferLength
	}

	// Write override host to guest memory
	nwritten, err := i.memory.WriteAt([]byte(overrideHost), int64(value_out))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	return XqdStatusOK
}

// xqd_backend_get_port gets the port of a backend
func (i *Instance) xqd_backend_get_port(backendNamePtr int32, backendNameLen int32, portOut int32) int32 {
	backendName, err := i.readBackendName(backendNamePtr, backendNameLen)
	if err != nil {
		return XqdError
	}

	i.abilog.Printf("backend_get_port: name=%q", backendName)

	backend := i.getBackend(backendName)
	if backend == nil {
		return XqdErrInvalidArgument
	}

	// Determine port from URL
	var port uint16
	if portStr := backend.URL.Port(); portStr != "" {
		// Parse explicit port from URL
		if p, err := strconv.Atoi(portStr); err == nil {
			port = uint16(p)
		}
	} else if backend.URL.Scheme == "https" {
		port = 443
	} else {
		port = 80
	}

	i.abilog.Printf("backend_get_port: name=%q port=%d", backendName, port)

	i.memory.PutUint16(port, int64(portOut))
	return XqdStatusOK
}

// xqd_backend_get_connect_timeout_ms gets the connection timeout in milliseconds
func (i *Instance) xqd_backend_get_connect_timeout_ms(backend_addr int32, backend_size int32, timeout_out int32) int32 {
	// Read backend name from guest memory
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_get_connect_timeout_ms: name=%q", backendName)

	// Get backend
	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	// Return the configured timeout (or 0 if not set)
	i.memory.PutUint32(b.ConnectTimeoutMs, int64(timeout_out))
	return XqdStatusOK
}

// xqd_backend_get_first_byte_timeout_ms gets the first byte timeout in milliseconds
func (i *Instance) xqd_backend_get_first_byte_timeout_ms(backend_addr int32, backend_size int32, timeout_out int32) int32 {
	// Read backend name from guest memory
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_get_first_byte_timeout_ms: name=%q", backendName)

	// Get backend
	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	// Return the configured timeout (or 0 if not set)
	i.memory.PutUint32(b.FirstByteTimeoutMs, int64(timeout_out))
	return XqdStatusOK
}

// xqd_backend_get_between_bytes_timeout_ms gets the between bytes timeout in milliseconds
func (i *Instance) xqd_backend_get_between_bytes_timeout_ms(backend_addr int32, backend_size int32, timeout_out int32) int32 {
	// Read backend name from guest memory
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_get_between_bytes_timeout_ms: name=%q", backendName)

	// Get backend
	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	// Return the configured timeout (or 0 if not set)
	i.memory.PutUint32(b.BetweenBytesTimeoutMs, int64(timeout_out))
	return XqdStatusOK
}

// xqd_backend_is_ssl checks if backend uses SSL
func (i *Instance) xqd_backend_is_ssl(backend_addr int32, backend_size int32, is_ssl_out int32) int32 {
	// Read backend name from guest memory
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_is_ssl: name=%q", backendName)

	// Get backend
	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	// Check if SSL is enabled (either explicitly set or inferred from https:// scheme)
	isSSL := b.UseSSL || b.URL.Scheme == "https"

	if isSSL {
		i.memory.PutUint32(1, int64(is_ssl_out))
	} else {
		i.memory.PutUint32(0, int64(is_ssl_out))
	}

	return XqdStatusOK
}

// xqd_backend_get_ssl_min_version gets the minimum SSL/TLS version
func (i *Instance) xqd_backend_get_ssl_min_version(backend_addr int32, backend_size int32, version_out int32) int32 {
	// Read backend name from guest memory
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_get_ssl_min_version: name=%q", backendName)

	// Get backend
	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	// Return the configured min version (or 0 if not set)
	// For local testing, SSL version information is not available
	if b.SSLMinVersion == 0 {
		return XqdErrUnsupported
	}

	i.memory.PutUint32(b.SSLMinVersion, int64(version_out))
	return XqdStatusOK
}

// xqd_backend_get_ssl_max_version gets the maximum SSL/TLS version
func (i *Instance) xqd_backend_get_ssl_max_version(backend_addr int32, backend_size int32, version_out int32) int32 {
	// Read backend name from guest memory
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_get_ssl_max_version: name=%q", backendName)

	// Get backend
	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	// Return the configured max version (or 0 if not set)
	// For local testing, SSL version information is not available
	if b.SSLMaxVersion == 0 {
		return XqdErrUnsupported
	}

	i.memory.PutUint32(b.SSLMaxVersion, int64(version_out))
	return XqdStatusOK
}

// xqd_backend_get_http_keepalive_time gets the HTTP keepalive idle timeout
func (i *Instance) xqd_backend_get_http_keepalive_time(backend_addr int32, backend_size int32, timeout_out int32) int32 {
	// Read backend name from guest memory
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_get_http_keepalive_time: name=%q", backendName)

	// Get backend
	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	// Return the configured HTTP keepalive time (or 0 if not set/supported)
	i.memory.PutUint32(b.HTTPKeepaliveTimeMs, int64(timeout_out))
	return XqdStatusOK
}

// xqd_backend_get_tcp_keepalive_enable checks if TCP keepalive is enabled
func (i *Instance) xqd_backend_get_tcp_keepalive_enable(backend_addr int32, backend_size int32, is_enabled_out int32) int32 {
	// Read backend name from guest memory
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_get_tcp_keepalive_enable: name=%q", backendName)

	// Get backend
	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	// Return 1 if enabled, 0 if disabled
	if b.TCPKeepaliveEnable {
		i.memory.PutUint32(1, int64(is_enabled_out))
	} else {
		i.memory.PutUint32(0, int64(is_enabled_out))
	}

	return XqdStatusOK
}

// xqd_backend_get_tcp_keepalive_interval gets the TCP keepalive interval
func (i *Instance) xqd_backend_get_tcp_keepalive_interval(backend_addr int32, backend_size int32, timeout_out int32) int32 {
	// Read backend name from guest memory
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_get_tcp_keepalive_interval: name=%q", backendName)

	// Get backend
	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	// Return the configured interval in seconds (convert from ms)
	intervalSecs := b.TCPKeepaliveIntervalMs / 1000
	i.memory.PutUint32(intervalSecs, int64(timeout_out))
	return XqdStatusOK
}

// xqd_backend_get_tcp_keepalive_probes gets the number of TCP keepalive probes
func (i *Instance) xqd_backend_get_tcp_keepalive_probes(backend_addr int32, backend_size int32, probes_out int32) int32 {
	// Read backend name from guest memory
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_get_tcp_keepalive_probes: name=%q", backendName)

	// Get backend
	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	// Return the configured number of probes
	i.memory.PutUint32(b.TCPKeepaliveProbes, int64(probes_out))
	return XqdStatusOK
}

// xqd_backend_get_tcp_keepalive_time gets the TCP keepalive idle time
func (i *Instance) xqd_backend_get_tcp_keepalive_time(backend_addr int32, backend_size int32, timeout_out int32) int32 {
	// Read backend name from guest memory
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_get_tcp_keepalive_time: name=%q", backendName)

	// Get backend
	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	// Return the configured time in seconds (convert from ms)
	timeSecs := b.TCPKeepaliveTimeMs / 1000
	i.memory.PutUint32(timeSecs, int64(timeout_out))
	return XqdStatusOK
}

// readBackendName is a helper function to read a backend name from guest memory.
// Returns the backend name as a string, or an error if the memory read fails.
func (i *Instance) readBackendName(namePtr int32, nameLen int32) (string, error) {
	buf := make([]byte, nameLen)
	_, err := i.memory.ReadAt(buf, int64(namePtr))
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// xqd_backend_is_ipv6_preferred checks if the backend prefers IPv6 over IPv4
func (i *Instance) xqd_backend_is_ipv6_preferred(backend_addr int32, backend_size int32, is_preferred_out int32) int32 {
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_is_ipv6_preferred: name=%q", backendName)

	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	// Return 1 if IPv6 preferred, 0 otherwise
	var preferred uint32
	if b.PreferIPv6 {
		preferred = 1
	}
	i.memory.PutUint32(preferred, int64(is_preferred_out))
	return XqdStatusOK
}

// xqd_backend_get_max_connections gets the max connections in pool for a backend
func (i *Instance) xqd_backend_get_max_connections(backend_addr int32, backend_size int32, max_out int32) int32 {
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_get_max_connections: name=%q", backendName)

	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	i.memory.PutUint32(b.MaxConnections, int64(max_out))
	return XqdStatusOK
}

// xqd_backend_get_max_use gets how many times a pooled connection can be reused
func (i *Instance) xqd_backend_get_max_use(backend_addr int32, backend_size int32, max_out int32) int32 {
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_get_max_use: name=%q", backendName)

	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	i.memory.PutUint32(b.MaxUse, int64(max_out))
	return XqdStatusOK
}

// xqd_backend_get_max_lifetime_ms gets the max lifetime for keepalive connections
func (i *Instance) xqd_backend_get_max_lifetime_ms(backend_addr int32, backend_size int32, max_out int32) int32 {
	buf := make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	backendName := string(buf)
	i.abilog.Printf("backend_get_max_lifetime_ms: name=%q", backendName)

	b := i.getBackend(backendName)
	if b == nil {
		return XqdErrInvalidArgument
	}

	i.memory.PutUint32(b.MaxLifetimeMs, int64(max_out))
	return XqdStatusOK
}

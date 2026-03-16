package fastlike

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Shield represents a shield POP configuration
type Shield struct {
	RunningOn   bool   // true if this POP is the shield
	Unencrypted string // HTTP URL for the shield
	Encrypted   string // HTTPS URL for the shield
}

// Shield backend options mask bits (from shielding.witx)
const (
	ShieldBackendOptionsReserved        uint32 = 1 << 0
	ShieldBackendOptionsUseCacheKey     uint32 = 1 << 1
	ShieldBackendOptionsFirstByteTimeout uint32 = 1 << 2
)

// Shield backend config struct layout (wasm32):
//   Offset 0: cache_key_ptr    (4 bytes, pointer)
//   Offset 4: cache_key_len    (4 bytes, u32)
//   Offset 8: first_byte_timeout_ms (4 bytes, u32)

// xqd_shield_info returns information about a shield POP.
// The binary response format is: [1-byte running_on flag][unencrypted_url]\0[encrypted_url]\0
func (i *Instance) xqd_shield_info(
	name_addr int32,
	name_size int32,
	info_out int32,
	info_max_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Printf("shield_info: name_addr=%d name_size=%d", name_addr, name_size)

	// Read shield name from guest memory
	nameBuf := make([]byte, name_size)
	_, err := i.memory.ReadAt(nameBuf, int64(name_addr))
	if err != nil {
		return XqdError
	}
	name := string(nameBuf)

	shield, ok := i.shields[name]
	if !ok {
		i.abilog.Printf("shield_info: shield %q not found", name)
		return XqdErrInvalidArgument
	}

	// Build binary response: [running_on: 1 byte][unencrypted_url\0][encrypted_url\0]
	var runningOn byte
	if shield.RunningOn {
		runningOn = 1
	}

	result := []byte{runningOn}
	result = append(result, []byte(shield.Unencrypted)...)
	result = append(result, 0)
	result = append(result, []byte(shield.Encrypted)...)
	result = append(result, 0)

	if int32(len(result)) > info_max_len {
		i.memory.PutUint32(uint32(len(result)), int64(nwritten_out))
		return XqdErrBufferLength
	}

	_, err = i.memory.WriteAt(result, int64(info_out))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(len(result)), int64(nwritten_out))
	i.abilog.Printf("shield_info: shield=%q running_on=%v wrote %d bytes", name, shield.RunningOn, len(result))
	return XqdStatusOK
}

// xqd_backend_for_shield creates or returns a backend for a shield POP.
// Reads optional configuration (cache key, first byte timeout) from the config struct
// and creates a transport-backed proxy handler for the shield URL.
func (i *Instance) xqd_backend_for_shield(
	name_addr int32,
	name_size int32,
	config_mask int32,
	config_ptr int32,
	backend_name_out int32,
	backend_name_max_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Printf("backend_for_shield: name_addr=%d name_size=%d config_mask=%d", name_addr, name_size, config_mask)

	// Read shield name from guest memory
	nameBuf := make([]byte, name_size)
	_, err := i.memory.ReadAt(nameBuf, int64(name_addr))
	if err != nil {
		return XqdError
	}
	name := string(nameBuf)

	// Reject reserved flag (matches Viceroy behavior)
	if uint32(config_mask)&ShieldBackendOptionsReserved != 0 {
		i.abilog.Printf("backend_for_shield: RESERVED flag set, rejecting")
		return XqdErrInvalidArgument
	}

	// Validate cache_key if USE_CACHE_KEY is set
	if uint32(config_mask)&ShieldBackendOptionsUseCacheKey != 0 {
		cacheKeyPtr := int32(i.memory.Uint32(int64(config_ptr + 0)))
		cacheKeyLen := i.memory.Uint32(int64(config_ptr + 4))
		if cacheKeyLen > 0 {
			keyBuf := make([]byte, cacheKeyLen)
			_, err := i.memory.ReadAt(keyBuf, int64(cacheKeyPtr))
			if err != nil {
				return XqdErrInvalidArgument
			}
			i.abilog.Printf("backend_for_shield: cache_key=%q (ignored in local testing)", string(keyBuf))
		}
	}

	// Resolve the shield URI: first check configured shields, then try as raw URI
	shieldURI := name
	if shield, ok := i.shields[name]; ok {
		// Use the encrypted URL from the configured shield, falling back to unencrypted
		if shield.Encrypted != "" {
			shieldURI = shield.Encrypted
		} else if shield.Unencrypted != "" {
			shieldURI = shield.Unencrypted
		}
	}

	u, err := url.Parse(shieldURI)
	if err != nil || u.Host == "" {
		i.abilog.Printf("backend_for_shield: invalid shield URI %q: %v", shieldURI, err)
		return XqdErrInvalidArgument
	}

	// Generate backend name matching Viceroy's format: "******{uri}*****"
	backendName := fmt.Sprintf("******%s*****", shieldURI)

	// Build the backend with optional first byte timeout
	backend := &Backend{
		Name:      backendName,
		URL:       u,
		IsDynamic: true,
	}

	if uint32(config_mask)&ShieldBackendOptionsFirstByteTimeout != 0 {
		backend.FirstByteTimeoutMs = i.memory.Uint32(int64(config_ptr + 8))
		i.abilog.Printf("backend_for_shield: first_byte_timeout_ms=%d", backend.FirstByteTimeoutMs)
	}

	// Create a transport-backed proxy handler (same pattern as dynamic backend registration)
	transport := backend.CreateTransport()
	backend.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Scheme = backend.URL.Scheme
		r.URL.Host = backend.URL.Host

		resp, err := transport.RoundTrip(r)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = fmt.Fprintf(w, "Shield backend request failed: %v", err)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		for k, v := range resp.Header {
			for _, vv := range v {
				w.Header().Add(k, vv)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})

	i.addBackend(backendName, backend)

	// Write the backend name to the output buffer
	nameBytes := []byte(backendName)
	if int32(len(nameBytes)) > backend_name_max_len {
		i.memory.PutUint32(uint32(len(nameBytes)), int64(nwritten_out))
		return XqdErrBufferLength
	}

	_, err = i.memory.WriteAt(nameBytes, int64(backend_name_out))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(len(nameBytes)), int64(nwritten_out))
	i.abilog.Printf("backend_for_shield: created backend %q for shield URI %q", backendName, shieldURI)
	return XqdStatusOK
}

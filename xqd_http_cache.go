package fastlike

import (
	"crypto/sha256"
)

// xqd_http_cache_is_request_cacheable checks if a request is cacheable per RFC 9111
// This function checks whether the request method is GET or HEAD, and considers
// requests with other methods uncacheable.
//
// Returns 1 for cacheable, 0 for not cacheable
func (i *Instance) xqd_http_cache_is_request_cacheable(
	req_handle int32,
) int32 {
	i.abilog.Printf("http_cache_is_request_cacheable: handle=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		return XqdErrInvalidHandle
	}

	// Per RFC 9111 conservative semantics: only GET and HEAD are cacheable
	method := req.Method
	if method == "GET" || method == "HEAD" {
		i.abilog.Printf("http_cache_is_request_cacheable: method=%s -> cacheable", method)
		return 1 // cacheable
	}

	i.abilog.Printf("http_cache_is_request_cacheable: method=%s -> not cacheable", method)
	return 0 // not cacheable
}

// xqd_http_cache_get_suggested_cache_key generates a cache key based on the request
//
// The cache key is a 32-byte SHA256 hash derived from the request URL.
// If the provided buffer is too small, returns XqdErrBufferLength with the required size.
func (i *Instance) xqd_http_cache_get_suggested_cache_key(
	req_handle int32,
	key_out_ptr int32,
	key_out_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Printf("http_cache_get_suggested_cache_key: handle=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		return XqdErrInvalidHandle
	}

	// Generate cache key from request URL
	// Use SHA256 hash of the full URL (including scheme, host, path, query)
	url := req.URL.String()
	hash := sha256.Sum256([]byte(url))
	cacheKey := hash[:]

	// Cache keys are always 32 bytes (SHA256)
	const cacheKeySize = 32

	// Check if buffer is large enough
	if key_out_len < cacheKeySize {
		// Write required size
		i.memory.PutUint32(uint32(cacheKeySize), int64(nwritten_out))
		i.abilog.Printf("http_cache_get_suggested_cache_key: buffer too small, need %d bytes", cacheKeySize)
		return XqdErrBufferLength
	}

	// Write cache key to guest memory
	_, err := i.memory.WriteAt(cacheKey, int64(key_out_ptr))
	if err != nil {
		return XqdError
	}

	// Write actual size written
	i.memory.PutUint32(uint32(cacheKeySize), int64(nwritten_out))
	i.abilog.Printf("http_cache_get_suggested_cache_key: wrote %d bytes for url=%s", cacheKeySize, url)

	return XqdStatusOK
}

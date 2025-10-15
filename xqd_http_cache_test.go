package fastlike

import (
	"io"
	"log"
	"net/url"
	"testing"
)

func TestHttpCacheIsRequestCacheable(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		expected uint32
	}{
		{"GET is cacheable", "GET", 1},
		{"HEAD is cacheable", "HEAD", 1},
		{"POST is not cacheable", "POST", 0},
		{"PUT is not cacheable", "PUT", 0},
		{"DELETE is not cacheable", "DELETE", 0},
		{"PATCH is not cacheable", "PATCH", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := &Instance{
				requests: &RequestHandles{},
				memory:   &Memory{ByteMemory(make([]byte, 4096))},
				abilog:   log.New(io.Discard, "", 0),
			}

			// Create a request with the test method
			rhid, rh := inst.requests.New()
			rh.Method = tt.method
			rh.URL, _ = url.Parse("https://example.com/path")

			// Allocate space for result
			resultPtr := int32(0)

			// Call is_request_cacheable
			status := inst.xqd_http_cache_is_request_cacheable(int32(rhid), resultPtr)

			if status != XqdStatusOK {
				t.Fatalf("xqd_http_cache_is_request_cacheable(%s) status = %d, want %d", tt.method, status, XqdStatusOK)
			}

			// Read the result from memory
			result := inst.memory.Uint32(int64(resultPtr))

			if result != tt.expected {
				t.Errorf("xqd_http_cache_is_request_cacheable(%s) result = %d, want %d", tt.method, result, tt.expected)
			}
		})
	}
}

func TestHttpCacheIsRequestCacheableInvalidHandle(t *testing.T) {
	inst := &Instance{
		requests: &RequestHandles{},
		memory:   &Memory{ByteMemory(make([]byte, 4096))},
		abilog:   log.New(io.Discard, "", 0),
	}

	// Call with invalid handle
	resultPtr := int32(0)
	status := inst.xqd_http_cache_is_request_cacheable(999, resultPtr)

	if status != XqdErrInvalidHandle {
		t.Errorf("xqd_http_cache_is_request_cacheable(invalid) = %d, want %d", status, XqdErrInvalidHandle)
	}
}

func TestHttpCacheGetSuggestedCacheKey(t *testing.T) {
	inst := &Instance{
		requests: &RequestHandles{},
		memory:   &Memory{ByteMemory(make([]byte, 4096))},
		abilog:   log.New(io.Discard, "", 0),
	}

	// Create a request
	rhid, rh := inst.requests.New()
	rh.Method = "GET"
	rh.URL, _ = url.Parse("https://example.com/path?query=value")

	// Allocate space for the cache key and nwritten
	keyPtr := int32(0)
	keyLen := int32(32) // SHA256 is 32 bytes
	nwrittenPtr := int32(100)

	// Call get_suggested_cache_key
	result := inst.xqd_http_cache_get_suggested_cache_key(int32(rhid), keyPtr, keyLen, nwrittenPtr)

	if result != XqdStatusOK {
		t.Fatalf("xqd_http_cache_get_suggested_cache_key() = %d, want %d", result, XqdStatusOK)
	}

	// Verify nwritten is 32
	nwritten := inst.memory.Uint32(int64(nwrittenPtr))
	if nwritten != 32 {
		t.Errorf("nwritten = %d, want 32", nwritten)
	}

	// Verify the key is 32 bytes and not all zeros
	key := make([]byte, 32)
	_, err := inst.memory.ReadAt(key, int64(keyPtr))
	if err != nil {
		t.Fatalf("failed to read cache key: %v", err)
	}

	allZeros := true
	for _, b := range key {
		if b != 0 {
			allZeros = false
			break
		}
	}

	if allZeros {
		t.Error("cache key is all zeros, expected a valid SHA256 hash")
	}
}

func TestHttpCacheGetSuggestedCacheKeyBufferTooSmall(t *testing.T) {
	inst := &Instance{
		requests: &RequestHandles{},
		memory:   &Memory{ByteMemory(make([]byte, 4096))},
		abilog:   log.New(io.Discard, "", 0),
	}

	// Create a request
	rhid, rh := inst.requests.New()
	rh.Method = "GET"
	rh.URL, _ = url.Parse("https://example.com/path")

	// Allocate space but make it too small
	keyPtr := int32(0)
	keyLen := int32(16) // Too small, need 32
	nwrittenPtr := int32(100)

	// Call get_suggested_cache_key
	result := inst.xqd_http_cache_get_suggested_cache_key(int32(rhid), keyPtr, keyLen, nwrittenPtr)

	if result != XqdErrBufferLength {
		t.Errorf("xqd_http_cache_get_suggested_cache_key(small buffer) = %d, want %d", result, XqdErrBufferLength)
	}

	// Verify nwritten contains the required size (32)
	nwritten := inst.memory.Uint32(int64(nwrittenPtr))
	if nwritten != 32 {
		t.Errorf("nwritten = %d, want 32", nwritten)
	}
}

func TestHttpCacheGetSuggestedCacheKeyInvalidHandle(t *testing.T) {
	inst := &Instance{
		requests: &RequestHandles{},
		memory:   &Memory{ByteMemory(make([]byte, 4096))},
		abilog:   log.New(io.Discard, "", 0),
	}

	// Call with invalid handle
	result := inst.xqd_http_cache_get_suggested_cache_key(999, 0, 32, 100)

	if result != XqdErrInvalidHandle {
		t.Errorf("xqd_http_cache_get_suggested_cache_key(invalid) = %d, want %d", result, XqdErrInvalidHandle)
	}
}

func TestHttpCacheGetSuggestedCacheKeyDifferentUrls(t *testing.T) {
	inst := &Instance{
		requests: &RequestHandles{},
		memory:   &Memory{ByteMemory(make([]byte, 4096))},
		abilog:   log.New(io.Discard, "", 0),
	}

	// Create two requests with different URLs
	rhid1, rh1 := inst.requests.New()
	rh1.Method = "GET"
	rh1.URL, _ = url.Parse("https://example.com/path1")

	rhid2, rh2 := inst.requests.New()
	rh2.Method = "GET"
	rh2.URL, _ = url.Parse("https://example.com/path2")

	// Get cache keys for both
	key1Ptr := int32(0)
	key2Ptr := int32(200)
	nwrittenPtr := int32(400)

	result1 := inst.xqd_http_cache_get_suggested_cache_key(int32(rhid1), key1Ptr, 32, nwrittenPtr)
	result2 := inst.xqd_http_cache_get_suggested_cache_key(int32(rhid2), key2Ptr, 32, nwrittenPtr)

	if result1 != XqdStatusOK || result2 != XqdStatusOK {
		t.Fatal("Failed to get cache keys")
	}

	// Read both keys
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	_, err := inst.memory.ReadAt(key1, int64(key1Ptr))
	if err != nil {
		t.Fatalf("failed to read cache key1: %v", err)
	}
	_, err = inst.memory.ReadAt(key2, int64(key2Ptr))
	if err != nil {
		t.Fatalf("failed to read cache key2: %v", err)
	}

	// Verify they're different
	same := true
	for i := 0; i < 32; i++ {
		if key1[i] != key2[i] {
			same = false
			break
		}
	}

	if same {
		t.Error("Expected different cache keys for different URLs, but got the same")
	}
}

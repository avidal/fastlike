package fastlike

import (
	"io"
	"log"
	"net/http"
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

func newHttpCacheHandleTestInstance() *Instance {
	return &Instance{
		requests:     &RequestHandles{},
		responses:    &ResponseHandles{},
		cache:        NewCache(),
		cacheHandles: &CacheHandles{},
		memory:       &Memory{ByteMemory(make([]byte, 4096))},
		abilog:       log.New(io.Discard, "", 0),
	}
}

func TestHttpCacheSuggestedBackendRequestUsesLookupRequest(t *testing.T) {
	inst := newHttpCacheHandleTestInstance()
	reqID, req := inst.requests.New()
	req.URL, _ = url.Parse("https://example.com/path?q=1")
	req.Host = "example.com"
	req.version = Http2
	req.Header.Set("Test-Header", "test-value")
	req.Header.Set("If-None-Match", `"etag"`)
	req.Header.Set("If-Modified-Since", "Tue, 22 Sep 2026 10:00:00 GMT")
	req.Header.Set("Range", "bytes=0-10")

	if status := inst.xqd_http_cache_transaction_lookup(int32(reqID), 0, 0, 0); status != XqdStatusOK {
		t.Fatalf("transaction_lookup status %d", status)
	}
	cacheHandle := int32(inst.memory.Uint32(0))

	// Changes made after the lookup must not reach the backend request.
	req.Header.Set("Test-Header", "changed")
	req.Header.Set("Late-Header", "1")
	inst.requests.Take(reqID)

	if status := inst.xqd_http_cache_get_suggested_backend_request(cacheHandle, 8); status != XqdStatusOK {
		t.Fatalf("get_suggested_backend_request status %d", status)
	}
	backend := inst.requests.Get(int(inst.memory.Uint32(8)))
	if backend == nil {
		t.Fatal("no request handle written")
	}
	if backend.Method != "GET" || backend.URL.String() != "https://example.com/path?q=1" || backend.Host != "example.com" {
		t.Errorf("got %s %s host %q", backend.Method, backend.URL, backend.Host)
	}
	if backend.version != Http2 {
		t.Errorf("version %d, want %d", backend.version, Http2)
	}
	if got := backend.Header.Get("Test-Header"); got != "test-value" {
		t.Errorf("Test-Header = %q, want %q", got, "test-value")
	}
	for _, name := range []string{"Late-Header", "If-None-Match", "If-Modified-Since", "Range"} {
		if got := backend.Header.Get(name); got != "" {
			t.Errorf("%s = %q, want it removed", name, got)
		}
	}

	// Every call hands out a request the guest can change on its own.
	backend.Header.Set("Test-Header", "mutated")
	if status := inst.xqd_http_cache_get_suggested_backend_request(cacheHandle, 12); status != XqdStatusOK {
		t.Fatalf("second get_suggested_backend_request status %d", status)
	}
	if got := inst.requests.Get(int(inst.memory.Uint32(12))).Header.Get("Test-Header"); got != "test-value" {
		t.Errorf("second request Test-Header = %q, want %q", got, "test-value")
	}
}

func TestHttpCacheSuggestedBackendRequestPerHandle(t *testing.T) {
	inst := newHttpCacheHandleTestInstance()
	var cacheHandles [2]int32
	for n, value := range []string{"first", "second"} {
		reqID, req := inst.requests.New()
		req.URL, _ = url.Parse("https://example.com/same-key")
		req.Header.Set("Test-Header", value)
		if status := inst.xqd_http_cache_transaction_lookup(int32(reqID), 0, 0, int32(n*4)); status != XqdStatusOK {
			t.Fatalf("transaction_lookup %d status %d", n, status)
		}
		cacheHandles[n] = int32(inst.memory.Uint32(int64(n * 4)))
	}

	// Both lookups share one transaction, but each handle keeps its own request.
	if inst.cacheHandles.Get(int(cacheHandles[0])).Transaction != inst.cacheHandles.Get(int(cacheHandles[1])).Transaction {
		t.Fatal("the two lookups did not share a transaction")
	}
	for n, want := range []string{"first", "second"} {
		if status := inst.xqd_http_cache_get_suggested_backend_request(cacheHandles[n], 16); status != XqdStatusOK {
			t.Fatalf("get_suggested_backend_request %d status %d", n, status)
		}
		if got := inst.requests.Get(int(inst.memory.Uint32(16))).Header.Get("Test-Header"); got != want {
			t.Errorf("handle %d: Test-Header = %q, want %q", n, got, want)
		}
	}
}

func TestHttpCacheSuggestedBackendRequestAfterPlainLookup(t *testing.T) {
	inst := newHttpCacheHandleTestInstance()
	reqID, req := inst.requests.New()
	req.URL, _ = url.Parse("https://example.com/")
	req.Header.Set("Test-Header", "test-value")

	if status := inst.xqd_http_cache_lookup(int32(reqID), 0, 0, 0); status != XqdStatusOK {
		t.Fatalf("lookup status %d", status)
	}
	if status := inst.xqd_http_cache_get_suggested_backend_request(int32(inst.memory.Uint32(0)), 8); status != XqdStatusOK {
		t.Fatalf("get_suggested_backend_request status %d", status)
	}
	if got := inst.requests.Get(int(inst.memory.Uint32(8))).Header.Get("Test-Header"); got != "test-value" {
		t.Errorf("Test-Header = %q, want %q", got, "test-value")
	}
}

func TestPrepareFullBackendRequest(t *testing.T) {
	conditional := []string{"If-Modified-Since", "If-Unmodified-Since", "If-None-Match", "If-Match", "If-Range"}
	tests := []struct {
		method          string
		wantMethod      string
		keepConditional bool
	}{
		{"GET", "GET", false},
		{"HEAD", "GET", false},
		{"OPTIONS", "OPTIONS", false},
		{"TRACE", "TRACE", false},
		{"POST", "POST", true},
		{"PUT", "PUT", true},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			r := &http.Request{Method: tt.method, Header: http.Header{}}
			for _, name := range conditional {
				r.Header.Set(name, "x")
			}
			r.Header.Set("Range", "bytes=0-1")
			r.Header.Set("Accept", "text/html")

			prepareFullBackendRequest(r)

			if r.Method != tt.wantMethod {
				t.Errorf("method %s, want %s", r.Method, tt.wantMethod)
			}
			for _, name := range conditional {
				if kept := r.Header.Get(name) != ""; kept != tt.keepConditional {
					t.Errorf("%s kept = %v, want %v", name, kept, tt.keepConditional)
				}
			}
			if r.Header.Get("Range") != "" {
				t.Error("Range was not removed")
			}
			if r.Header.Get("Accept") != "text/html" {
				t.Error("Accept was removed")
			}
		})
	}
}

func TestHttpCachePrepareResponseForStorageReturnsNewHandle(t *testing.T) {
	inst := newHttpCacheHandleTestInstance()
	reqID, req := inst.requests.New()
	req.URL, _ = url.Parse("https://example.com/")
	if status := inst.xqd_http_cache_transaction_lookup(int32(reqID), 0, 0, 0); status != XqdStatusOK {
		t.Fatalf("transaction_lookup status %d", status)
	}
	cacheHandle := int32(inst.memory.Uint32(0))

	respID, resp := inst.responses.New()
	resp.Header = http.Header{"X-Test": {"original"}}
	resp.RemoteAddr = "192.0.2.1:443"
	resp.version = Http2

	if status := inst.xqd_http_cache_prepare_response_for_storage(cacheHandle, int32(respID), 8, 12); status != XqdStatusOK {
		t.Fatalf("prepare_response_for_storage status %d", status)
	}
	preparedID := int(inst.memory.Uint32(12))
	if preparedID == respID {
		t.Fatalf("prepared response reuses handle %d", respID)
	}

	// The Rust SDK closes the original handle as soon as it has the new one.
	if status := inst.xqd_resp_close(int32(respID)); status != XqdStatusOK {
		t.Fatalf("closing the original response: status %d", status)
	}
	prepared := inst.responses.Get(preparedID)
	if prepared == nil {
		t.Fatal("prepared response was closed along with the original")
	}
	if prepared.StatusCode != 200 || prepared.Header.Get("X-Test") != "original" {
		t.Errorf("prepared response: status %d, X-Test %q", prepared.StatusCode, prepared.Header.Get("X-Test"))
	}
	if prepared.RemoteAddr != "192.0.2.1:443" || prepared.version != Http2 {
		t.Errorf("prepared response: remote %q, version %d", prepared.RemoteAddr, prepared.version)
	}

	prepared.Header.Set("X-Test", "changed")
	if got := resp.Header.Get("X-Test"); got != "original" {
		t.Errorf("original response header changed to %q", got)
	}
}

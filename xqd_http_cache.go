package fastlike

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// xqd_http_cache_is_request_cacheable checks if a request is cacheable per RFC 9111
// This function checks whether the request method is GET or HEAD, and considers
// requests with other methods uncacheable.
//
// Writes 1 for cacheable, 0 for not cacheable to is_cacheable_out pointer
func (i *Instance) xqd_http_cache_is_request_cacheable(
	req_handle int32,
	is_cacheable_out int32,
) int32 {
	i.abilog.Printf("http_cache_is_request_cacheable: handle=%d", req_handle)

	req := i.requests.Get(int(req_handle))
	if req == nil {
		return XqdErrInvalidHandle
	}

	// Per RFC 9111 conservative semantics: only GET and HEAD are cacheable
	method := req.Method
	var isCacheable uint32
	if method == "GET" || method == "HEAD" {
		i.abilog.Printf("http_cache_is_request_cacheable: method=%s -> cacheable", method)
		isCacheable = 1
	} else {
		i.abilog.Printf("http_cache_is_request_cacheable: method=%s -> not cacheable", method)
		isCacheable = 0
	}

	// Write result to guest memory
	i.memory.WriteUint32(is_cacheable_out, isCacheable)

	return XqdStatusOK
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

// xqd_http_cache_lookup performs a non-transactional cache lookup using a request
func (i *Instance) xqd_http_cache_lookup(
	req_handle int32,
	options_mask uint32,
	options int32,
	cache_handle_out int32,
) int32 {
	i.abilog.Println("http_cache_lookup")

	req := i.requests.Get(int(req_handle))
	if req == nil {
		return XqdErrInvalidHandle
	}

	// Generate cache key from request
	url := req.URL.String()
	hash := sha256.Sum256([]byte(url))
	key := hash[:]

	// Build lookup options
	lookupOpts := &CacheLookupOptions{}

	// Note: HTTP cache lookup options are different from raw cache lookup options
	// For now, we'll use basic lookup without special options

	entry := i.cache.Lookup(key, lookupOpts)

	// Create a transaction to wrap the entry
	tx := &CacheTransaction{
		Key:   key,
		Entry: entry,
		ready: make(chan struct{}),
	}
	close(tx.ready) // Already complete

	handleID := i.cacheHandles.New(tx)
	i.memory.WriteUint32(cache_handle_out, uint32(handleID))

	return XqdStatusOK
}

// xqd_http_cache_transaction_lookup performs a transactional cache lookup using a request
func (i *Instance) xqd_http_cache_transaction_lookup(
	req_handle int32,
	options_mask uint32,
	options int32,
	cache_handle_out int32,
) int32 {
	i.abilog.Println("http_cache_transaction_lookup")

	req := i.requests.Get(int(req_handle))
	if req == nil {
		return XqdErrInvalidHandle
	}

	// Generate cache key from request
	url := req.URL.String()
	hash := sha256.Sum256([]byte(url))
	key := hash[:]

	lookupOpts := &CacheLookupOptions{}
	tx := i.cache.TransactionLookup(key, lookupOpts)

	// Store the original request URL and method in the transaction
	// so that get_suggested_backend_request can create a proper request
	tx.RequestURL = url
	tx.RequestMethod = req.Method

	handleID := i.cacheHandles.New(tx)
	i.memory.WriteUint32(cache_handle_out, uint32(handleID))

	return XqdStatusOK
}

// xqd_http_cache_transaction_insert inserts a response into cache
func (i *Instance) xqd_http_cache_transaction_insert(
	cache_handle int32,
	resp_handle int32,
	options_mask uint32,
	options int32,
	body_handle_out int32,
) int32 {
	i.abilog.Println("http_cache_transaction_insert")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil {
		return XqdErrInvalidHandle
	}

	resp := i.responses.Get(int(resp_handle))
	if resp == nil {
		return XqdErrInvalidHandle
	}

	writeOpts := i.readHttpCacheWriteOptions(options_mask, options)

	obj := i.cache.Insert(handle.Transaction.Key, writeOpts)

	// Create a body handle that writes to the cache object
	bodyID, body := i.bodies.NewBuffer()
	body.writer = &cacheBodyWriter{
		cache:        obj,
		originalBody: body.buf,
	}

	i.memory.WriteUint32(body_handle_out, uint32(bodyID))

	// Complete the transaction
	i.cache.CompleteTransaction(handle.Transaction)

	return XqdStatusOK
}

// xqd_http_cache_transaction_insert_and_stream_back inserts and streams back
func (i *Instance) xqd_http_cache_transaction_insert_and_stream_back(
	cache_handle int32,
	resp_handle int32,
	options_mask uint32,
	options int32,
	body_handle_out int32,
	cache_handle_out int32,
) int32 {
	i.abilog.Println("http_cache_transaction_insert_and_stream_back")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil {
		return XqdErrInvalidHandle
	}

	resp := i.responses.Get(int(resp_handle))
	if resp == nil {
		return XqdErrInvalidHandle
	}

	writeOpts := i.readHttpCacheWriteOptions(options_mask, options)

	obj := i.cache.Insert(handle.Transaction.Key, writeOpts)

	// Create a body handle for writing
	writeBodyID, writeBody := i.bodies.NewBuffer()
	writeBody.writer = &cacheBodyWriter{
		cache:        obj,
		originalBody: writeBody.buf,
	}

	// Create a new transaction/handle for reading back
	readTx := &CacheTransaction{
		Key: handle.Transaction.Key,
		Entry: &CacheEntry{
			Object: obj,
			State: CacheState{
				Found:  true,
				Usable: true,
			},
		},
		ready: make(chan struct{}),
	}
	close(readTx.ready)

	readHandleID := i.cacheHandles.New(readTx)

	i.memory.WriteUint32(body_handle_out, uint32(writeBodyID))
	i.memory.WriteUint32(cache_handle_out, uint32(readHandleID))

	// Complete the original transaction
	i.cache.CompleteTransaction(handle.Transaction)

	return XqdStatusOK
}

// xqd_http_cache_transaction_update updates cache metadata
func (i *Instance) xqd_http_cache_transaction_update(
	cache_handle int32,
	resp_handle int32,
	options_mask uint32,
	options int32,
) int32 {
	i.abilog.Println("http_cache_transaction_update")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil {
		return XqdErrInvalidHandle
	}

	resp := i.responses.Get(int(resp_handle))
	if resp == nil {
		return XqdErrInvalidHandle
	}

	writeOpts := i.readHttpCacheWriteOptions(options_mask, options)

	err := i.cache.TransactionUpdate(handle.Transaction, writeOpts)
	if err != nil {
		return XqdError
	}

	// Complete the transaction
	i.cache.CompleteTransaction(handle.Transaction)

	return XqdStatusOK
}

// xqd_http_cache_transaction_update_and_return_fresh updates and returns fresh entry
func (i *Instance) xqd_http_cache_transaction_update_and_return_fresh(
	cache_handle int32,
	resp_handle int32,
	options_mask uint32,
	options int32,
	cache_handle_out int32,
) int32 {
	i.abilog.Println("http_cache_transaction_update_and_return_fresh")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil {
		return XqdErrInvalidHandle
	}

	resp := i.responses.Get(int(resp_handle))
	if resp == nil {
		return XqdErrInvalidHandle
	}

	writeOpts := i.readHttpCacheWriteOptions(options_mask, options)

	err := i.cache.TransactionUpdate(handle.Transaction, writeOpts)
	if err != nil {
		return XqdError
	}

	// Create a new handle for the fresh entry
	freshTx := &CacheTransaction{
		Key:   handle.Transaction.Key,
		Entry: handle.Transaction.Entry,
		ready: make(chan struct{}),
	}
	close(freshTx.ready)

	freshHandleID := i.cacheHandles.New(freshTx)
	i.memory.WriteUint32(cache_handle_out, uint32(freshHandleID))

	// Complete the original transaction
	i.cache.CompleteTransaction(handle.Transaction)

	return XqdStatusOK
}

// xqd_http_cache_transaction_record_not_cacheable marks entry as not cacheable
func (i *Instance) xqd_http_cache_transaction_record_not_cacheable(
	cache_handle int32,
	options_mask uint32,
	options int32,
) int32 {
	i.abilog.Println("http_cache_transaction_record_not_cacheable")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil {
		return XqdErrInvalidHandle
	}

	// Mark as not cacheable by canceling the transaction
	// In a real implementation, this would record negative caching info
	err := i.cache.TransactionCancel(handle.Transaction)
	if err != nil {
		return XqdError
	}

	return XqdStatusOK
}

// xqd_http_cache_transaction_abandon abandons a cache transaction
func (i *Instance) xqd_http_cache_transaction_abandon(cache_handle int32) int32 {
	i.abilog.Println("http_cache_transaction_abandon")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil {
		return XqdErrInvalidHandle
	}

	err := i.cache.TransactionCancel(handle.Transaction)
	if err != nil {
		return XqdError
	}

	return XqdStatusOK
}

// xqd_http_cache_close closes a cache handle
func (i *Instance) xqd_http_cache_close(cache_handle int32) int32 {
	i.abilog.Println("http_cache_close")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil {
		return XqdErrInvalidHandle
	}

	// Nothing to do - just validates the handle exists
	return XqdStatusOK
}

// xqd_http_cache_get_suggested_backend_request creates a backend request from cache state
func (i *Instance) xqd_http_cache_get_suggested_backend_request(
	cache_handle int32,
	req_handle_out int32,
) int32 {
	i.abilog.Println("http_cache_get_suggested_backend_request")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil {
		return XqdErrInvalidHandle
	}

	// Create a new request with the URL and method from the original request
	// In a real implementation, this would be populated with cache validation headers
	reqID, reqHandle := i.requests.New()

	// Parse the URL from the stored string
	reqURL, err := url.Parse(handle.Transaction.RequestURL)
	if err != nil {
		i.abilog.Printf("http_cache_get_suggested_backend_request: failed to parse URL %q: %v", handle.Transaction.RequestURL, err)
		return XqdError
	}

	reqHandle.Request = &http.Request{
		Method: handle.Transaction.RequestMethod,
		URL:    reqURL,
		Header: http.Header{},
	}

	i.abilog.Printf("http_cache_get_suggested_backend_request: created request url=%s method=%s", reqURL, reqHandle.Method)
	i.memory.WriteUint32(req_handle_out, uint32(reqID))

	return XqdStatusOK
}

// xqd_http_cache_get_suggested_cache_options gets suggested cache options from response
func (i *Instance) xqd_http_cache_get_suggested_cache_options(
	cache_handle int32,
	resp_handle int32,
	requested_mask uint32,
	requested_options int32,
	options_mask_out int32,
	options_out int32,
) int32 {
	i.abilog.Println("http_cache_get_suggested_cache_options")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil {
		return XqdErrInvalidHandle
	}

	resp := i.responses.Get(int(resp_handle))
	if resp == nil {
		return XqdErrInvalidHandle
	}

	// HTTP cache write options structure (based on Viceroy):
	// - max_age_ns: u64 (8 bytes) at offset 0
	// - vary_rule_ptr: *u8 (4 bytes) at offset 8
	// - vary_rule_len: usize (4 bytes) at offset 12
	// - initial_age_ns: u64 (8 bytes) at offset 16
	// - stale_while_revalidate_ns: u64 (8 bytes) at offset 24
	// - surrogate_keys_ptr: *u8 (4 bytes) at offset 32
	// - surrogate_keys_len: usize (4 bytes) at offset 36
	// - length: u64 (8 bytes) at offset 40

	const (
		HttpCacheWriteOptionsMaskMaxAgeNs                = 1 << 0
		HttpCacheWriteOptionsMaskVaryRule                = 1 << 1
		HttpCacheWriteOptionsMaskInitialAgeNs            = 1 << 2
		HttpCacheWriteOptionsMaskStaleWhileRevalidateNs  = 1 << 3
		HttpCacheWriteOptionsMaskSurrogateKeys           = 1 << 4
		HttpCacheWriteOptionsMaskLength                  = 1 << 5
	)

	// Parse Cache-Control header to determine max-age and other directives
	// Default to 1 hour (3600 seconds) if not specified
	maxAgeNs := uint64(3600 * 1000000000) // 1 hour in nanoseconds
	optionsMask := uint32(HttpCacheWriteOptionsMaskMaxAgeNs)

	// Parse Cache-Control header if present
	if cacheControl := resp.Header.Get("Cache-Control"); cacheControl != "" {
		i.abilog.Printf("http_cache_get_suggested_cache_options: parsing Cache-Control: %s", cacheControl)
		// Simple parsing for max-age directive
		// In production, this should use a proper Cache-Control parser
		// For now, we just extract max-age if present
		// Example: "max-age=3600, public"
		for _, directive := range splitCacheControl(cacheControl) {
			if len(directive) > 8 && directive[:8] == "max-age=" {
				if seconds, err := parseInt(directive[8:]); err == nil && seconds >= 0 {
					maxAgeNs = uint64(seconds) * 1000000000
					i.abilog.Printf("http_cache_get_suggested_cache_options: found max-age=%d seconds", seconds)
				}
			}
		}
	}

	// Write max_age_ns
	i.memory.WriteUint64(options_out+0, maxAgeNs)

	// Write options mask
	i.memory.WriteUint32(options_mask_out, optionsMask)

	i.abilog.Printf("http_cache_get_suggested_cache_options: returning mask=%d, max_age_ns=%d", optionsMask, maxAgeNs)

	return XqdStatusOK
}

// splitCacheControl splits a Cache-Control header value into directives
func splitCacheControl(s string) []string {
	var directives []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			directive := strings.TrimSpace(s[start:i])
			if directive != "" {
				directives = append(directives, directive)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		directive := strings.TrimSpace(s[start:])
		if directive != "" {
			directives = append(directives, directive)
		}
	}
	return directives
}

// parseInt parses an integer from a string
func parseInt(s string) (int64, error) {
	s = strings.TrimSpace(s)
	var result int64
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("invalid integer")
		}
		result = result*10 + int64(s[i]-'0')
	}
	return result, nil
}

// xqd_http_cache_prepare_response_for_storage prepares response for caching
func (i *Instance) xqd_http_cache_prepare_response_for_storage(
	cache_handle int32,
	resp_handle int32,
	storage_action_out int32,
	resp_handle_out int32,
) int32 {
	i.abilog.Println("http_cache_prepare_response_for_storage")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil {
		return XqdErrInvalidHandle
	}

	resp := i.responses.Get(int(resp_handle))
	if resp == nil {
		return XqdErrInvalidHandle
	}

	// Storage action: 0=Insert, 1=Update, 2=DoNotStore, 3=RecordUncacheable
	storageAction := uint32(0) // Insert

	// Check if response is cacheable
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		storageAction = 2 // DoNotStore
	}

	i.memory.WriteUint32(storage_action_out, storageAction)

	// Return the same response handle (no modification needed)
	i.memory.WriteUint32(resp_handle_out, uint32(resp_handle))

	return XqdStatusOK
}

// xqd_http_cache_get_found_response gets cached response
func (i *Instance) xqd_http_cache_get_found_response(
	cache_handle int32,
	transform_for_client uint32,
	resp_handle_out int32,
	body_handle_out int32,
) int32 {
	i.abilog.Println("http_cache_get_found_response")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil {
		return XqdErrInvalidHandle
	}

	entry := handle.Transaction.Entry
	if !entry.State.Found || !entry.State.Usable || entry.Object == nil {
		return XqdErrNone
	}

	// Create a response from cached object
	respID, respHandle := i.responses.New()
	respHandle.Response = &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader([]byte{})),
	}

	i.memory.WriteUint32(resp_handle_out, uint32(respID))

	// Create a body handle for reading from cache
	bodyID, body := i.bodies.NewBuffer()
	body.reader = &cacheBodyReader{
		cache:  entry.Object,
		offset: 0,
	}

	i.memory.WriteUint32(body_handle_out, uint32(bodyID))

	return XqdStatusOK
}

// xqd_http_cache_get_state gets cache lookup state
func (i *Instance) xqd_http_cache_get_state(
	cache_handle int32,
	cache_lookup_state_out int32,
) int32 {
	i.abilog.Println("http_cache_get_state")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil {
		return XqdErrInvalidHandle
	}

	state := handle.Transaction.Entry.State
	var flags uint32
	if state.Found {
		flags |= CacheLookupStateFound
	}
	if state.Usable {
		flags |= CacheLookupStateUsable
	}
	if state.Stale {
		flags |= CacheLookupStateStale
	}
	if state.MustInsertOrUpdate {
		flags |= CacheLookupStateMustInsertOrUpdate
	}

	i.memory.WriteUint32(cache_lookup_state_out, flags)

	return XqdStatusOK
}

// xqd_http_cache_get_length gets cached object length
func (i *Instance) xqd_http_cache_get_length(
	cache_handle int32,
	length_out int32,
) int32 {
	i.abilog.Println("http_cache_get_length")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil || handle.Transaction.Entry.Object == nil {
		return XqdErrInvalidHandle
	}

	obj := handle.Transaction.Entry.Object
	if obj.Length != nil {
		i.memory.WriteUint64(length_out, *obj.Length)
		return XqdStatusOK
	}

	return XqdErrNone
}

// xqd_http_cache_get_max_age_ns gets max age in nanoseconds
func (i *Instance) xqd_http_cache_get_max_age_ns(
	cache_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("http_cache_get_max_age_ns")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil || handle.Transaction.Entry.Object == nil {
		return XqdErrInvalidHandle
	}

	obj := handle.Transaction.Entry.Object
	i.memory.WriteUint64(duration_out, obj.MaxAgeNs)

	return XqdStatusOK
}

// xqd_http_cache_get_stale_while_revalidate_ns gets stale-while-revalidate duration
func (i *Instance) xqd_http_cache_get_stale_while_revalidate_ns(
	cache_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("http_cache_get_stale_while_revalidate_ns")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil || handle.Transaction.Entry.Object == nil {
		return XqdErrInvalidHandle
	}

	obj := handle.Transaction.Entry.Object
	// Always return the value (even if 0) - the Rust library expects it to be present
	i.memory.WriteUint64(duration_out, obj.StaleWhileRevalidateNs)

	return XqdStatusOK
}

// xqd_http_cache_get_age_ns gets age of cached object
func (i *Instance) xqd_http_cache_get_age_ns(
	cache_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("http_cache_get_age_ns")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil || handle.Transaction.Entry.Object == nil {
		return XqdErrInvalidHandle
	}

	obj := handle.Transaction.Entry.Object
	age := obj.GetAge()
	i.memory.WriteUint64(duration_out, age)

	return XqdStatusOK
}

// xqd_http_cache_get_hits gets hit count
func (i *Instance) xqd_http_cache_get_hits(
	cache_handle int32,
	hits_out int32,
) int32 {
	i.abilog.Println("http_cache_get_hits")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil || handle.Transaction.Entry.Object == nil {
		return XqdErrInvalidHandle
	}

	obj := handle.Transaction.Entry.Object
	i.memory.WriteUint64(hits_out, obj.HitCount)

	return XqdStatusOK
}

// xqd_http_cache_get_sensitive_data checks if cached data is sensitive
func (i *Instance) xqd_http_cache_get_sensitive_data(
	cache_handle int32,
	sensitive_out int32,
) int32 {
	i.abilog.Println("http_cache_get_sensitive_data")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil || handle.Transaction.Entry.Object == nil {
		return XqdErrInvalidHandle
	}

	obj := handle.Transaction.Entry.Object
	sensitive := uint32(0)
	if obj.SensitiveData {
		sensitive = 1
	}

	i.memory.WriteUint32(sensitive_out, sensitive)

	return XqdStatusOK
}

// xqd_http_cache_get_surrogate_keys gets surrogate keys
func (i *Instance) xqd_http_cache_get_surrogate_keys(
	cache_handle int32,
	keys_out_ptr int32,
	keys_out_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Println("http_cache_get_surrogate_keys")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil || handle.Transaction.Entry.Object == nil {
		return XqdErrInvalidHandle
	}

	obj := handle.Transaction.Entry.Object

	// Join surrogate keys with spaces (empty list is OK - write 0 bytes)
	keysStr := ""
	if obj.SurrogateKeys != nil && len(obj.SurrogateKeys) > 0 {
		for idx, key := range obj.SurrogateKeys {
			if idx > 0 {
				keysStr += " "
			}
			keysStr += key
		}
	}

	keyBytes := []byte(keysStr)

	if len(keyBytes) > int(keys_out_len) {
		i.memory.WriteUint32(nwritten_out, uint32(len(keyBytes)))
		return XqdErrBufferLength
	}

	if len(keyBytes) > 0 {
		_, _ = i.memory.WriteAt(keyBytes, int64(keys_out_ptr))
	}
	i.memory.WriteUint32(nwritten_out, uint32(len(keyBytes)))

	return XqdStatusOK
}

// xqd_http_cache_get_vary_rule gets vary rule
func (i *Instance) xqd_http_cache_get_vary_rule(
	cache_handle int32,
	rule_out_ptr int32,
	rule_out_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Println("http_cache_get_vary_rule")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil || handle.Transaction.Entry.Object == nil {
		return XqdErrInvalidHandle
	}

	obj := handle.Transaction.Entry.Object
	ruleBytes := []byte(obj.VaryRule) // Empty string is OK - write 0 bytes

	if len(ruleBytes) > int(rule_out_len) {
		i.memory.WriteUint32(nwritten_out, uint32(len(ruleBytes)))
		return XqdErrBufferLength
	}

	if len(ruleBytes) > 0 {
		_, _ = i.memory.WriteAt(ruleBytes, int64(rule_out_ptr))
	}
	i.memory.WriteUint32(nwritten_out, uint32(len(ruleBytes)))

	return XqdStatusOK
}

// Helper function to read HTTP cache write options from guest memory
func (i *Instance) readHttpCacheWriteOptions(mask uint32, optionsPtr int32) *CacheWriteOptions {
	opts := &CacheWriteOptions{}

	// HTTP cache write options structure (based on Viceroy):
	// - max_age_ns: u64 (8 bytes) at offset 0
	// - vary_rule_ptr: *u8 (4 bytes) at offset 8
	// - vary_rule_len: usize (4 bytes) at offset 12
	// - initial_age_ns: u64 (8 bytes) at offset 16
	// - stale_while_revalidate_ns: u64 (8 bytes) at offset 24
	// - surrogate_keys_ptr: *u8 (4 bytes) at offset 32
	// - surrogate_keys_len: usize (4 bytes) at offset 36
	// - length: u64 (8 bytes) at offset 40

	// Read max_age_ns (always present)
	opts.MaxAgeNs = i.memory.ReadUint64(optionsPtr + 0)

	// Mask bits for HTTP cache write options
	const (
		HttpCacheWriteOptionsMaskVaryRule                = 1 << 1
		HttpCacheWriteOptionsMaskInitialAgeNs            = 1 << 2
		HttpCacheWriteOptionsMaskStaleWhileRevalidateNs  = 1 << 3
		HttpCacheWriteOptionsMaskSurrogateKeys           = 1 << 4
		HttpCacheWriteOptionsMaskLength                  = 1 << 5
		HttpCacheWriteOptionsMaskSensitiveData           = 1 << 6
	)

	// Read vary_rule
	if mask&HttpCacheWriteOptionsMaskVaryRule != 0 {
		varyPtr := int32(i.memory.Uint32(int64(optionsPtr + 8)))
		varyLen := int32(i.memory.Uint32(int64(optionsPtr + 12)))
		if varyLen > 0 {
			varyBuf := make([]byte, varyLen)
			_, _ = i.memory.ReadAt(varyBuf, int64(varyPtr))
			opts.VaryRule = string(varyBuf)
		}
	}

	// Read initial_age_ns
	if mask&HttpCacheWriteOptionsMaskInitialAgeNs != 0 {
		val := i.memory.ReadUint64(optionsPtr + 16)
		opts.InitialAgeNs = &val
	}

	// Read stale_while_revalidate_ns
	if mask&HttpCacheWriteOptionsMaskStaleWhileRevalidateNs != 0 {
		val := i.memory.ReadUint64(optionsPtr + 24)
		opts.StaleWhileRevalidateNs = &val
	}

	// Read surrogate_keys
	if mask&HttpCacheWriteOptionsMaskSurrogateKeys != 0 {
		keysPtr := int32(i.memory.Uint32(int64(optionsPtr + 32)))
		keysLen := int32(i.memory.Uint32(int64(optionsPtr + 36)))
		if keysLen > 0 {
			keysBuf := make([]byte, keysLen)
			_, _ = i.memory.ReadAt(keysBuf, int64(keysPtr))
			keysStr := string(keysBuf)
			opts.SurrogateKeys = splitSurrogateKeys(keysStr)
		}
	}

	// Read length
	if mask&HttpCacheWriteOptionsMaskLength != 0 {
		val := i.memory.ReadUint64(optionsPtr + 40)
		opts.Length = &val
	}

	// Read sensitive_data flag
	if mask&HttpCacheWriteOptionsMaskSensitiveData != 0 {
		opts.SensitiveData = true
	}

	return opts
}

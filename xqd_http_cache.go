package fastlike

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"
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

	if method == http.MethodGet || method == http.MethodHead {
		isCacheable = 1
		i.abilog.Printf("http_cache_is_request_cacheable: method=%s -> cacheable", method)
	} else {
		isCacheable = 0
		i.abilog.Printf("http_cache_is_request_cacheable: method=%s -> not cacheable", method)
	}

	// Write result to guest memory
	i.memory.WriteUint32(is_cacheable_out, isCacheable)

	return XqdStatusOK
}

// xqd_http_cache_get_suggested_cache_key generates a suggested cache key for an HTTP request.
// The cache key is a 32-byte SHA256 hash of the request URL.
// Returns XqdErrBufferLength if the provided buffer is too small (writes required size to nwritten_out).
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

	// Generate cache key: SHA256 hash of the full URL (scheme, host, path, query)
	url := req.URL.String()
	hash := sha256.Sum256([]byte(url))
	cacheKey := hash[:]

	const cacheKeySize = 32 // SHA256 produces 32 bytes

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

	lookup := newHTTPCacheLookup(req)
	entry := i.cache.Lookup(key, &CacheLookupOptions{RequestHeaders: lookup.requestHeaders})
	handleID := i.newHTTPCacheHandle(settledTransaction(key, entry, nil), lookup)
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

	lookup := newHTTPCacheLookup(req)
	tx := i.cache.TransactionLookup(key, &CacheLookupOptions{RequestHeaders: lookup.requestHeaders}, i)
	handleID := i.newHTTPCacheHandle(tx, lookup)
	i.memory.WriteUint32(cache_handle_out, uint32(handleID))

	return XqdStatusOK
}

// xqd_http_cache_transaction_insert stores a response and returns the body to
// write it with.
func (i *Instance) xqd_http_cache_transaction_insert(
	cache_handle int32,
	resp_handle int32,
	options_mask uint32,
	options int32,
	body_handle_out int32,
) int32 {
	i.abilog.Println("http_cache_transaction_insert")

	handle, writeOpts, status := i.httpCacheWrite(cache_handle, resp_handle, options_mask, options)
	if status != XqdStatusOK {
		return status
	}
	obj := i.cache.Insert(handle.Transaction.Key, writeOpts)
	bodyID := i.newCacheInsertBody(obj, handle.Transaction.Key)
	i.memory.WriteUint32(body_handle_out, uint32(bodyID))
	i.cache.CompleteTransaction(handle.Transaction)

	return XqdStatusOK
}

// xqd_http_cache_transaction_insert_and_stream_back stores a response and also
// returns a cache handle that reads it back as it is written.
func (i *Instance) xqd_http_cache_transaction_insert_and_stream_back(
	cache_handle int32,
	resp_handle int32,
	options_mask uint32,
	options int32,
	body_handle_out int32,
	cache_handle_out int32,
) int32 {
	i.abilog.Println("http_cache_transaction_insert_and_stream_back")

	handle, writeOpts, status := i.httpCacheWrite(cache_handle, resp_handle, options_mask, options)
	if status != XqdStatusOK {
		return status
	}
	obj := i.cache.Insert(handle.Transaction.Key, writeOpts)
	writeBodyID := i.newCacheInsertBody(obj, handle.Transaction.Key)
	readHandleID := i.newHTTPCacheHandle(usableTransaction(handle.Transaction.Key, obj), handle.lookup)

	i.memory.WriteUint32(body_handle_out, uint32(writeBodyID))
	i.memory.WriteUint32(cache_handle_out, uint32(readHandleID))
	i.cache.CompleteTransaction(handle.Transaction)

	return XqdStatusOK
}

// httpCacheWrite takes the response of an insert or update and returns the
// write options that store it.
// As in production, the response is consumed even if the cache handle is
// invalid.
func (i *Instance) httpCacheWrite(cache_handle, resp_handle int32, options_mask uint32, options int32) (*CacheHandle, *CacheWriteOptions, int32) {
	writeOpts := i.readHttpCacheWriteOptions(options_mask, options)
	resp := i.responses.Take(int(resp_handle))
	if resp == nil {
		return nil, nil, XqdErrInvalidHandle
	}
	handle := i.httpCacheHandle(cache_handle)
	if handle == nil {
		return nil, nil, XqdErrInvalidHandle
	}

	writeOpts.Response = newStoredResponse(resp.Response, handle.lookup.time)
	writeOpts.RequestHeaders = handle.lookup.requestHeaders
	return handle, writeOpts, XqdStatusOK
}

// serializeRequestHeader formats the headers cache variants are matched
// against, including the Host that Go keeps apart.
func serializeRequestHeader(r *http.Request) []byte {
	var buf bytes.Buffer
	_ = r.Header.Write(&buf)
	if _, ok := r.Header["Host"]; !ok && r.Host != "" {
		buf.WriteString("Host: " + r.Host + "\r\n")
	}
	return buf.Bytes()
}

// xqd_http_cache_transaction_update freshens the found object with a new
// response head.
func (i *Instance) xqd_http_cache_transaction_update(
	cache_handle int32,
	resp_handle int32,
	options_mask uint32,
	options int32,
) int32 {
	i.abilog.Println("http_cache_transaction_update")

	handle, writeOpts, status := i.httpCacheWrite(cache_handle, resp_handle, options_mask, options)
	if status != XqdStatusOK {
		return status
	}
	if err := i.cache.TransactionUpdate(handle.Transaction, writeOpts); err != nil {
		return XqdError
	}
	i.cache.CompleteTransaction(handle.Transaction)

	return XqdStatusOK
}

// xqd_http_cache_transaction_update_and_return_fresh freshens the found object
// and returns a cache handle that serves it.
func (i *Instance) xqd_http_cache_transaction_update_and_return_fresh(
	cache_handle int32,
	resp_handle int32,
	options_mask uint32,
	options int32,
	cache_handle_out int32,
) int32 {
	i.abilog.Println("http_cache_transaction_update_and_return_fresh")

	handle, writeOpts, status := i.httpCacheWrite(cache_handle, resp_handle, options_mask, options)
	if status != XqdStatusOK {
		return status
	}
	if err := i.cache.TransactionUpdate(handle.Transaction, writeOpts); err != nil {
		return XqdError
	}
	fresh := usableTransaction(handle.Transaction.Key, handle.Transaction.Entry.Object)
	freshHandleID := i.newHTTPCacheHandle(fresh, handle.lookup)
	i.memory.WriteUint32(cache_handle_out, uint32(freshHandleID))
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

	handle := i.httpCacheHandle(cache_handle)
	if handle == nil {
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

	handle := i.httpCacheHandle(cache_handle)
	if handle == nil {
		return XqdErrInvalidHandle
	}

	err := i.cache.TransactionCancel(handle.Transaction)
	if err != nil {
		return XqdError
	}

	return XqdStatusOK
}

// xqd_http_cache_close closes a cache handle and abandons its transaction.
func (i *Instance) xqd_http_cache_close(cache_handle int32) int32 {
	i.abilog.Println("http_cache_close")

	if i.httpCacheHandle(cache_handle) == nil {
		return XqdErrInvalidHandle
	}
	handle := i.cacheHandles.Take(int(cache_handle))
	_ = i.cache.TransactionCancel(handle.Transaction)
	return XqdStatusOK
}

// xqd_http_cache_get_suggested_backend_request creates a backend request from cache state
func (i *Instance) xqd_http_cache_get_suggested_backend_request(
	cache_handle int32,
	req_handle_out int32,
) int32 {
	i.abilog.Println("http_cache_get_suggested_backend_request")

	handle := i.httpCacheHandle(cache_handle)
	if handle == nil {
		return XqdErrInvalidHandle
	}

	backendReq := cloneRequestHead(handle.lookup.request)
	if handle.storedResponse != nil {
		prepareRevalidationRequest(backendReq, handle.storedResponse.header)
	} else {
		prepareFullBackendRequest(backendReq)
	}

	reqID, reqHandle := i.requests.New()
	reqHandle.Request = backendReq
	reqHandle.version = handle.lookup.version

	i.abilog.Printf("http_cache_get_suggested_backend_request: created request url=%s method=%s", backendReq.URL, backendReq.Method)
	i.memory.WriteUint32(req_handle_out, uint32(reqID))

	return XqdStatusOK
}

// httpCacheLookup is what HTTP cache handles keep of the lookup that created
// them, a snapshot unaffected by later changes to the guest's request.
type httpCacheLookup struct {
	request        *http.Request
	version        int32
	time           time.Time
	requestHeaders []byte
}

func newHTTPCacheLookup(req *RequestHandle) *httpCacheLookup {
	return &httpCacheLookup{
		request:        cloneRequestHead(req.Request),
		version:        req.version,
		time:           time.Now(),
		requestHeaders: serializeRequestHeader(req.Request),
	}
}

// newHTTPCacheHandle registers a cache handle along with the response head it
// found at that point, as production keeps per handle.
func (i *Instance) newHTTPCacheHandle(tx *CacheTransaction, lookup *httpCacheLookup) int {
	id := i.cacheHandles.New(tx)
	handle := i.cacheHandles.Get(id)
	handle.lookup = lookup
	if tx.Entry != nil && tx.Entry.Object != nil {
		handle.storedResponse = tx.Entry.Object.Response
	}
	return id
}

// httpCacheHandle returns an HTTP cache handle, rejecting core cache ones like
// production does.
func (i *Instance) httpCacheHandle(cache_handle int32) *CacheHandle {
	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.lookup == nil {
		return nil
	}
	return handle
}

// httpCacheObject returns the object an HTTP cache handle found.
func (i *Instance) httpCacheObject(cache_handle int32) *CachedObject {
	handle := i.httpCacheHandle(cache_handle)
	if handle == nil || handle.Transaction.Entry == nil {
		return nil
	}
	return handle.Transaction.Entry.Object
}

// cloneRequestHead copies the method, URL, headers and host of a request.
// The protocol version lives on the request handle, and the body is separate.
func cloneRequestHead(r *http.Request) *http.Request {
	u := *r.URL
	header := r.Header.Clone()
	if header == nil {
		header = http.Header{}
	}
	return &http.Request{
		Method: r.Method,
		URL:    &u,
		Header: header,
		Host:   r.Host,
	}
}

// prepareFullBackendRequest mirrors what production's cache-semantics crate
// does to get a complete response from the backend.
// Conditional headers are removed from safe requests, range headers from all
// of them, and HEAD becomes GET.
func prepareFullBackendRequest(r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		for _, name := range []string{"If-Modified-Since", "If-Unmodified-Since", "If-None-Match", "If-Match", "If-Range"} {
			r.Header.Del(name)
		}
	}
	if r.Method == http.MethodHead {
		r.Method = http.MethodGet
	}
	r.Header.Del("Range")
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

	handle := i.httpCacheHandle(cache_handle)
	if handle == nil {
		return XqdErrInvalidHandle
	}

	resp := i.responses.Get(int(resp_handle))
	if resp == nil {
		return XqdErrInvalidHandle
	}

	// HTTP cache write options structure layout (from Viceroy ABI):
	// Offset | Field                        | Size
	// -------|------------------------------|------
	//      0 | max_age_ns                   | 8 bytes (u64)
	//      8 | vary_rule_ptr                | 4 bytes (*u8)
	//     12 | vary_rule_len                | 4 bytes (usize)
	//     16 | initial_age_ns               | 8 bytes (u64)
	//     24 | stale_while_revalidate_ns    | 8 bytes (u64)
	//     32 | surrogate_keys_ptr           | 4 bytes (*u8)
	//     36 | surrogate_keys_len           | 4 bytes (usize)
	//     40 | length                       | 8 bytes (u64)

	const HttpCacheWriteOptionsMaskMaxAgeNs = 1 << 0

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

// splitCacheControl splits a Cache-Control header value into individual directives.
// Directives are comma-separated and whitespace is trimmed from each directive.
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

// parseInt parses a non-negative integer from a string without using strconv.
// Returns an error if the string contains non-digit characters.
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

// xqd_http_cache_prepare_response_for_storage suggests a storage action for a
// backend response and returns the response to store.
func (i *Instance) xqd_http_cache_prepare_response_for_storage(
	cache_handle int32,
	resp_handle int32,
	storage_action_out int32,
	resp_handle_out int32,
) int32 {
	i.abilog.Println("http_cache_prepare_response_for_storage")

	handle := i.httpCacheHandle(cache_handle)
	if handle == nil {
		return XqdErrInvalidHandle
	}

	resp := i.responses.Get(int(resp_handle))
	if resp == nil {
		return XqdErrInvalidHandle
	}

	// Production hands back a new handle and leaves the original one open.
	// The Rust SDK closes the original as soon as it gets the new one, so
	// returning the same handle would close the response it keeps using.
	response := *resp.Response
	response.Trailer = resp.Trailer.Clone()

	var storageAction uint32
	if stored := handle.storedResponse; stored != nil && resp.StatusCode == http.StatusNotModified {
		storageAction = HttpStorageActionUpdate
		response.StatusCode = stored.status
		response.Status = http.StatusText(stored.status)
		response.Header = freshenStoredHeader(stored.header, resp.Header)
	} else {
		storageAction = storageActionFor(handle.lookup.request.Method, resp.Response)
		response.Header = resp.Header.Clone()
		if storageAction == HttpStorageActionInsert {
			stripHeadersForStorage(response.Header, response.Header)
		}
	}

	preparedID, prepared := i.responses.New()
	*prepared = *resp
	prepared.Response = &response
	i.memory.WriteUint32(storage_action_out, storageAction)
	i.memory.WriteUint32(resp_handle_out, uint32(preparedID))

	return XqdStatusOK
}

// xqd_http_cache_get_found_response returns the stored response and its body.
// With transform_for_client, it is adapted to the lookup request as production
// does, except that ranges are not answered with a 206 yet.
func (i *Instance) xqd_http_cache_get_found_response(
	cache_handle int32,
	transform_for_client uint32,
	resp_handle_out int32,
	body_handle_out int32,
) int32 {
	i.abilog.Println("http_cache_get_found_response")

	handle := i.httpCacheHandle(cache_handle)
	if handle == nil || handle.Transaction.Entry == nil {
		return XqdErrInvalidHandle
	}

	entry := handle.Transaction.Entry
	stored := handle.storedResponse
	if !entry.State.Found || !entry.State.Usable || entry.Object == nil || stored == nil {
		return XqdErrNone
	}

	status, withBody := stored.status, true
	var header http.Header
	switch {
	case transform_for_client != 1:
		header = stored.header.Clone()
	case stored.validatorsMatch(handle.lookup.request):
		status, withBody = http.StatusNotModified, false
		header = notModifiedHeader(stored.header)
		header.Set("Accept-Ranges", "bytes")
	default:
		header = stored.header.Clone()
		header.Set("Age", ageHeaderValue(stored.currentAge(time.Now())))
		header.Set("Accept-Ranges", "bytes")
		withBody = handle.lookup.request.Method != http.MethodHead
	}

	respID, respHandle := i.responses.New()
	respHandle.StatusCode = status
	respHandle.Status = http.StatusText(status)
	respHandle.Header = header

	var bodyID int
	if withBody {
		bodyID, _ = i.newCacheObjectBody(entry.Object, 0, 0, false)
	} else {
		bodyID, _ = i.bodies.NewBuffer()
	}

	i.memory.WriteUint32(resp_handle_out, uint32(respID))
	i.memory.WriteUint32(body_handle_out, uint32(bodyID))

	return XqdStatusOK
}

// xqd_http_cache_get_state gets cache lookup state
func (i *Instance) xqd_http_cache_get_state(
	cache_handle int32,
	cache_lookup_state_out int32,
) int32 {
	i.abilog.Println("http_cache_get_state")

	handle := i.httpCacheHandle(cache_handle)
	if handle == nil || handle.Transaction.Entry == nil {
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

	obj := i.httpCacheObject(cache_handle)
	if obj == nil {
		return XqdErrInvalidHandle
	}

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

	obj := i.httpCacheObject(cache_handle)
	if obj == nil {
		return XqdErrInvalidHandle
	}

	i.memory.WriteUint64(duration_out, obj.MaxAgeNs)

	return XqdStatusOK
}

// xqd_http_cache_get_stale_while_revalidate_ns gets stale-while-revalidate duration
func (i *Instance) xqd_http_cache_get_stale_while_revalidate_ns(
	cache_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("http_cache_get_stale_while_revalidate_ns")

	obj := i.httpCacheObject(cache_handle)
	if obj == nil {
		return XqdErrInvalidHandle
	}

	// Always return the value (even if 0) - the Rust library expects it to be present
	i.memory.WriteUint64(duration_out, obj.StaleWhileRevalidateNs)

	return XqdStatusOK
}

// xqd_http_cache_get_stale_if_error_ns gets stale-if-error duration
func (i *Instance) xqd_http_cache_get_stale_if_error_ns(
	cache_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("http_cache_get_stale_if_error_ns")

	obj := i.httpCacheObject(cache_handle)
	if obj == nil {
		return XqdErrInvalidHandle
	}

	i.memory.WriteUint64(duration_out, obj.StaleIfErrorNs)

	return XqdStatusOK
}

// xqd_http_cache_transaction_choose_stale resolves a transaction with a stale response
// if one is available within the stale-if-error window
func (i *Instance) xqd_http_cache_transaction_choose_stale(cache_handle int32) int32 {
	i.abilog.Println("http_cache_transaction_choose_stale")

	handle := i.httpCacheHandle(cache_handle)
	if handle == nil {
		return XqdErrInvalidHandle
	}

	if !i.cache.TransactionChooseStale(handle.Transaction) {
		return XqdErrNone
	}

	// Complete the transaction (like transaction_update_and_return_fresh)
	i.cache.CompleteTransaction(handle.Transaction)
	return XqdStatusOK
}

// xqd_http_cache_get_age_ns gets age of cached object
func (i *Instance) xqd_http_cache_get_age_ns(
	cache_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("http_cache_get_age_ns")

	obj := i.httpCacheObject(cache_handle)
	if obj == nil {
		return XqdErrInvalidHandle
	}

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

	obj := i.httpCacheObject(cache_handle)
	if obj == nil {
		return XqdErrInvalidHandle
	}

	i.memory.WriteUint64(hits_out, obj.HitCount.Load())

	return XqdStatusOK
}

// xqd_http_cache_get_sensitive_data checks if cached data is sensitive
func (i *Instance) xqd_http_cache_get_sensitive_data(
	cache_handle int32,
	sensitive_out int32,
) int32 {
	i.abilog.Println("http_cache_get_sensitive_data")

	obj := i.httpCacheObject(cache_handle)
	if obj == nil {
		return XqdErrInvalidHandle
	}

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

	obj := i.httpCacheObject(cache_handle)
	if obj == nil {
		return XqdErrInvalidHandle
	}

	// Join surrogate keys with spaces (empty list is OK - write 0 bytes)
	keysStr := ""
	if len(obj.SurrogateKeys) > 0 {
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

	obj := i.httpCacheObject(cache_handle)
	if obj == nil {
		return XqdErrInvalidHandle
	}

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

// readHttpCacheWriteOptions reads HTTP cache write options from guest memory.
// The options structure contains max age, vary rules, surrogate keys, and other cache metadata.
func (i *Instance) readHttpCacheWriteOptions(mask uint32, optionsPtr int32) *CacheWriteOptions {
	opts := &CacheWriteOptions{}

	// HTTP cache write options structure layout (from Viceroy ABI):
	// Offset | Field                        | Size
	// -------|------------------------------|------
	//      0 | max_age_ns                   | 8 bytes (u64)
	//      8 | vary_rule_ptr                | 4 bytes (*u8)
	//     12 | vary_rule_len                | 4 bytes (usize)
	//     16 | initial_age_ns               | 8 bytes (u64)
	//     24 | stale_while_revalidate_ns    | 8 bytes (u64)
	//     32 | surrogate_keys_ptr           | 4 bytes (*u8)
	//     36 | surrogate_keys_len           | 4 bytes (usize)
	//     40 | length                       | 8 bytes (u64)

	// Read max_age_ns (always present at offset 0)
	opts.MaxAgeNs = i.memory.ReadUint64(optionsPtr)

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

	// Read stale_if_error_ns at offset 48 (after length at offset 40, which is 8 bytes)
	if mask&HttpCacheWriteOptionsMaskStaleIfErrorNs != 0 {
		val := i.memory.ReadUint64(optionsPtr + 48)
		opts.StaleIfErrorNs = &val
	}

	return opts
}

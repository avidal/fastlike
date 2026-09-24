package fastlike

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
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

// xqd_http_cache_get_suggested_cache_key writes the request's default cache
// key, which production computes from the host and the path and query.
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

	key := logicalCacheKey(req.Request)
	nwrittenAddr, status := i.memory.wiggleField(nwritten_out, 0, 4)
	if status != XqdStatusOK {
		return status
	}
	i.memory.PutUint32(uint32(len(key)), nwrittenAddr)
	if uint32(key_out_len) < uint32(len(key)) {
		return XqdErrBufferLength
	}
	if _, err := i.memory.WriteAt(key[:], int64(key_out_ptr)); err != nil {
		return XqdErrInvalidArgument
	}
	return XqdStatusOK
}

// logicalCacheKeySalt starts every default HTTP cache key in production.
const logicalCacheKeySalt = "CachedSession\x03\x02\x01"

// logicalCacheKey is production's default HTTP cache key.
// It hashes the Host header, or the URI host without its port when there is
// none, lowercased, followed by the path and query.
func logicalCacheKey(r *http.Request) [32]byte {
	host, ok := hostHeader(r)
	if !ok {
		host = uriHost(r.URL)
	}

	pathAndQuery := r.URL.EscapedPath()
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		pathAndQuery += "?" + r.URL.RawQuery
	}
	if pathAndQuery == "" {
		pathAndQuery = "/"
	}

	buf := append([]byte(logicalCacheKeySalt), 0)
	buf = append(append(buf, strings.ToLower(host)...), 0)
	buf = append(append(buf, pathAndQuery...), 0)
	return sha256.Sum256(buf)
}

// hostHeader returns the request's first Host header.
// Go moves it out of the header map of incoming requests, into Request.Host.
func hostHeader(r *http.Request) (string, bool) {
	if host, ok := firstHeaderValue(r.Header, "Host"); ok {
		return host, true
	}
	return r.Host, r.Host != ""
}

// uriHost is the host of a URL as the http crate reports it: without the
// port, but with the brackets of an IPv6 address.
func uriHost(u *url.URL) string {
	if strings.HasPrefix(u.Host, "[") {
		if end := strings.IndexByte(u.Host, ']'); end >= 0 {
			return u.Host[:end+1]
		}
		return u.Host
	}
	host, _, _ := strings.Cut(u.Host, ":")
	return host
}

// prepareHTTPCacheLookup validates the lookup options the way production does,
// before the request handle, and returns the lookup and the key it uses.
// Fastlike sends requests for unknown backends to the default backend, so
// unlike production it does not reject a backend name it does not know.
func (i *Instance) prepareHTTPCacheLookup(name string, req_handle int32, options_mask uint32, options int32) (*httpCacheLookup, []byte, int32) {
	requireFlags(name, "http_cache_lookup_options_mask", int32(options_mask), httpCacheLookupOptionsKnown)

	addr, status := i.memory.wiggleRecord(options, 4, httpCacheLookupOptionsSize)
	if status != XqdStatusOK {
		return nil, nil, status
	}
	var key []byte
	if options_mask&HttpCacheLookupOptionsMaskOverrideKey != 0 {
		overrideKey, ok := i.readGuestBytes(int32(addr))
		if !ok || len(overrideKey) != 32 {
			return nil, nil, XqdErrInvalidArgument
		}
		key = overrideKey
	}
	if options_mask&HttpCacheLookupOptionsMaskBackendName != 0 {
		if backend, ok := i.readGuestBytes(int32(addr + 8)); !ok || !utf8.Valid(backend) {
			return nil, nil, XqdErrInvalidArgument
		}
	}

	req := i.requests.Get(int(req_handle))
	if req == nil {
		return nil, nil, XqdErrInvalidHandle
	}
	if key == nil {
		logical := logicalCacheKey(req.Request)
		key = logical[:]
	}
	return newHTTPCacheLookup(req), key, XqdStatusOK
}

// xqd_http_cache_lookup performs a non-transactional cache lookup using a request
func (i *Instance) xqd_http_cache_lookup(
	req_handle int32,
	options_mask uint32,
	options int32,
	cache_handle_out int32,
) int32 {
	i.abilog.Println("http_cache_lookup")

	lookup, key, status := i.prepareHTTPCacheLookup("http_cache_lookup", req_handle, options_mask, options)
	if status != XqdStatusOK {
		return status
	}

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

	lookup, key, status := i.prepareHTTPCacheLookup("http_cache_transaction_lookup", req_handle, options_mask, options)
	if status != XqdStatusOK {
		return status
	}

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
	// Production adds this key back when the guest replaced the suggested
	// ones, so that purging a URL still works.
	if keySK := keySurrogateKey(handle.Transaction.Key); !slices.Contains(writeOpts.SurrogateKeys, keySK) {
		writeOpts.SurrogateKeys = append(writeOpts.SurrogateKeys, keySK)
	}
	return handle, writeOpts, XqdStatusOK
}

// keySurrogateKey is the surrogate key production derives from a cache key,
// the uppercase hex form that URL purges use.
func keySurrogateKey(key []byte) string {
	return strings.ToUpper(hex.EncodeToString(key))
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
	i.cache.TransactionCancel(handle.Transaction)

	return XqdStatusOK
}

// xqd_http_cache_transaction_abandon gives up the obligation of a handle.
// Like production, a handle without one is refused.
func (i *Instance) xqd_http_cache_transaction_abandon(cache_handle int32) int32 {
	i.abilog.Println("http_cache_transaction_abandon")

	handle := i.httpCacheHandle(cache_handle)
	if handle == nil || !i.cache.TransactionCancel(handle.Transaction) {
		return XqdErrInvalidHandle
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
	i.cache.TransactionCancel(handle.Transaction)
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

// newHTTPCacheHandle registers a cache handle along with the response head and
// the length it found at that point, as production keeps per handle.
func (i *Instance) newHTTPCacheHandle(tx *CacheTransaction, lookup *httpCacheLookup) int {
	id := i.cacheHandles.New(tx)
	handle := i.cacheHandles.Get(id)
	handle.lookup = lookup
	if tx.Entry != nil && tx.Entry.Object != nil {
		handle.storedResponse = tx.Entry.Object.Response
		handle.foundLength, handle.foundLengthKnown = tx.Entry.Object.KnownLength()
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

// httpCacheObject returns the object an HTTP cache handle found, or the status
// an accessor returns without one.
func (i *Instance) httpCacheObject(cache_handle int32) (*CachedObject, int32) {
	handle := i.httpCacheHandle(cache_handle)
	if handle == nil || handle.Transaction.Entry == nil {
		return nil, XqdErrInvalidHandle
	}
	if handle.Transaction.Entry.Object == nil {
		return nil, XqdErrNone
	}
	return handle.Transaction.Entry.Object, XqdStatusOK
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

	requireFlags("http_cache_get_suggested_cache_options", "http_cache_write_options_mask", int32(requested_mask), httpCacheWriteOptionsKnown)

	resp := i.responses.Get(int(resp_handle))
	if resp == nil {
		return XqdErrInvalidHandle
	}
	handle := i.httpCacheHandle(cache_handle)
	if handle == nil {
		return XqdErrInvalidHandle
	}

	suggested := suggestCacheOptions(resp.Header, time.Now())
	w := &optionsWriter{mem: i.memory, pointers: requested_options, out: options_out}
	w.u64(httpCacheOptionsMaxAgeNs, suggested.maxAgeNs)
	var maskOut uint32
	if requested_mask&HttpCacheWriteOptionsMaskVaryRule != 0 {
		maskOut |= HttpCacheWriteOptionsMaskVaryRule
		w.bytes(httpCacheOptionsVaryRule, []byte(suggested.varyRule))
	}
	if requested_mask&HttpCacheWriteOptionsMaskInitialAgeNs != 0 {
		maskOut |= HttpCacheWriteOptionsMaskInitialAgeNs
		w.u64(httpCacheOptionsInitialAgeNs, suggested.initialAgeNs)
	}
	if requested_mask&HttpCacheWriteOptionsMaskStaleWhileRevalidateNs != 0 {
		maskOut |= HttpCacheWriteOptionsMaskStaleWhileRevalidateNs
		w.u64(httpCacheOptionsStaleWhileRevalidateNs, suggested.staleWhileRevalidateNs)
	}
	if requested_mask&HttpCacheWriteOptionsMaskStaleIfErrorNs != 0 {
		maskOut |= HttpCacheWriteOptionsMaskStaleIfErrorNs
		w.u64(httpCacheOptionsStaleIfErrorNs, suggested.staleIfErrorNs)
	}
	if requested_mask&HttpCacheWriteOptionsMaskSurrogateKeys != 0 {
		maskOut |= HttpCacheWriteOptionsMaskSurrogateKeys
		w.bytes(httpCacheOptionsSurrogateKeys, []byte(keySurrogateKey(handle.Transaction.Key)))
	}
	// Production never knows the length here and never suggests sensitive
	// data, so those bits are never reported.
	if maskAddr, ok := w.field(options_mask_out, 0, 4); ok {
		i.memory.PutUint32(maskOut, maskAddr)
	}
	if w.status != XqdStatusOK {
		return w.status
	}
	if w.tooSmall {
		return XqdErrBufferLength
	}
	return XqdStatusOK
}

// Offsets of the http_cache_write_options fields.
const (
	httpCacheOptionsMaxAgeNs               = 0
	httpCacheOptionsVaryRule               = 8
	httpCacheOptionsInitialAgeNs           = 16
	httpCacheOptionsStaleWhileRevalidateNs = 24
	httpCacheOptionsSurrogateKeys          = 32
	httpCacheOptionsLength                 = 40
	httpCacheOptionsStaleIfErrorNs         = 48
)

// optionsWriter writes suggested http_cache_write_options to out, checking
// each field like wiggle, and does nothing more once a check fails.
type optionsWriter struct {
	mem           *Memory
	pointers, out int32
	status        int32
	tooSmall      bool
}

func (w *optionsWriter) field(record int32, offset, size uint32) (int64, bool) {
	if w.status != XqdStatusOK {
		return 0, false
	}
	var addr int64
	addr, w.status = w.mem.wiggleField(record, offset, size)
	return addr, w.status == XqdStatusOK
}

func (w *optionsWriter) u64(offset uint32, v uint64) {
	if addr, ok := w.field(w.out, offset, 8); ok {
		w.mem.PutUint64(v, addr)
	}
}

// bytes suggests a vary rule or surrogate keys in the buffer that pointers
// names at the pointer and length fields found at offset.
// Like production, it writes the needed length to out before checking the
// buffer, and records a buffer that is too small instead of failing at once.
func (w *optionsWriter) bytes(offset uint32, data []byte) {
	lenAddr, ok := w.field(w.out, offset+4, 4)
	if !ok {
		return
	}
	w.mem.PutUint32(uint32(len(data)), lenAddr)

	bufPtrAddr, _ := w.field(w.pointers, offset, 4)
	bufLenAddr, ok := w.field(w.pointers, offset+4, 4)
	if !ok {
		return
	}
	bufPtr, bufLen := w.mem.Uint32(bufPtrAddr), w.mem.Uint32(bufLenAddr)
	if bufLen < uint32(len(data)) {
		w.tooSmall = true
		return
	}
	if len(data) > 0 {
		if _, err := w.mem.WriteAt(data, int64(bufPtr)); err != nil {
			w.status = XqdErrInvalidArgument
			return
		}
	}
	if ptrAddr, ok := w.field(w.out, offset, 4); ok {
		w.mem.PutUint32(bufPtr, ptrAddr)
	}
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
// does.
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
	var bodyRange byteRange
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
		// The body follows CacheD's rules for the range, which fall back to
		// the whole object when the range does not fit, while Content-Range
		// always reflects the request, whatever the stored status.
		if r, ok := requestedRange(handle.lookup.request); ok {
			bodyRange = r
			if contentRange, ok := r.contentRange(handle.foundLength, handle.foundLengthKnown); ok {
				status = http.StatusPartialContent
				header.Add("Content-Range", contentRange)
			}
		}
		header.Set("Accept-Ranges", "bytes")
		withBody = handle.lookup.request.Method != http.MethodHead
	}

	respID, respHandle := i.responses.New()
	respHandle.StatusCode = status
	respHandle.Status = http.StatusText(status)
	respHandle.Header = header

	var bodyID int
	if withBody {
		reader := cacheRangeReader(entry.Object, bodyRange, true)
		bodyID, _ = i.bodies.NewReader(io.NopCloser(reader))
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

	obj, status := i.httpCacheObject(cache_handle)
	if status != XqdStatusOK {
		return status
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

	obj, status := i.httpCacheObject(cache_handle)
	if status != XqdStatusOK {
		return status
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

	obj, status := i.httpCacheObject(cache_handle)
	if status != XqdStatusOK {
		return status
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

	obj, status := i.httpCacheObject(cache_handle)
	if status != XqdStatusOK {
		return status
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

	obj, status := i.httpCacheObject(cache_handle)
	if status != XqdStatusOK {
		return status
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

	obj, status := i.httpCacheObject(cache_handle)
	if status != XqdStatusOK {
		return status
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

	obj, status := i.httpCacheObject(cache_handle)
	if status != XqdStatusOK {
		return status
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

	obj, status := i.httpCacheObject(cache_handle)
	if status != XqdStatusOK {
		return status
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

	obj, status := i.httpCacheObject(cache_handle)
	if status != XqdStatusOK {
		return status
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

	opts.MaxAgeNs = i.memory.ReadUint64(optionsPtr + httpCacheOptionsMaxAgeNs)

	if mask&HttpCacheWriteOptionsMaskVaryRule != 0 {
		varyPtr := int32(i.memory.Uint32(int64(optionsPtr + httpCacheOptionsVaryRule)))
		varyLen := int32(i.memory.Uint32(int64(optionsPtr + httpCacheOptionsVaryRule + 4)))
		if varyLen > 0 {
			varyBuf := make([]byte, varyLen)
			_, _ = i.memory.ReadAt(varyBuf, int64(varyPtr))
			opts.VaryRule = string(varyBuf)
		}
	}

	if mask&HttpCacheWriteOptionsMaskInitialAgeNs != 0 {
		val := i.memory.ReadUint64(optionsPtr + httpCacheOptionsInitialAgeNs)
		opts.InitialAgeNs = &val
	}

	if mask&HttpCacheWriteOptionsMaskStaleWhileRevalidateNs != 0 {
		val := i.memory.ReadUint64(optionsPtr + httpCacheOptionsStaleWhileRevalidateNs)
		opts.StaleWhileRevalidateNs = &val
	}

	if mask&HttpCacheWriteOptionsMaskSurrogateKeys != 0 {
		keysPtr := int32(i.memory.Uint32(int64(optionsPtr + httpCacheOptionsSurrogateKeys)))
		keysLen := int32(i.memory.Uint32(int64(optionsPtr + httpCacheOptionsSurrogateKeys + 4)))
		if keysLen > 0 {
			keysBuf := make([]byte, keysLen)
			_, _ = i.memory.ReadAt(keysBuf, int64(keysPtr))
			keysStr := string(keysBuf)
			opts.SurrogateKeys = splitSurrogateKeys(keysStr)
		}
	}

	if mask&HttpCacheWriteOptionsMaskLength != 0 {
		val := i.memory.ReadUint64(optionsPtr + httpCacheOptionsLength)
		opts.Length = &val
	}

	if mask&HttpCacheWriteOptionsMaskSensitiveData != 0 {
		opts.SensitiveData = true
	}

	if mask&HttpCacheWriteOptionsMaskStaleIfErrorNs != 0 {
		val := i.memory.ReadUint64(optionsPtr + httpCacheOptionsStaleIfErrorNs)
		opts.StaleIfErrorNs = &val
	}

	return opts
}

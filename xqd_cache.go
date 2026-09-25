package fastlike

import (
	"bytes"
	"errors"
	"io"
	"math"
	"sync"
	"time"
)

// xqd_cache_lookup performs a non-transactional cache lookup
// cache_key: pointer to cache key bytes
// cache_key_len: length of cache key
// options_mask: bitmask of which options are set
// options: pointer to CacheLookupOptions struct
// cache_handle_out: output pointer for cache handle
func (i *Instance) xqd_cache_lookup(
	cache_key int32,
	cache_key_len int32,
	options_mask uint32,
	options int32,
	cache_handle_out int32,
) int32 {
	i.abilog.Println("xqd_cache_lookup")
	i.deepBumpCacheLookup()

	key := make([]byte, cache_key_len)
	_, _ = i.memory.ReadAt(key, int64(cache_key))

	lookupOpts := i.readCacheLookupOptions(options_mask, options)

	entry := i.cache.Lookup(key, lookupOpts)
	if entry != nil {
		i.deepBumpCacheOutcome(entry.State)
	} else {
		i.deepBumpCacheOutcome(CacheState{})
	}

	handleID := i.cacheHandles.New(settledTransaction(key, entry, lookupOpts))
	i.memory.WriteUint32(cache_handle_out, uint32(handleID))

	return XqdStatusOK
}

// xqd_cache_insert performs a non-transactional cache insert
func (i *Instance) xqd_cache_insert(
	cache_key int32,
	cache_key_len int32,
	options_mask uint32,
	options int32,
	body_handle_out int32,
) int32 {
	i.abilog.Println("xqd_cache_insert")
	i.deepBumpCacheInsert()

	key := make([]byte, cache_key_len)
	_, _ = i.memory.ReadAt(key, int64(cache_key))
	writeOpts, status := i.readCacheWriteOptions(options_mask, options)
	if status != XqdStatusOK {
		return status
	}

	obj := i.cache.Insert(key, writeOpts)
	bodyID := i.newCacheInsertBody(obj, key)
	i.memory.WriteUint32(body_handle_out, uint32(bodyID))

	return XqdStatusOK
}

// xqd_cache_transaction_lookup performs a transactional cache lookup with request collapsing
func (i *Instance) xqd_cache_transaction_lookup(
	cache_key int32,
	cache_key_len int32,
	options_mask uint32,
	options int32,
	cache_handle_out int32,
) int32 {
	i.abilog.Println("xqd_cache_transaction_lookup")
	i.deepBumpCacheLookup()

	key := make([]byte, cache_key_len)
	_, _ = i.memory.ReadAt(key, int64(cache_key))

	lookupOpts := i.readCacheLookupOptions(options_mask, options)

	tx := i.cache.TransactionLookup(key, lookupOpts, i)
	if tx != nil && tx.Entry != nil {
		i.deepBumpCacheOutcome(tx.Entry.State)
	} else {
		i.deepBumpCacheOutcome(CacheState{})
	}

	handleID := i.cacheHandles.New(tx)
	i.memory.WriteUint32(cache_handle_out, uint32(handleID))

	return XqdStatusOK
}

// xqd_cache_transaction_lookup_async performs an async transactional lookup
func (i *Instance) xqd_cache_transaction_lookup_async(
	cache_key int32,
	cache_key_len int32,
	options_mask uint32,
	options int32,
	cache_busy_handle_out int32,
) int32 {
	i.abilog.Println("xqd_cache_transaction_lookup_async")

	key := make([]byte, cache_key_len)
	_, _ = i.memory.ReadAt(key, int64(cache_key))

	lookupOpts := i.readCacheLookupOptions(options_mask, options)

	// Start async lookup (in our case, it's immediate but we return a busy handle)
	tx := i.cache.TransactionLookup(key, lookupOpts, i)
	i.deepBumpCacheLookup()
	if tx != nil && tx.Entry != nil {
		i.deepBumpCacheOutcome(tx.Entry.State)
	} else {
		i.deepBumpCacheOutcome(CacheState{})
	}

	busyHandleID := i.cacheBusyHandles.New(tx)
	i.memory.WriteUint32(cache_busy_handle_out, uint32(busyHandleID))

	return XqdStatusOK
}

// xqd_cache_busy_handle_wait waits for an async cache lookup to complete
func (i *Instance) xqd_cache_busy_handle_wait(
	busy_handle int32,
	cache_handle_out int32,
) int32 {
	i.abilog.Println("xqd_cache_busy_handle_wait")

	busyHandle := i.cacheBusyHandles.Take(int(busy_handle))
	if busyHandle == nil {
		return XqdErrInvalidHandle
	}

	// Wait for transaction to complete
	<-busyHandle.Transaction.ready

	// Create a cache handle from the transaction
	handleID := i.cacheHandles.New(busyHandle.Transaction)
	i.memory.WriteUint32(cache_handle_out, uint32(handleID))

	return XqdStatusOK
}

// xqd_cache_transaction_insert inserts into cache within a transaction
func (i *Instance) xqd_cache_transaction_insert(
	cache_handle int32,
	options_mask uint32,
	options int32,
	body_handle_out int32,
) int32 {
	i.abilog.Println("xqd_cache_transaction_insert")
	i.deepBumpCacheInsert()

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil {
		return XqdErrInvalidHandle
	}

	writeOpts, status := i.readCacheWriteOptions(options_mask, options)
	if status != XqdStatusOK {
		return status
	}

	obj, err := i.cache.TransactionInsert(handle.Transaction, writeOpts)
	if err != nil {
		return transactionWriteStatus(err)
	}
	bodyID := i.newCacheInsertBody(obj, handle.Transaction.Key)
	i.memory.WriteUint32(body_handle_out, uint32(bodyID))

	return XqdStatusOK
}

// xqd_cache_transaction_insert_and_stream_back inserts and streams back simultaneously.
// This allows the guest program to write data to the cache while simultaneously reading it back,
// which is useful for streaming scenarios where the data needs to be both cached and sent downstream.
func (i *Instance) xqd_cache_transaction_insert_and_stream_back(
	cache_handle int32,
	options_mask uint32,
	options int32,
	body_handle_out int32,
	cache_handle_out int32,
) int32 {
	i.abilog.Println("xqd_cache_transaction_insert_and_stream_back")
	i.deepBumpCacheInsert()

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil {
		return XqdErrInvalidHandle
	}

	writeOpts, status := i.readCacheWriteOptions(options_mask, options)
	if status != XqdStatusOK {
		return status
	}

	entry, err := i.cache.TransactionInsertAndStreamBack(handle.Transaction, writeOpts)
	if err != nil {
		return transactionWriteStatus(err)
	}
	writeBodyID := i.newCacheInsertBody(entry.Object, handle.Transaction.Key)
	readHandleID := i.cacheHandles.New(settledTransaction(handle.Transaction.Key, entry, nil))

	i.memory.WriteUint32(body_handle_out, uint32(writeBodyID))
	i.memory.WriteUint32(cache_handle_out, uint32(readHandleID))

	return XqdStatusOK
}

// xqd_cache_transaction_update updates metadata for a cached object
func (i *Instance) xqd_cache_transaction_update(
	cache_handle int32,
	options_mask uint32,
	options int32,
) int32 {
	i.abilog.Println("xqd_cache_transaction_update")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil {
		return XqdErrInvalidHandle
	}

	writeOpts, status := i.readCacheWriteOptions(options_mask, options)
	if status != XqdStatusOK {
		return status
	}

	if err := i.cache.TransactionUpdate(handle.Transaction, writeOpts); err != nil {
		return transactionWriteStatus(err)
	}

	return XqdStatusOK
}

// transactionWriteStatus maps a refused cache write to its status.
func transactionWriteStatus(err error) int32 {
	if errors.Is(err, errNoObligation) {
		return XqdErrInvalidHandle
	}
	return XqdError
}

// xqd_cache_transaction_cancel gives up the obligation of a cache handle.
// Like production, a handle without one is refused.
func (i *Instance) xqd_cache_transaction_cancel(cache_handle int32) int32 {
	i.abilog.Println("xqd_cache_transaction_cancel")

	handle := i.coreCacheHandle(cache_handle)
	if handle == nil || !i.cache.TransactionCancel(handle.Transaction) {
		return XqdErrInvalidHandle
	}

	return XqdStatusOK
}

// xqd_cache_close_busy closes a busy cache handle
func (i *Instance) xqd_cache_close_busy(busy_handle int32) int32 {
	i.abilog.Println("xqd_cache_close_busy")

	busyHandle := i.cacheBusyHandles.Take(int(busy_handle))
	if busyHandle == nil {
		return XqdErrInvalidHandle
	}

	return XqdStatusOK
}

// xqd_cache_close closes a cache handle.
// The SDK drops unused replace handles through this call too.
func (i *Instance) xqd_cache_close(cache_handle int32) int32 {
	i.abilog.Println("xqd_cache_close")

	if replace := i.cacheReplaceHandles.Take(int(cache_handle)); replace != nil {
		i.cache.ReplaceAbandon(replace.Replace)
		return XqdStatusOK
	}

	handle := i.cacheHandles.Take(int(cache_handle))
	if handle == nil {
		return XqdErrInvalidHandle
	}
	if handle.Transaction != nil && i.cache != nil {
		// An obligation to fetch that the guest walks away from must not keep
		// the key pending.
		i.cache.TransactionCancel(handle.Transaction)
	}

	return XqdStatusOK
}

// xqd_cache_get_state gets the cache lookup state flags
func (i *Instance) xqd_cache_get_state(
	cache_handle int32,
	cache_lookup_state_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_state")

	handle := i.coreCacheHandle(cache_handle)
	if handle == nil {
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

// xqd_cache_get_user_metadata gets user metadata from cached object, including
// an expired one the handle has to replace.
// The required size is reported even when the buffer is too small.
func (i *Instance) xqd_cache_get_user_metadata(
	cache_handle int32,
	user_metadata_out_ptr int32,
	user_metadata_out_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_user_metadata")

	handle := i.coreCacheHandle(cache_handle)
	if handle == nil {
		return XqdErrInvalidHandle
	}
	entry := handle.Transaction.Entry
	if entry.Object == nil {
		return XqdErrNone
	}

	metadata := entry.Metadata.UserMetadata
	i.memory.WriteUint32(nwritten_out, uint32(len(metadata)))
	if len(metadata) > int(user_metadata_out_len) {
		return XqdErrBufferLength
	}
	_, _ = i.memory.WriteAt(metadata, int64(user_metadata_out_ptr))

	return XqdStatusOK
}

// coreCacheHandle returns a cache handle that holds a lookup result.
func (i *Instance) coreCacheHandle(cache_handle int32) *CacheHandle {
	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil {
		return nil
	}
	return handle
}

// foundCacheHandle returns a cache handle that found a usable object, or the
// status an accessor returns without one.
func (i *Instance) foundCacheHandle(cache_handle int32) (*CacheHandle, int32) {
	handle := i.coreCacheHandle(cache_handle)
	if handle == nil {
		return nil, XqdErrInvalidHandle
	}
	if !handle.Transaction.Entry.State.Found {
		return nil, XqdErrNone
	}
	return handle, XqdStatusOK
}

// foundCacheMetadata returns what a handle's lookup saw of the object it found.
func (i *Instance) foundCacheMetadata(cache_handle int32) (*ObjectMetadata, int32) {
	handle, status := i.foundCacheHandle(cache_handle)
	if status != XqdStatusOK {
		return nil, status
	}
	return &handle.Transaction.Entry.Metadata, XqdStatusOK
}

// xqd_cache_get_body gets the body of a cached object with optional range
func (i *Instance) xqd_cache_get_body(
	cache_handle int32,
	options_mask uint32,
	options int32,
	body_handle_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_body")

	handle, status := i.foundCacheHandle(cache_handle)
	if status != XqdStatusOK {
		return status
	}

	alwaysUseRequestedRange := handle.Transaction.Options != nil && handle.Transaction.Options.AlwaysUseRequestedRange
	bodyID, status := i.newCacheObjectBody(handle.Transaction.Entry, options_mask, options, alwaysUseRequestedRange)
	if status != XqdStatusOK {
		return status
	}
	i.memory.WriteUint32(body_handle_out, uint32(bodyID))

	return XqdStatusOK
}

// newCacheObjectBody creates a body handle reading the object found, restricted
// to the optional inclusive from/to range.
// A range outside the size known at lookup falls back to the whole body.
// With an unknown size the range is honored only when the guest insists, and
// the read then fails if the object ends early.
func (i *Instance) newCacheObjectBody(found *CacheEntry, options_mask uint32, options int32, alwaysUseRequestedRange bool) (int, int32) {
	r := byteRange{
		hasFirst: options_mask&CacheGetBodyOptionsMaskFrom != 0,
		hasLast:  options_mask&CacheGetBodyOptionsMaskTo != 0,
	}
	if r.hasFirst {
		r.first = i.memory.ReadUint64(options)
	}
	if r.hasLast {
		r.last = i.memory.ReadUint64(options + 8)
	}
	if r.hasFirst && r.hasLast && r.last < r.first {
		// An end before the start can never be satisfied, whatever the size.
		return 0, XqdErrInvalidArgument
	}

	reader := cacheRangeReader(found.Object, found.Metadata.Length, found.Metadata.LengthKnown, r, alwaysUseRequestedRange)
	bodyID, _ := i.bodies.NewReader(io.NopCloser(reader))
	return bodyID, XqdStatusOK
}

// cacheRangeReader reads r of obj the way CacheD serves it, given the length
// known at lookup.
func cacheRangeReader(obj *CachedObject, length uint64, lengthKnown bool, r byteRange, alwaysUseRequestedRange bool) io.Reader {
	reader := &cacheBodyReader{cache: obj}
	if !r.hasFirst && !r.hasLast {
		return reader
	}

	size, sizeKnown := int64(length), lengthKnown
	switch {
	case sizeKnown && !r.hasFirst:
		// A lone end bound asks for the last bytes, and CacheD serves the
		// whole object when that is none of them or more than it holds.
		if r.last > 0 && r.last <= uint64(size) {
			reader.offset = size - int64(r.last)
			reader.end = size
			reader.bounded = true
		}
	case sizeKnown:
		last := r.last
		if !r.hasLast {
			last = uint64(size) - 1
		}
		if r.first >= uint64(size) || last >= uint64(size) {
			return reader
		}
		reader.offset = int64(r.first)
		reader.end = int64(last) + 1
		reader.bounded = true
	case alwaysUseRequestedRange && !r.hasFirst:
		return &cacheSuffixReader{cache: obj, count: r.last}
	case alwaysUseRequestedRange:
		// CacheD commits to the range once the object reaches its start, and
		// serves the whole object if the object ends first, which is bound to
		// happen for a start no body can reach.
		if r.first >= math.MaxInt64 {
			return reader
		}
		reader.offset = int64(r.first)
		reader.wholeIfShort = true
		if r.hasLast {
			reader.end = int64(min(r.last, math.MaxInt64-1)) + 1
			reader.bounded = true
		}
	}
	return reader
}

// xqd_cache_get_length gets the length of a cached object
func (i *Instance) xqd_cache_get_length(
	cache_handle int32,
	length_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_length")

	found, status := i.foundCacheMetadata(cache_handle)
	if status != XqdStatusOK {
		return status
	}
	return i.writeKnownLength(found, length_out)
}

// writeKnownLength writes the length a lookup knew, or returns XqdErrNone when
// it knew none.
func (i *Instance) writeKnownLength(found *ObjectMetadata, length_out int32) int32 {
	if !found.LengthKnown {
		return XqdErrNone
	}
	i.memory.WriteUint64(length_out, found.Length)
	return XqdStatusOK
}

// xqd_cache_get_max_age_ns gets the max age in nanoseconds
func (i *Instance) xqd_cache_get_max_age_ns(
	cache_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_max_age_ns")

	found, status := i.foundCacheMetadata(cache_handle)
	if status != XqdStatusOK {
		return status
	}
	i.memory.WriteUint64(duration_out, found.MaxAgeNs)

	return XqdStatusOK
}

// xqd_cache_get_stale_while_revalidate_ns gets the stale-while-revalidate duration
func (i *Instance) xqd_cache_get_stale_while_revalidate_ns(
	cache_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_stale_while_revalidate_ns")

	found, status := i.foundCacheMetadata(cache_handle)
	if status != XqdStatusOK {
		return status
	}
	i.memory.WriteUint64(duration_out, found.StaleWhileRevalidateNs)

	return XqdStatusOK
}

// xqd_cache_get_age_ns gets the age of the cached object in nanoseconds
func (i *Instance) xqd_cache_get_age_ns(
	cache_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_age_ns")

	found, status := i.foundCacheMetadata(cache_handle)
	if status != XqdStatusOK {
		return status
	}
	i.memory.WriteUint64(duration_out, found.AgeNs)

	return XqdStatusOK
}

// xqd_cache_get_hits gets the hit count for a cached object
func (i *Instance) xqd_cache_get_hits(
	cache_handle int32,
	hits_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_hits")

	found, status := i.foundCacheMetadata(cache_handle)
	if status != XqdStatusOK {
		return status
	}
	i.memory.WriteUint64(hits_out, found.Hits)

	return XqdStatusOK
}

// Helper functions

// readCacheLookupOptions reads cache lookup options from guest memory and returns a CacheLookupOptions struct
func (i *Instance) readCacheLookupOptions(mask uint32, optionsPtr int32) *CacheLookupOptions {
	opts := &CacheLookupOptions{}

	if mask&CacheLookupOptionsMaskRequestHeaders != 0 {
		reqHandle := i.memory.Uint32(int64(optionsPtr))
		if reqHandle != uint32(HandleInvalid) {
			req := i.requests.Get(int(reqHandle))
			if req != nil {
				opts.RequestHeaders = serializeRequestHeader(req.Request)
			}
		}
	}

	if mask&CacheLookupOptionsMaskAlwaysUseRequestedRange != 0 {
		opts.AlwaysUseRequestedRange = true
	}

	return opts
}

// readCacheWriteOptions reads cache write options from guest memory
func (i *Instance) readCacheWriteOptions(mask uint32, optionsPtr int32) (*CacheWriteOptions, int32) {
	if !i.memory.validRange(int64(optionsPtr), cacheWriteOptionsSize) {
		return nil, XqdErrInvalidArgument
	}
	opts := &CacheWriteOptions{}

	// Read max_age_ns (always present at offset 0)
	opts.MaxAgeNs = i.memory.ReadUint64(optionsPtr)

	offset := int32(8) // Next field starts after max_age_ns (8 bytes)

	// Read request_headers handle
	if mask&CacheWriteOptionsMaskRequestHeaders != 0 {
		reqHandle := i.memory.Uint32(int64(optionsPtr + offset))
		if reqHandle != uint32(HandleInvalid) {
			req := i.requests.Get(int(reqHandle))
			if req != nil {
				opts.RequestHeaders = serializeRequestHeader(req.Request)
			}
		}
	}
	offset += 4

	// Read vary_rule
	if mask&CacheWriteOptionsMaskVaryRule != 0 {
		varyBuf, ok := i.readGuestBytes(optionsPtr + offset)
		if !ok {
			return nil, XqdErrInvalidArgument
		}
		opts.VaryRule = string(varyBuf)
	}
	offset += 8

	// The 64-bit fields that follow are 8-byte aligned in the C layout.
	offset += 4

	// Read initial_age_ns
	if mask&CacheWriteOptionsMaskInitialAgeNs != 0 {
		val := i.memory.ReadUint64(optionsPtr + offset)
		opts.InitialAgeNs = &val
	}
	offset += 8

	// Read stale_while_revalidate_ns
	if mask&CacheWriteOptionsMaskStaleWhileRevalidateNs != 0 {
		val := i.memory.ReadUint64(optionsPtr + offset)
		opts.StaleWhileRevalidateNs = &val
	}
	offset += 8

	// Read surrogate_keys
	if mask&CacheWriteOptionsMaskSurrogateKeys != 0 {
		keysBuf, ok := i.readGuestBytes(optionsPtr + offset)
		if !ok {
			return nil, XqdErrInvalidArgument
		}
		opts.SurrogateKeys = splitSurrogateKeys(string(keysBuf))
	}
	offset += 8

	// Read length
	if mask&CacheWriteOptionsMaskLength != 0 {
		val := i.memory.ReadUint64(optionsPtr + offset)
		opts.Length = &val
	}
	offset += 8

	// Read user_metadata
	if mask&CacheWriteOptionsMaskUserMetadata != 0 {
		mdBuf, ok := i.readGuestBytes(optionsPtr + offset)
		if !ok {
			return nil, XqdErrInvalidArgument
		}
		if len(mdBuf) > 0 {
			opts.UserMetadata = mdBuf
		}
	}
	offset += 8

	// Read edge_max_age_ns
	if mask&CacheWriteOptionsMaskEdgeMaxAgeNs != 0 {
		val := i.memory.ReadUint64(optionsPtr + offset)
		opts.EdgeMaxAgeNs = &val
	}

	// Read sensitive_data flag
	if mask&CacheWriteOptionsMaskSensitiveData != 0 {
		opts.SensitiveData = true
	}

	if mask&CacheWriteOptionsMaskService != 0 {
		// Writing on behalf of another service needs a privileged session.
		return nil, XqdErrUnsupported
	}

	return opts, XqdStatusOK
}

// readGuestBytes copies the byte range described by a pointer and length pair
// at fieldPtr, refusing lengths that do not fit in guest memory before any
// allocation happens.
func (i *Instance) readGuestBytes(fieldPtr int32) ([]byte, bool) {
	ptr := i.memory.Uint32(int64(fieldPtr))
	length := i.memory.Uint32(int64(fieldPtr + 4))
	if !i.memory.validRange(int64(ptr), uint64(length)) {
		return nil, false
	}
	if length == 0 {
		return nil, true
	}
	buf := make([]byte, length)
	_, _ = i.memory.ReadAt(buf, int64(ptr))
	return buf, true
}

// splitSurrogateKeys splits a space-separated string of surrogate keys into a slice.
// Returns nil for empty strings. Multiple consecutive spaces are treated as single separators.
func splitSurrogateKeys(s string) []string {
	if s == "" {
		return nil
	}
	var keys []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			if i > start {
				keys = append(keys, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		keys = append(keys, s[start:])
	}
	return keys
}

// cacheBodySink is the write end of a cache insert body.
// Appended bodies are copied into the object in the background, so the guest
// never waits for them.
// Unlike production, writes are never held back, so a stalled source cannot
// hang the guest.
type cacheBodySink struct {
	obj   *CachedObject
	store *Cache
	key   []byte

	mu       sync.Mutex
	queue    []*sinkSource
	current  *sinkSource
	draining bool
	finished bool
	stopped  bool
	expiry   *time.Timer
}

// sinkSource is a queued body, or bytes written after one, and is closed at
// most once.
type sinkSource struct {
	reader    io.Reader
	data      []byte
	closeOnce sync.Once
}

func (src *sinkSource) close() {
	src.closeOnce.Do(func() { closeReader(src.reader) })
}

// cacheFillWindow bounds how long a closed insert may keep copying, like
// production's session I/O deadline.
var cacheFillWindow = time.Hour

func (s *cacheBodySink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.draining || s.stopped {
		return s.writeLocked(p)
	}
	if last := len(s.queue) - 1; last >= 0 && s.queue[last].reader == nil {
		s.queue[last].data = append(s.queue[last].data, p...)
	} else {
		s.queue = append(s.queue, &sinkSource{data: bytes.Clone(p)})
	}
	return len(p), nil
}

// writeLocked stores p unless the insert was abandoned.
// The caller holds the lock.
func (s *cacheBodySink) writeLocked(p []byte) (int, error) {
	if s.stopped {
		return 0, io.ErrClosedPipe
	}
	return s.obj.WriteBody(p)
}

// Writes to the cache never wait.
func (s *cacheBodySink) readyChannel() <-chan struct{} {
	return closedStreamingReadyChannel()
}

// Append queues src and closes it once it was copied or dropped.
func (s *cacheBodySink) Append(src io.Reader) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	queued := &sinkSource{reader: src}
	if s.stopped {
		queued.close()
		return io.ErrClosedPipe
	}
	s.queue = append(s.queue, queued)
	if !s.draining {
		s.draining = true
		go s.drain()
	}
	return nil
}

func (s *cacheBodySink) drain() {
	var buf []byte
	for {
		s.mu.Lock()
		if s.stopped || len(s.queue) == 0 {
			s.draining = false
			s.current = nil
			if s.finished && !s.stopped {
				s.obj.FinishWrite()
			}
			if s.expiry != nil {
				s.expiry.Stop()
			}
			s.mu.Unlock()
			return
		}
		src := s.queue[0]
		s.queue[0] = nil
		s.queue = s.queue[1:]
		s.current = src
		s.mu.Unlock()

		var err error
		if src.reader == nil {
			_, err = sinkObjectWriter{s}.Write(src.data)
		} else {
			if buf == nil {
				buf = make([]byte, 32*1024)
			}
			_, err = io.CopyBuffer(sinkObjectWriter{s}, src.reader, buf)
			src.close()
		}
		if err != nil {
			// A truncated or failed source must not be stored as complete.
			_ = s.Abandon()
			return
		}
	}
}

// Close completes the object once the queue is drained, and gives up on it
// if that takes longer than cacheFillWindow.
func (s *cacheBodySink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		return nil
	}
	s.finished = true
	if !s.draining {
		s.obj.FinishWrite()
		return nil
	}
	s.expiry = time.AfterFunc(cacheFillWindow, s.expire)
	return nil
}

func (s *cacheBodySink) expire() {
	s.mu.Lock()
	stalled := s.draining && !s.stopped
	s.mu.Unlock()
	if stalled {
		_ = s.Abandon()
	}
}

// Abandon discards the object and fails its readers.
func (s *cacheBodySink) Abandon() error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	if s.expiry != nil {
		s.expiry.Stop()
	}
	dropped, current := s.queue, s.current
	s.queue = nil
	s.mu.Unlock()

	for _, src := range dropped {
		src.close()
	}
	if current != nil {
		// Closing the source unblocks a copy waiting for more data.
		current.close()
	}
	s.store.Discard(s.key, s.obj)
	return nil
}

// sinkObjectWriter writes copied bytes to the object, unless the insert was
// abandoned meanwhile.
type sinkObjectWriter struct {
	sink *cacheBodySink
}

func (w sinkObjectWriter) Write(p []byte) (int, error) {
	w.sink.mu.Lock()
	defer w.sink.mu.Unlock()

	return w.sink.writeLocked(p)
}

func (i *Instance) newCacheInsertBody(obj *CachedObject, key []byte) int {
	id, _ := i.bodies.NewSink(&cacheBodySink{obj: obj, store: i.cache, key: key})
	return id
}

// cacheBodyReader implements io.Reader for reading body data from a cached object.
// It supports streaming reads with blocking behavior while the cache write is in progress.
// A bounded reader stops at end and fails if the object is shorter than that.
// A reader with wholeIfShort serves the whole object instead when the object
// ends before the range starts.
type cacheBodyReader struct {
	cache        *CachedObject
	offset       int64
	end          int64
	bounded      bool
	wholeIfShort bool
}

func (r *cacheBodyReader) Read(p []byte) (int, error) {
	if r.bounded {
		remaining := r.end - r.offset
		if remaining <= 0 {
			if r.cache.writeFailed() {
				return 0, io.ErrUnexpectedEOF
			}
			return 0, io.EOF
		}
		if int64(len(p)) > remaining {
			p = p[:remaining]
		}
	}
	n, err := r.cache.ReadBody(p, r.offset)
	if n == 0 && err == io.EOF && r.wholeIfShort {
		*r = cacheBodyReader{cache: r.cache}
		n, err = r.cache.ReadBody(p, 0)
	}
	if n > 0 {
		r.wholeIfShort = false
	}
	r.offset += int64(n)
	if err == io.EOF && r.bounded && r.offset < r.end {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

// readCacheReplaceOptions reads cache replace options from guest memory.
// The strategy defaults to immediate.
func (i *Instance) readCacheReplaceOptions(mask uint32, optionsPtr int32) (*CacheReplaceOptions, int32) {
	if !i.memory.validRange(int64(optionsPtr), cacheReplaceOptionsSize) {
		return nil, XqdErrInvalidArgument
	}
	if mask&CacheReplaceOptionsMaskService != 0 {
		// Replacing on behalf of another service needs a privileged session.
		return nil, XqdErrUnsupported
	}

	opts := &CacheReplaceOptions{ReplaceStrategy: CacheReplaceImmediate}

	if mask&CacheReplaceOptionsMaskRequestHeaders != 0 {
		reqHandle := i.memory.Uint32(int64(optionsPtr))
		if reqHandle != uint32(HandleInvalid) {
			req := i.requests.Get(int(reqHandle))
			if req == nil {
				return nil, XqdErrInvalidHandle
			}
			opts.RequestHeaders = serializeRequestHeader(req.Request)
		}
	}

	if mask&CacheReplaceOptionsMaskReplaceStrategy != 0 {
		switch strategy := CacheReplaceStrategy(i.memory.Uint32(int64(optionsPtr + 4))); strategy {
		case CacheReplaceImmediate, CacheReplaceImmediateForceMiss, CacheReplaceWait:
			opts.ReplaceStrategy = strategy
		default:
			return nil, XqdErrInvalidArgument
		}
	}

	opts.AlwaysUseRequestedRange = mask&CacheReplaceOptionsMaskAlwaysUseRequestedRange != 0

	return opts, XqdStatusOK
}

// cacheReplaceExisting returns what a replace found under its key, or
// XqdErrNone when it found nothing.
func (i *Instance) cacheReplaceExisting(replace_handle int32) (*CacheEntry, int32) {
	handle := i.cacheReplaceHandles.Get(int(replace_handle))
	if handle == nil {
		return nil, XqdErrInvalidHandle
	}
	if handle.Replace.Existing.Object == nil {
		return nil, XqdErrNone
	}
	return &handle.Replace.Existing, XqdStatusOK
}

// xqd_cache_replace begins a replace operation and returns a replace handle
func (i *Instance) xqd_cache_replace(
	cache_key int32,
	cache_key_len int32,
	options_mask uint32,
	options int32,
	replace_handle_out int32,
) int32 {
	i.abilog.Println("xqd_cache_replace")

	// Nothing is registered until every guest pointer has been checked, so a
	// hostile call cannot leave a replace pending with no handle to abandon it.
	if cache_key_len < 0 || !i.memory.validRange(int64(cache_key), uint64(cache_key_len)) || !i.memory.validRange(int64(replace_handle_out), 4) {
		return XqdErrInvalidArgument
	}

	key := make([]byte, cache_key_len)
	_, _ = i.memory.ReadAt(key, int64(cache_key))

	replaceOpts, status := i.readCacheReplaceOptions(options_mask, options)
	if status != XqdStatusOK {
		return status
	}

	i.deepBumpCacheLookup()
	replace := i.cache.Replace(key, replaceOpts, i)
	i.deepBumpCacheOutcome(replace.Existing.State)

	handleID := i.cacheReplaceHandles.New(replace)
	i.memory.WriteUint32(replace_handle_out, uint32(handleID))

	return XqdStatusOK
}

// xqd_cache_replace_insert provides the replacement object and consumes the replace handle
func (i *Instance) xqd_cache_replace_insert(
	replace_handle int32,
	options_mask uint32,
	options int32,
	body_handle_out int32,
) int32 {
	i.abilog.Println("xqd_cache_replace_insert")

	if i.cacheReplaceHandles.Get(int(replace_handle)) == nil {
		return XqdErrInvalidHandle
	}
	if !i.memory.validRange(int64(body_handle_out), 4) {
		return XqdErrInvalidArgument
	}

	// Options are read before the handle is consumed, so a bad options pointer
	// cannot strand the pending replace.
	writeOpts, status := i.readCacheWriteOptions(options_mask, options)
	if status != XqdStatusOK {
		return status
	}
	handle := i.cacheReplaceHandles.Take(int(replace_handle))

	i.deepBumpCacheInsert()
	obj := i.cache.ReplaceInsert(handle.Replace, writeOpts)

	bodyID := i.newCacheInsertBody(obj, handle.Replace.Key)
	i.memory.WriteUint32(body_handle_out, uint32(bodyID))

	return XqdStatusOK
}

// xqd_cache_replace_get_age_ns gets the age of the existing object during replace
func (i *Instance) xqd_cache_replace_get_age_ns(
	replace_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("xqd_cache_replace_get_age_ns")

	if !i.memory.validRange(int64(duration_out), 8) {
		return XqdErrInvalidArgument
	}

	existing, status := i.cacheReplaceExisting(replace_handle)
	if status != XqdStatusOK {
		return status
	}

	i.memory.WriteUint64(duration_out, existing.Metadata.AgeNs)

	return XqdStatusOK
}

// xqd_cache_replace_get_body gets the body of the existing object during replace
func (i *Instance) xqd_cache_replace_get_body(
	replace_handle int32,
	options_mask uint32,
	options int32,
	body_handle_out int32,
) int32 {
	i.abilog.Println("xqd_cache_replace_get_body")

	if !i.memory.validRange(int64(body_handle_out), 4) || !i.memory.validRange(int64(options), cacheGetBodyOptionsSize) {
		return XqdErrInvalidArgument
	}

	existing, status := i.cacheReplaceExisting(replace_handle)
	if status != XqdStatusOK {
		return status
	}

	handle := i.cacheReplaceHandles.Get(int(replace_handle))
	if handle.readerBody != 0 && i.bodies.Get(handle.readerBody) != nil {
		// The previous reader has to be closed first.
		return XqdErrInvalidHandle
	}

	bodyID, status := i.newCacheObjectBody(existing, options_mask, options, handle.Replace.Options.AlwaysUseRequestedRange)
	if status != XqdStatusOK {
		return status
	}
	handle.readerBody = bodyID
	i.memory.WriteUint32(body_handle_out, uint32(bodyID))

	return XqdStatusOK
}

// xqd_cache_replace_get_hits gets the hit count of the existing object during replace
func (i *Instance) xqd_cache_replace_get_hits(
	replace_handle int32,
	hits_out int32,
) int32 {
	i.abilog.Println("xqd_cache_replace_get_hits")

	if !i.memory.validRange(int64(hits_out), 8) {
		return XqdErrInvalidArgument
	}

	existing, status := i.cacheReplaceExisting(replace_handle)
	if status != XqdStatusOK {
		return status
	}

	i.memory.WriteUint64(hits_out, existing.Metadata.Hits)

	return XqdStatusOK
}

// xqd_cache_replace_get_length gets the length of the existing object during replace
func (i *Instance) xqd_cache_replace_get_length(
	replace_handle int32,
	length_out int32,
) int32 {
	i.abilog.Println("xqd_cache_replace_get_length")

	if !i.memory.validRange(int64(length_out), 8) {
		return XqdErrInvalidArgument
	}

	existing, status := i.cacheReplaceExisting(replace_handle)
	if status != XqdStatusOK {
		return status
	}
	return i.writeKnownLength(&existing.Metadata, length_out)
}

// xqd_cache_replace_get_max_age_ns gets the max age of the existing object during replace
func (i *Instance) xqd_cache_replace_get_max_age_ns(
	replace_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("xqd_cache_replace_get_max_age_ns")

	if !i.memory.validRange(int64(duration_out), 8) {
		return XqdErrInvalidArgument
	}

	existing, status := i.cacheReplaceExisting(replace_handle)
	if status != XqdStatusOK {
		return status
	}

	i.memory.WriteUint64(duration_out, existing.Metadata.MaxAgeNs)

	return XqdStatusOK
}

// xqd_cache_replace_get_stale_while_revalidate_ns gets the stale-while-revalidate
// period of the existing object during replace.
func (i *Instance) xqd_cache_replace_get_stale_while_revalidate_ns(
	replace_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("xqd_cache_replace_get_stale_while_revalidate_ns")

	if !i.memory.validRange(int64(duration_out), 8) {
		return XqdErrInvalidArgument
	}

	existing, status := i.cacheReplaceExisting(replace_handle)
	if status != XqdStatusOK {
		return status
	}

	i.memory.WriteUint64(duration_out, existing.Metadata.StaleWhileRevalidateNs)

	return XqdStatusOK
}

// xqd_cache_replace_get_state gets the lookup state of the existing object during replace.
// An empty state, not an error, means nothing was found; SDKs rely on that.
func (i *Instance) xqd_cache_replace_get_state(
	replace_handle int32,
	state_out int32,
) int32 {
	i.abilog.Println("xqd_cache_replace_get_state")

	if !i.memory.validRange(int64(state_out), 4) {
		return XqdErrInvalidArgument
	}

	handle := i.cacheReplaceHandles.Get(int(replace_handle))
	if handle == nil {
		return XqdErrInvalidHandle
	}

	state := handle.Replace.Existing.State
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

	i.memory.WriteUint32(state_out, flags)

	return XqdStatusOK
}

// xqd_cache_replace_get_user_metadata gets the user metadata of the existing object during replace.
// The required size is always reported, so a guest with a short buffer can retry.
func (i *Instance) xqd_cache_replace_get_user_metadata(
	replace_handle int32,
	user_metadata_out_ptr int32,
	user_metadata_out_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Println("xqd_cache_replace_get_user_metadata")

	if user_metadata_out_len < 0 || !i.memory.validRange(int64(nwritten_out), 4) {
		return XqdErrInvalidArgument
	}

	existing, status := i.cacheReplaceExisting(replace_handle)
	if status != XqdStatusOK {
		return status
	}

	// The required size is reported before the buffer is looked at, so a
	// guest can probe with an empty buffer.
	metadata := existing.Metadata.UserMetadata
	i.memory.WriteUint32(nwritten_out, uint32(len(metadata)))
	if len(metadata) > int(user_metadata_out_len) {
		return XqdErrBufferLength
	}
	if !i.memory.validRange(int64(user_metadata_out_ptr), uint64(len(metadata))) {
		return XqdErrInvalidArgument
	}

	_, _ = i.memory.WriteAt(metadata, int64(user_metadata_out_ptr))

	return XqdStatusOK
}

// cacheSuffixReader serves the last count bytes of an object whose size is
// not known yet, which means waiting for the writer to finish first.
type cacheSuffixReader struct {
	cache *CachedObject
	count uint64
	inner io.Reader
}

func (r *cacheSuffixReader) Read(p []byte) (int, error) {
	if r.inner == nil {
		size, failed := r.cache.waitForWriteComplete()
		if failed {
			return 0, io.ErrUnexpectedEOF
		}
		r.inner = cacheRangeReader(r.cache, uint64(size), true, byteRange{last: r.count, hasLast: true}, false)
	}
	return r.inner.Read(p)
}

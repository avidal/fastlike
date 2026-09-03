package fastlike

import (
	"bytes"
	"io"
	"math"
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

	// Create a transaction to wrap the entry (for handle consistency)
	tx := &CacheTransaction{
		Key:     key,
		Entry:   entry,
		Options: lookupOpts,
		ready:   make(chan struct{}),
	}
	close(tx.ready) // Already complete

	handleID := i.cacheHandles.New(tx)
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

	// Create a body handle that writes to the cache object
	bodyID, body := i.bodies.NewBuffer()

	// Wrap to write to cache object
	origWriter := body.writer
	body.writer = &cacheBodyWriter{
		cache:        obj,
		originalBody: origWriter,
	}

	// Set a closer that marks the cache write as complete
	body.closer = &cacheOnlyCloser{cache: obj, store: i.cache, key: key}

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

	obj := i.cache.Insert(handle.Transaction.Key, writeOpts)

	// Create a body handle that writes to the cache object
	bodyID, body := i.bodies.NewBuffer()
	body.writer = &cacheBodyWriter{
		cache:        obj,
		originalBody: body.buf,
	}

	// Set a closer that marks the cache write as complete
	body.closer = &cacheOnlyCloser{cache: obj, store: i.cache, key: handle.Transaction.Key}

	i.memory.WriteUint32(body_handle_out, uint32(bodyID))

	// Complete the transaction
	i.cache.CompleteTransaction(handle.Transaction)

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

	obj := i.cache.Insert(handle.Transaction.Key, writeOpts)

	// Create a pipe to enable simultaneous write and read without deadlock
	pipeReader, pipeWriter := io.Pipe()

	// Create a cache body writer that writes to the cache object
	cacheWriter := &cacheBodyWriter{
		cache:        obj,
		originalBody: nil,
	}

	// Use MultiWriter to tee data to both the pipe (for immediate reading) and the cache (for storage)
	multiWriter := &cacheTeeWriter{io.MultiWriter(pipeWriter, cacheWriter)}

	// Create a write body handle that writes to both the pipe and cache
	writeBodyID, writeBody := i.bodies.NewBuffer()
	writeBody.writer = multiWriter
	writeBody.closer = &pipeAndCacheCloser{
		pipeWriter: pipeWriter,
		cache:      obj,
		store:      i.cache,
		key:        handle.Transaction.Key,
	}

	// Create a new transaction/handle for reading back from the pipe
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
	// Store the pipe reader in the cache handle so get_body can use it
	readCacheHandle := i.cacheHandles.Get(readHandleID)
	readCacheHandle.StreamingPipeReader = pipeReader

	i.memory.WriteUint32(body_handle_out, uint32(writeBodyID))
	i.memory.WriteUint32(cache_handle_out, uint32(readHandleID))

	// Complete the original transaction
	i.cache.CompleteTransaction(handle.Transaction)

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

	err := i.cache.TransactionUpdate(handle.Transaction, writeOpts)
	if err != nil {
		return XqdError
	}

	// Complete the transaction
	i.cache.CompleteTransaction(handle.Transaction)

	return XqdStatusOK
}

// xqd_cache_transaction_cancel cancels a cache transaction
func (i *Instance) xqd_cache_transaction_cancel(cache_handle int32) int32 {
	i.abilog.Println("xqd_cache_transaction_cancel")

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
		_ = i.cache.TransactionCancel(handle.Transaction)
	}

	return XqdStatusOK
}

// xqd_cache_get_state gets the cache lookup state flags
func (i *Instance) xqd_cache_get_state(
	cache_handle int32,
	cache_lookup_state_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_state")

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

// xqd_cache_get_user_metadata gets user metadata from cached object
func (i *Instance) xqd_cache_get_user_metadata(
	cache_handle int32,
	user_metadata_out_ptr int32,
	user_metadata_out_len int32,
	nwritten_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_user_metadata")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil || handle.Transaction.Entry.Object == nil {
		return XqdErrInvalidHandle
	}

	metadata := handle.Transaction.Entry.Object.UserMetadata
	if metadata == nil {
		metadata = []byte{}
	}

	if len(metadata) > int(user_metadata_out_len) {
		return XqdErrBufferLength
	}

	_, _ = i.memory.WriteAt(metadata, int64(user_metadata_out_ptr))
	i.memory.WriteUint32(nwritten_out, uint32(len(metadata)))

	return XqdStatusOK
}

// xqd_cache_get_body gets the body of a cached object with optional range
func (i *Instance) xqd_cache_get_body(
	cache_handle int32,
	options_mask uint32,
	options int32,
	body_handle_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_body")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil || handle.Transaction.Entry.Object == nil {
		return XqdErrInvalidHandle
	}

	obj := handle.Transaction.Entry.Object

	hasRange := options_mask&(CacheGetBodyOptionsMaskFrom|CacheGetBodyOptionsMaskTo) != 0
	if !hasRange && handle.StreamingPipeReader != nil {
		// insert_and_stream_back handles read through their pipe so the guest
		// cannot deadlock against its own write.
		bodyID, body := i.bodies.NewBuffer()
		body.reader = handle.StreamingPipeReader
		i.memory.WriteUint32(body_handle_out, uint32(bodyID))
		return XqdStatusOK
	}

	alwaysUseRequestedRange := handle.Transaction.Options != nil && handle.Transaction.Options.AlwaysUseRequestedRange
	bodyID, status := i.newCacheObjectBody(obj, options_mask, options, alwaysUseRequestedRange)
	if status != XqdStatusOK {
		return status
	}
	i.memory.WriteUint32(body_handle_out, uint32(bodyID))

	return XqdStatusOK
}

// newCacheObjectBody creates a body handle reading obj, restricted to the
// optional inclusive from/to range.
// A range outside a known size falls back to the whole body.
// With an unknown size the range is honored only when the guest insists, and
// the read then fails if the object ends early.
func (i *Instance) newCacheObjectBody(obj *CachedObject, options_mask uint32, options int32, alwaysUseRequestedRange bool) (int, int32) {
	hasFrom := options_mask&CacheGetBodyOptionsMaskFrom != 0
	hasTo := options_mask&CacheGetBodyOptionsMaskTo != 0

	var from, to uint64
	if hasFrom {
		from = i.memory.ReadUint64(options)
	}
	if hasTo {
		to = i.memory.ReadUint64(options + 8)
	}
	if hasFrom && hasTo && to < from {
		// An end before the start can never be satisfied, whatever the size.
		return 0, XqdErrInvalidArgument
	}

	bodyID, body := i.bodies.NewBuffer()
	reader := &cacheBodyReader{cache: obj}
	body.reader = reader
	if !hasFrom && !hasTo {
		return bodyID, XqdStatusOK
	}

	size, sizeKnown := obj.KnownLength()
	switch {
	case sizeKnown && hasTo && !hasFrom:
		// A lone end bound asks for the last `to` bytes.
		reader.offset = size - int64(min(to, uint64(size)))
		reader.end = size
		reader.bounded = true
	case sizeKnown:
		if !hasTo {
			to = uint64(size) - 1
		}
		if from >= uint64(size) || to >= uint64(size) {
			return bodyID, XqdStatusOK
		}
		reader.offset = int64(from)
		reader.end = int64(to) + 1
		reader.bounded = true
	case alwaysUseRequestedRange && hasTo && !hasFrom:
		body.reader = &cacheSuffixReader{cache: obj, count: to}
	case alwaysUseRequestedRange:
		// Offsets past what a body can hold simply never get satisfied.
		reader.offset = int64(min(from, math.MaxInt64))
		reader.mustReach = reader.offset > 0
		if hasTo {
			reader.end = int64(min(to, math.MaxInt64-1)) + 1
			reader.bounded = true
		}
	}
	return bodyID, XqdStatusOK
}

// xqd_cache_get_length gets the length of a cached object
func (i *Instance) xqd_cache_get_length(
	cache_handle int32,
	length_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_length")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil || handle.Transaction.Entry.Object == nil {
		return XqdErrInvalidHandle
	}

	obj := handle.Transaction.Entry.Object
	if obj.Length != nil {
		i.memory.WriteUint64(length_out, *obj.Length)
		return XqdStatusOK
	}

	// If length is not known, return NONE
	return XqdErrNone
}

// xqd_cache_get_max_age_ns gets the max age in nanoseconds
func (i *Instance) xqd_cache_get_max_age_ns(
	cache_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_max_age_ns")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil || handle.Transaction.Entry.Object == nil {
		return XqdErrInvalidHandle
	}

	obj := handle.Transaction.Entry.Object
	i.memory.WriteUint64(duration_out, obj.MaxAgeNs)

	return XqdStatusOK
}

// xqd_cache_get_stale_while_revalidate_ns gets the stale-while-revalidate duration
func (i *Instance) xqd_cache_get_stale_while_revalidate_ns(
	cache_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_stale_while_revalidate_ns")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil || handle.Transaction.Entry.Object == nil {
		return XqdErrInvalidHandle
	}

	obj := handle.Transaction.Entry.Object
	if obj.StaleWhileRevalidateNs > 0 {
		i.memory.WriteUint64(duration_out, obj.StaleWhileRevalidateNs)
		return XqdStatusOK
	}

	return XqdErrNone
}

// xqd_cache_get_age_ns gets the age of the cached object in nanoseconds
func (i *Instance) xqd_cache_get_age_ns(
	cache_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_age_ns")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil || handle.Transaction.Entry.Object == nil {
		return XqdErrInvalidHandle
	}

	obj := handle.Transaction.Entry.Object
	age := obj.GetAge()
	i.memory.WriteUint64(duration_out, age)

	return XqdStatusOK
}

// xqd_cache_get_hits gets the hit count for a cached object
func (i *Instance) xqd_cache_get_hits(
	cache_handle int32,
	hits_out int32,
) int32 {
	i.abilog.Println("xqd_cache_get_hits")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil || handle.Transaction.Entry == nil || handle.Transaction.Entry.Object == nil {
		return XqdErrInvalidHandle
	}

	obj := handle.Transaction.Entry.Object
	i.memory.WriteUint64(hits_out, obj.HitCount.Load())

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
				buf := &bytes.Buffer{}
				_ = req.Header.Write(buf)
				opts.RequestHeaders = buf.Bytes()
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
				buf := &bytes.Buffer{}
				_ = req.Header.Write(buf)
				opts.RequestHeaders = buf.Bytes()
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
	ptr := int32(i.memory.Uint32(int64(fieldPtr)))
	length := int32(i.memory.Uint32(int64(fieldPtr + 4)))
	if length < 0 || !i.memory.validRange(int64(ptr), uint64(length)) {
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

// cacheBodyWriter implements io.Writer for writing body data to a cached object.
// It writes to both the cache object and optionally to an original buffer for tracking.
type cacheBodyWriter struct {
	cache        *CachedObject
	originalBody io.Writer
}

func (w *cacheBodyWriter) cacheBacked() {}

func (w *cacheBodyWriter) Write(p []byte) (int, error) {
	n, err := w.cache.WriteBody(p)
	if err != nil {
		return n, err
	}
	// Also write to original buffer for tracking
	if w.originalBody != nil {
		_, _ = w.originalBody.Write(p)
	}
	return n, nil
}

// cacheBodyReader implements io.Reader for reading body data from a cached object.
// It supports streaming reads with blocking behavior while the cache write is in progress.
// A bounded reader stops at end and fails if the object is shorter than that.
// A reader that must reach its start fails if the object ends before it.
type cacheBodyReader struct {
	cache     *CachedObject
	offset    int64
	end       int64
	bounded   bool
	mustReach bool
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
	if n > 0 {
		r.mustReach = false
	}
	r.offset += int64(n)
	if err == io.EOF && ((r.bounded && r.offset < r.end) || r.mustReach) {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

// pipeAndCacheCloser implements io.Closer for cache insert_and_stream_back operations.
// It closes both the pipe writer (sending EOF to the reader) and marks the cache write as complete.
// Abandoning the body instead fails the pipe and discards the incomplete object.
type pipeAndCacheCloser struct {
	pipeWriter *io.PipeWriter
	cache      *CachedObject
	store      *Cache
	key        []byte
}

func (c *pipeAndCacheCloser) Close() error {
	// Close the pipe writer first (this will EOF the pipe reader)
	err := c.pipeWriter.Close()
	// Mark the cache write as complete
	c.cache.FinishWrite()
	return err
}

func (c *pipeAndCacheCloser) Abandon() error {
	err := c.pipeWriter.CloseWithError(io.ErrUnexpectedEOF)
	c.store.Discard(c.key, c.cache)
	return err
}

// cacheOnlyCloser implements io.Closer for cache insert operations.
// It marks the cache write as complete when closed.
// Abandoning the body instead discards the incomplete object, so a partial
// write never gets served as a finished one.
type cacheOnlyCloser struct {
	cache *CachedObject
	store *Cache
	key   []byte
}

func (c *cacheOnlyCloser) Close() error {
	// Mark the cache write as complete
	c.cache.FinishWrite()
	return nil
}

func (c *cacheOnlyCloser) Abandon() error {
	c.store.Discard(c.key, c.cache)
	return nil
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
			buf := &bytes.Buffer{}
			_ = req.Header.Write(buf)
			opts.RequestHeaders = buf.Bytes()
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

// cacheReplaceExisting returns the object a replace handle is replacing, or
// XqdErrNone when there was none.
func (i *Instance) cacheReplaceExisting(replace_handle int32) (*CachedObject, int32) {
	handle := i.cacheReplaceHandles.Get(int(replace_handle))
	if handle == nil {
		return nil, XqdErrInvalidHandle
	}
	if handle.Replace.Existing == nil {
		return nil, XqdErrNone
	}
	return handle.Replace.Existing, XqdStatusOK
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
	i.deepBumpCacheOutcome(replace.State())

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

	bodyID, body := i.bodies.NewBuffer()
	body.writer = &cacheBodyWriter{
		cache:        obj,
		originalBody: body.buf,
	}
	body.closer = &cacheOnlyCloser{cache: obj, store: i.cache, key: handle.Replace.Key}

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

	obj, status := i.cacheReplaceExisting(replace_handle)
	if status != XqdStatusOK {
		return status
	}

	i.memory.WriteUint64(duration_out, obj.GetAge())

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

	obj, status := i.cacheReplaceExisting(replace_handle)
	if status != XqdStatusOK {
		return status
	}

	handle := i.cacheReplaceHandles.Get(int(replace_handle))
	if handle.readerBody != 0 && i.bodies.Get(handle.readerBody) != nil {
		// The previous reader has to be closed first.
		return XqdErrInvalidHandle
	}

	bodyID, status := i.newCacheObjectBody(obj, options_mask, options, handle.Replace.Options.AlwaysUseRequestedRange)
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

	obj, status := i.cacheReplaceExisting(replace_handle)
	if status != XqdStatusOK {
		return status
	}

	i.memory.WriteUint64(hits_out, obj.HitCount.Load())

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

	obj, status := i.cacheReplaceExisting(replace_handle)
	if status != XqdStatusOK {
		return status
	}
	if obj.Length == nil {
		return XqdErrNone
	}

	i.memory.WriteUint64(length_out, *obj.Length)

	return XqdStatusOK
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

	obj, status := i.cacheReplaceExisting(replace_handle)
	if status != XqdStatusOK {
		return status
	}

	i.memory.WriteUint64(duration_out, obj.MaxAgeNs)

	return XqdStatusOK
}

// xqd_cache_replace_get_stale_while_revalidate_ns gets the stale-while-revalidate
// period of the existing object during replace.
// Unlike the lookup accessor, production reports a zero period rather than none.
func (i *Instance) xqd_cache_replace_get_stale_while_revalidate_ns(
	replace_handle int32,
	duration_out int32,
) int32 {
	i.abilog.Println("xqd_cache_replace_get_stale_while_revalidate_ns")

	if !i.memory.validRange(int64(duration_out), 8) {
		return XqdErrInvalidArgument
	}

	obj, status := i.cacheReplaceExisting(replace_handle)
	if status != XqdStatusOK {
		return status
	}

	i.memory.WriteUint64(duration_out, obj.StaleWhileRevalidateNs)

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

	state := handle.Replace.State()
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

	obj, status := i.cacheReplaceExisting(replace_handle)
	if status != XqdStatusOK {
		return status
	}

	// The required size is reported before the buffer is looked at, so a
	// guest can probe with an empty buffer.
	metadata := obj.UserMetadata
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
	inner *cacheBodyReader
}

func (r *cacheSuffixReader) Read(p []byte) (int, error) {
	if r.inner == nil {
		size, failed := r.cache.waitForWriteComplete()
		if failed {
			return 0, io.ErrUnexpectedEOF
		}
		r.inner = &cacheBodyReader{
			cache:   r.cache,
			offset:  size - int64(min(r.count, uint64(size))),
			end:     size,
			bounded: true,
		}
	}
	return r.inner.Read(p)
}

// cacheTeeWriter wraps the pipe and cache writers of a stream back insert so
// a redirect keeps both fed.
type cacheTeeWriter struct {
	io.Writer
}

func (w *cacheTeeWriter) cacheBacked() {}

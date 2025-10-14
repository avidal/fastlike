package fastlike

import (
	"bytes"
	"io"
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

	keyBuf := make([]byte, cache_key_len)
	i.memory.ReadAt(keyBuf, int64(cache_key))
	key := keyBuf

	lookupOpts := &CacheLookupOptions{}
	if options_mask&CacheLookupOptionsMaskRequestHeaders != 0 {
		// Read request headers handle from options struct
		reqHandle := i.memory.Uint32(int64(options + 0))
		if reqHandle != uint32(HandleInvalid) {
			req := i.requests.Get(int(reqHandle))
			if req != nil {
				// Serialize request headers
				buf := &bytes.Buffer{}
				_ = req.Header.Write(buf)
				lookupOpts.RequestHeaders = buf.Bytes()
			}
		}
	}
	if options_mask&CacheLookupOptionsMaskAlwaysUseRequestedRange != 0 {
		lookupOpts.AlwaysUseRequestedRange = true
	}

	entry := i.cache.Lookup(key, lookupOpts)

	// Create a transaction to wrap the entry (for handle consistency)
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

// xqd_cache_insert performs a non-transactional cache insert
func (i *Instance) xqd_cache_insert(
	cache_key int32,
	cache_key_len int32,
	options_mask uint32,
	options int32,
	body_handle_out int32,
) int32 {
	i.abilog.Println("xqd_cache_insert")

	keyBuf := make([]byte, cache_key_len)
	i.memory.ReadAt(keyBuf, int64(cache_key))
	key := keyBuf
	writeOpts := i.readCacheWriteOptions(options_mask, options)

	obj := i.cache.Insert(key, writeOpts)

	// Create a body handle that writes to the cache object
	bodyID, body := i.bodies.NewBuffer()

	// Wrap to write to cache object
	origWriter := body.writer
	body.writer = &cacheBodyWriter{
		cache:        obj,
		originalBody: origWriter,
	}

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

	keyBuf := make([]byte, cache_key_len)
	i.memory.ReadAt(keyBuf, int64(cache_key))
	key := keyBuf

	lookupOpts := &CacheLookupOptions{}
	if options_mask&CacheLookupOptionsMaskRequestHeaders != 0 {
		reqHandle := i.memory.Uint32(int64(options + 0))
		if reqHandle != uint32(HandleInvalid) {
			req := i.requests.Get(int(reqHandle))
			if req != nil {
				buf := &bytes.Buffer{}
				_ = req.Header.Write(buf)
				lookupOpts.RequestHeaders = buf.Bytes()
			}
		}
	}
	if options_mask&CacheLookupOptionsMaskAlwaysUseRequestedRange != 0 {
		lookupOpts.AlwaysUseRequestedRange = true
	}

	tx := i.cache.TransactionLookup(key, lookupOpts)

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

	keyBuf := make([]byte, cache_key_len)
	i.memory.ReadAt(keyBuf, int64(cache_key))
	key := keyBuf

	lookupOpts := &CacheLookupOptions{}
	if options_mask&CacheLookupOptionsMaskRequestHeaders != 0 {
		reqHandle := i.memory.Uint32(int64(options + 0))
		if reqHandle != uint32(HandleInvalid) {
			req := i.requests.Get(int(reqHandle))
			if req != nil {
				buf := &bytes.Buffer{}
				_ = req.Header.Write(buf)
				lookupOpts.RequestHeaders = buf.Bytes()
			}
		}
	}
	if options_mask&CacheLookupOptionsMaskAlwaysUseRequestedRange != 0 {
		lookupOpts.AlwaysUseRequestedRange = true
	}

	// Start async lookup (in our case, it's immediate but we return a busy handle)
	tx := i.cache.TransactionLookup(key, lookupOpts)

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

	busyHandle := i.cacheBusyHandles.Get(int(busy_handle))
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

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil {
		return XqdErrInvalidHandle
	}

	writeOpts := i.readCacheWriteOptions(options_mask, options)

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

// xqd_cache_transaction_insert_and_stream_back inserts and streams back simultaneously
func (i *Instance) xqd_cache_transaction_insert_and_stream_back(
	cache_handle int32,
	options_mask uint32,
	options int32,
	body_handle_out int32,
	cache_handle_out int32,
) int32 {
	i.abilog.Println("xqd_cache_transaction_insert_and_stream_back")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil || handle.Transaction == nil {
		return XqdErrInvalidHandle
	}

	writeOpts := i.readCacheWriteOptions(options_mask, options)

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

	writeOpts := i.readCacheWriteOptions(options_mask, options)

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

	busyHandle := i.cacheBusyHandles.Get(int(busy_handle))
	if busyHandle == nil {
		return XqdErrInvalidHandle
	}

	// Nothing to do - just validates the handle exists

	return XqdStatusOK
}

// xqd_cache_close closes a cache handle
func (i *Instance) xqd_cache_close(cache_handle int32) int32 {
	i.abilog.Println("xqd_cache_close")

	handle := i.cacheHandles.Get(int(cache_handle))
	if handle == nil {
		return XqdErrInvalidHandle
	}

	// Nothing to do - just validates the handle exists

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

	i.memory.WriteAt(metadata, int64(user_metadata_out_ptr))
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

	var fromOffset, toOffset int64
	hasFrom := options_mask&CacheGetBodyOptionsMaskFrom != 0
	hasTo := options_mask&CacheGetBodyOptionsMaskTo != 0

	if hasFrom {
		fromOffset = int64(i.memory.ReadUint64(options + 0))
	}
	if hasTo {
		toOffset = int64(i.memory.ReadUint64(options + 8))
	}

	// Create a body handle for reading from cache
	bodyID, body := i.bodies.NewBuffer()

	if hasFrom || hasTo {
		// Range read
		if !hasFrom {
			fromOffset = 0
		}
		if !hasTo {
			toOffset = int64(obj.Body.Len())
		}

		// Validate range
		if fromOffset > toOffset {
			return XqdErrInvalidArgument
		}

		// Read range from cache
		data := obj.Body.Bytes()[fromOffset:toOffset]
		body.buf.Write(data)
		body.reader = bytes.NewReader(body.buf.Bytes())
	} else {
		// Full body read - use streaming reader
		body.reader = &cacheBodyReader{
			cache:  obj,
			offset: 0,
		}
	}

	i.memory.WriteUint32(body_handle_out, uint32(bodyID))

	return XqdStatusOK
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
	i.memory.WriteUint64(hits_out, obj.HitCount)

	return XqdStatusOK
}

// Helper functions

// readCacheWriteOptions reads cache write options from guest memory
func (i *Instance) readCacheWriteOptions(mask uint32, optionsPtr int32) *CacheWriteOptions {
	opts := &CacheWriteOptions{}

	// Read max_age_ns (always present)
	opts.MaxAgeNs = i.memory.ReadUint64(optionsPtr + 0)

	offset := int32(8) // Start after max_age_ns

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
		varyPtr := int32(i.memory.Uint32(int64(optionsPtr + offset)))
		varyLen := int32(i.memory.Uint32(int64(optionsPtr + offset + 4)))
		if varyLen > 0 {
			varyBuf := make([]byte, varyLen)
			i.memory.ReadAt(varyBuf, int64(varyPtr))
			opts.VaryRule = string(varyBuf)
		}
	}
	offset += 8

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
		keysPtr := int32(i.memory.Uint32(int64(optionsPtr + offset)))
		keysLen := int32(i.memory.Uint32(int64(optionsPtr + offset + 4)))
		if keysLen > 0 {
			keysBuf := make([]byte, keysLen)
			i.memory.ReadAt(keysBuf, int64(keysPtr))
			keysStr := string(keysBuf)
			// Split by spaces
			opts.SurrogateKeys = splitSurrogateKeys(keysStr)
		}
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
		mdPtr := int32(i.memory.Uint32(int64(optionsPtr + offset)))
		mdLen := int32(i.memory.Uint32(int64(optionsPtr + offset + 4)))
		if mdLen > 0 {
			mdBuf := make([]byte, mdLen)
			i.memory.ReadAt(mdBuf, int64(mdPtr))
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

	return opts
}

// splitSurrogateKeys splits a space-separated string of surrogate keys
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

// cacheBodyWriter writes to a cached object
type cacheBodyWriter struct {
	cache        *CachedObject
	originalBody io.Writer
}

func (w *cacheBodyWriter) Write(p []byte) (int, error) {
	n, err := w.cache.WriteBody(p)
	if err != nil {
		return n, err
	}
	// Also write to original buffer for tracking
	if w.originalBody != nil {
		w.originalBody.Write(p)
	}
	return n, nil
}

// cacheBodyReader reads from a cached object with streaming support
type cacheBodyReader struct {
	cache  *CachedObject
	offset int64
}

func (r *cacheBodyReader) Read(p []byte) (int, error) {
	n, err := r.cache.ReadBody(p, r.offset)
	r.offset += int64(n)
	return n, err
}

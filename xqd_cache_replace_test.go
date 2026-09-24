package fastlike

import (
	"context"
	"io"
	"log"
	"testing"
	"time"
)

// Guest memory layout shared by the replace tests.
const (
	replaceTestKeyPtr        = 0
	replaceTestWriteOptsPtr  = 256
	replaceTestReplaceOpts   = 512
	replaceTestBodyOptsPtr   = 640
	replaceTestMetadataPtr   = 768
	replaceTestMetadataOut   = 896
	replaceTestHandleOut     = 1024
	replaceTestValueOut      = 1032
	replaceTestNwrittenOut   = 1040
	replaceTestWriteMaxAge   = 8
	replaceTestWriteInitial  = 24
	replaceTestWriteSWR      = 32
	replaceTestWriteLength   = 48
	replaceTestWriteMetadata = 56
)

func newCacheReplaceTestInstance() *Instance {
	return &Instance{
		requests:            &RequestHandles{},
		bodies:              &BodyHandles{},
		cache:               NewCache(),
		cacheHandles:        &CacheHandles{},
		cacheBusyHandles:    &CacheBusyHandles{},
		cacheReplaceHandles: &CacheReplaceHandles{},
		memory:              &Memory{ByteMemory(make([]byte, 4096))},
		abilog:              log.New(io.Discard, "", 0),
	}
}

type replaceTestObject struct {
	content  string
	maxAge   time.Duration
	initial  time.Duration
	swr      time.Duration
	metadata string
	length   bool
	finish   bool
}

// writeCacheWriteOptions lays out a cache write options struct in guest memory
// and returns the mask describing which fields are set.
func writeCacheWriteOptions(i *Instance, obj replaceTestObject) uint32 {
	mask := uint32(0)
	i.memory.WriteUint64(replaceTestWriteOptsPtr, uint64(obj.maxAge.Nanoseconds()))
	if obj.initial > 0 {
		mask |= CacheWriteOptionsMaskInitialAgeNs
		i.memory.WriteUint64(replaceTestWriteOptsPtr+replaceTestWriteInitial, uint64(obj.initial.Nanoseconds()))
	}
	if obj.swr > 0 {
		mask |= CacheWriteOptionsMaskStaleWhileRevalidateNs
		i.memory.WriteUint64(replaceTestWriteOptsPtr+replaceTestWriteSWR, uint64(obj.swr.Nanoseconds()))
	}
	if obj.length {
		mask |= CacheWriteOptionsMaskLength
		i.memory.WriteUint64(replaceTestWriteOptsPtr+replaceTestWriteLength, uint64(len(obj.content)))
	}
	if obj.metadata != "" {
		mask |= CacheWriteOptionsMaskUserMetadata
		_, _ = i.memory.WriteAt([]byte(obj.metadata), replaceTestMetadataPtr)
		i.memory.WriteUint32(replaceTestWriteOptsPtr+replaceTestWriteMetadata, replaceTestMetadataPtr)
		i.memory.WriteUint32(replaceTestWriteOptsPtr+replaceTestWriteMetadata+4, uint32(len(obj.metadata)))
	}
	return mask
}

func writeCacheKey(i *Instance, key string) (int32, int32) {
	_, _ = i.memory.WriteAt([]byte(key), replaceTestKeyPtr)
	return replaceTestKeyPtr, int32(len(key))
}

// insertTestObject stores obj under key through the plain insert hostcall.
func insertTestObject(t *testing.T, i *Instance, key string, obj replaceTestObject) {
	t.Helper()
	keyPtr, keyLen := writeCacheKey(i, key)
	mask := writeCacheWriteOptions(i, obj)
	if status := i.xqd_cache_insert(keyPtr, keyLen, mask, replaceTestWriteOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("cache_insert status = %d, want %d", status, XqdStatusOK)
	}
	body := i.bodies.Get(int(i.memory.Uint32(replaceTestHandleOut)))
	if _, err := body.Write([]byte(obj.content)); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if obj.finish {
		if err := body.Close(); err != nil {
			t.Fatalf("close body: %v", err)
		}
	}
}

// writeReplaceStrategy lays out replace options without request headers.
func writeReplaceStrategy(i *Instance, strategy CacheReplaceStrategy) {
	i.memory.WriteUint32(replaceTestReplaceOpts, uint32(HandleInvalid))
	i.memory.WriteUint32(replaceTestReplaceOpts+4, uint32(strategy))
}

// beginReplace starts a replace for key with the given strategy and returns the replace handle.
func beginReplace(t *testing.T, i *Instance, key string, strategy CacheReplaceStrategy) int32 {
	t.Helper()
	keyPtr, keyLen := writeCacheKey(i, key)
	mask := CacheReplaceOptionsMaskReplaceStrategy
	writeReplaceStrategy(i, strategy)
	if status := i.xqd_cache_replace(keyPtr, keyLen, mask, replaceTestReplaceOpts, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("cache_replace status = %d, want %d", status, XqdStatusOK)
	}
	return int32(i.memory.Uint32(replaceTestHandleOut))
}

// executeReplace provides the replacement object through replace_insert.
func executeReplace(t *testing.T, i *Instance, handle int32, obj replaceTestObject) {
	t.Helper()
	mask := writeCacheWriteOptions(i, obj)
	if status := i.xqd_cache_replace_insert(handle, mask, replaceTestWriteOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("cache_replace_insert status = %d, want %d", status, XqdStatusOK)
	}
	body := i.bodies.Get(int(i.memory.Uint32(replaceTestHandleOut)))
	if _, err := body.Write([]byte(obj.content)); err != nil {
		t.Fatalf("write replacement body: %v", err)
	}
	if obj.finish {
		if err := body.Close(); err != nil {
			t.Fatalf("close replacement body: %v", err)
		}
	}
}

// lookupState runs a plain lookup for key and returns its state flags.
func lookupState(t *testing.T, i *Instance, key string) uint32 {
	t.Helper()
	return handleState(t, i, plainLookup(t, i, key))
}

// lookupContent runs a plain lookup for key and reads the whole cached body.
func lookupContent(t *testing.T, i *Instance, key string) string {
	t.Helper()
	if status := i.xqd_cache_get_body(plainLookup(t, i, key), 0, 0, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("cache_get_body status = %d, want %d", status, XqdStatusOK)
	}
	return readTestBody(t, i, int(i.memory.Uint32(replaceTestHandleOut)))
}

func readTestBody(t *testing.T, i *Instance, bodyID int) string {
	t.Helper()
	data, err := io.ReadAll(i.bodies.Get(bodyID))
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if status := i.xqd_body_close(int32(bodyID)); status != XqdStatusOK {
		t.Fatalf("body_close status = %d", status)
	}
	return string(data)
}

func TestCacheReplaceWithoutExistingObject(t *testing.T) {
	i := newCacheReplaceTestInstance()
	handle := beginReplace(t, i, "missing", CacheReplaceImmediate)

	if status := i.xqd_cache_replace_get_state(handle, replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("replace_get_state status = %d, want %d", status, XqdStatusOK)
	}
	if flags := i.memory.Uint32(replaceTestValueOut); flags != 0 {
		t.Fatalf("replace_get_state flags = %#x, want 0", flags)
	}

	accessors := map[string]func() int32{
		"age":     func() int32 { return i.xqd_cache_replace_get_age_ns(handle, replaceTestValueOut) },
		"hits":    func() int32 { return i.xqd_cache_replace_get_hits(handle, replaceTestValueOut) },
		"length":  func() int32 { return i.xqd_cache_replace_get_length(handle, replaceTestValueOut) },
		"max_age": func() int32 { return i.xqd_cache_replace_get_max_age_ns(handle, replaceTestValueOut) },
		"swr":     func() int32 { return i.xqd_cache_replace_get_stale_while_revalidate_ns(handle, replaceTestValueOut) },
		"body":    func() int32 { return i.xqd_cache_replace_get_body(handle, 0, 0, replaceTestHandleOut) },
		"user_metadata": func() int32 {
			return i.xqd_cache_replace_get_user_metadata(handle, replaceTestMetadataOut, 128, replaceTestNwrittenOut)
		},
	}
	checkStatuses(t, accessors, XqdErrNone)

	executeReplace(t, i, handle, replaceTestObject{content: "fresh", maxAge: time.Minute, metadata: "meta", length: true, finish: true})

	if flags := lookupState(t, i, "missing"); flags != CacheLookupStateFound|CacheLookupStateUsable {
		t.Fatalf("lookup flags after replace = %#x, want found|usable", flags)
	}
	if content := lookupContent(t, i, "missing"); content != "fresh" {
		t.Fatalf("lookup content after replace = %q, want %q", content, "fresh")
	}
}

func TestCacheReplaceExposesExistingObject(t *testing.T) {
	i := newCacheReplaceTestInstance()
	existing := replaceTestObject{
		content:  "old content",
		maxAge:   time.Hour,
		initial:  5 * time.Second,
		swr:      30 * time.Second,
		metadata: "old-meta",
		length:   true,
		finish:   true,
	}
	insertTestObject(t, i, "key", existing)
	lookupState(t, i, "key")

	handle := beginReplace(t, i, "key", CacheReplaceImmediate)

	if status := i.xqd_cache_replace_get_state(handle, replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("replace_get_state status = %d", status)
	}
	if flags := i.memory.Uint32(replaceTestValueOut); flags != CacheLookupStateFound|CacheLookupStateUsable {
		t.Fatalf("replace_get_state flags = %#x, want found|usable", flags)
	}

	if status := i.xqd_cache_replace_get_length(handle, replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("replace_get_length status = %d", status)
	}
	if got := i.memory.Uint64(replaceTestValueOut); got != uint64(len(existing.content)) {
		t.Fatalf("replace_get_length = %d, want %d", got, len(existing.content))
	}

	if status := i.xqd_cache_replace_get_max_age_ns(handle, replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("replace_get_max_age_ns status = %d", status)
	}
	if got := i.memory.Uint64(replaceTestValueOut); got != uint64(time.Hour.Nanoseconds()) {
		t.Fatalf("replace_get_max_age_ns = %d, want %d", got, time.Hour.Nanoseconds())
	}

	if status := i.xqd_cache_replace_get_stale_while_revalidate_ns(handle, replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("replace_get_stale_while_revalidate_ns status = %d", status)
	}
	if got := i.memory.Uint64(replaceTestValueOut); got != uint64((30 * time.Second).Nanoseconds()) {
		t.Fatalf("replace_get_stale_while_revalidate_ns = %d, want %d", got, (30 * time.Second).Nanoseconds())
	}

	if status := i.xqd_cache_replace_get_age_ns(handle, replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("replace_get_age_ns status = %d", status)
	}
	if got := i.memory.Uint64(replaceTestValueOut); got < uint64((5*time.Second).Nanoseconds()) || got > uint64((10*time.Second).Nanoseconds()) {
		t.Fatalf("replace_get_age_ns = %d, want about 5s", got)
	}

	if status := i.xqd_cache_replace_get_hits(handle, replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("replace_get_hits status = %d", status)
	}
	if got := i.memory.Uint64(replaceTestValueOut); got != 2 {
		t.Fatalf("replace_get_hits = %d, want 2 after one lookup and the replace", got)
	}

	if status := i.xqd_cache_replace_get_user_metadata(handle, replaceTestMetadataOut, 128, replaceTestNwrittenOut); status != XqdStatusOK {
		t.Fatalf("replace_get_user_metadata status = %d", status)
	}
	n := i.memory.Uint32(replaceTestNwrittenOut)
	meta := make([]byte, n)
	_, _ = i.memory.ReadAt(meta, replaceTestMetadataOut)
	if string(meta) != "old-meta" {
		t.Fatalf("replace_get_user_metadata = %q, want %q", meta, "old-meta")
	}

	if status := i.xqd_cache_replace_get_body(handle, 0, 0, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("replace_get_body status = %d", status)
	}
	if got := readTestBody(t, i, int(i.memory.Uint32(replaceTestHandleOut))); got != existing.content {
		t.Fatalf("replace_get_body = %q, want %q", got, existing.content)
	}

	executeReplace(t, i, handle, replaceTestObject{content: "new content", maxAge: time.Minute, finish: true})

	if content := lookupContent(t, i, "key"); content != "new content" {
		t.Fatalf("lookup content after replace = %q, want %q", content, "new content")
	}
	onlyObject(t, i, "key")
}

func TestCacheReplaceUserMetadataShortBuffer(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "x", maxAge: time.Minute, metadata: "twelve-bytes", finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)

	if status := i.xqd_cache_replace_get_user_metadata(handle, replaceTestMetadataOut, 4, replaceTestNwrittenOut); status != XqdErrBufferLength {
		t.Fatalf("replace_get_user_metadata status = %d, want %d", status, XqdErrBufferLength)
	}
	if n := i.memory.Uint32(replaceTestNwrittenOut); n != uint32(len("twelve-bytes")) {
		t.Fatalf("nwritten = %d, want the required size %d", n, len("twelve-bytes"))
	}
}

func TestCacheReplaceBodyRange(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "0123456789", maxAge: time.Minute, finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)

	readRange := func(mask uint32, from, to uint64) string {
		t.Helper()
		i.memory.WriteUint64(replaceTestBodyOptsPtr, from)
		i.memory.WriteUint64(replaceTestBodyOptsPtr+8, to)
		if status := i.xqd_cache_replace_get_body(handle, mask, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
			t.Fatalf("replace_get_body status = %d", status)
		}
		return readTestBody(t, i, int(i.memory.Uint32(replaceTestHandleOut)))
	}

	both := CacheGetBodyOptionsMaskFrom | CacheGetBodyOptionsMaskTo
	if got := readRange(both, 2, 4); got != "234" {
		t.Fatalf("range 2..4 = %q, want %q (bounds are inclusive)", got, "234")
	}
	if got := readRange(both, 7, 7); got != "7" {
		t.Fatalf("range 7..7 = %q, want a single byte", got)
	}
	if got := readRange(CacheGetBodyOptionsMaskFrom, 8, 0); got != "89" {
		t.Fatalf("range 8.. = %q, want %q", got, "89")
	}
	if got := readRange(CacheGetBodyOptionsMaskTo, 0, 3); got != "789" {
		t.Fatalf("range ..3 = %q, want the last three bytes", got)
	}
	// CacheD finds a zero-length suffix invalid and serves the whole object
	// (libcached-protocol range.rs isvalidrange and deliverable_range).
	if got := readRange(CacheGetBodyOptionsMaskTo, 0, 0); got != "0123456789" {
		t.Fatalf("range ..0 = %q, want the whole body", got)
	}
	if got := readRange(CacheGetBodyOptionsMaskTo, 0, 50); got != "0123456789" {
		t.Fatalf("range ..50 = %q, want the whole body", got)
	}
	if got := readRange(both, 5, 20); got != "0123456789" {
		t.Fatalf("range past the end = %q, want the whole body", got)
	}
	i.memory.WriteUint64(replaceTestBodyOptsPtr, 6)
	i.memory.WriteUint64(replaceTestBodyOptsPtr+8, 2)
	bodies := len(i.bodies.handles)
	if status := i.xqd_cache_replace_get_body(handle, both, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdErrInvalidArgument {
		t.Fatalf("inverted range status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if len(i.bodies.handles) != bodies {
		t.Fatal("a rejected inverted range allocated a body handle")
	}
}

func TestCacheReplaceBodyRangeWhileStreaming(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "01234", maxAge: time.Minute, finish: false})
	writer := i.bodies.Get(int(i.memory.Uint32(replaceTestHandleOut)))

	handle := beginReplace(t, i, "key", CacheReplaceImmediate)

	// The size is unknown and the guest did not insist on the range, so the
	// whole body is streamed.
	i.memory.WriteUint64(replaceTestBodyOptsPtr, 1)
	i.memory.WriteUint64(replaceTestBodyOptsPtr+8, 2)
	if status := i.xqd_cache_replace_get_body(handle, CacheGetBodyOptionsMaskFrom|CacheGetBodyOptionsMaskTo, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("replace_get_body status = %d", status)
	}
	whole := i.bodies.Get(int(i.memory.Uint32(replaceTestHandleOut)))

	// Insisting on the range gives a bounded reader that waits for the bytes.
	// A replace handle serves one reader at a time, so this one gets its own.
	handle = beginReplace(t, i, "key", CacheReplaceImmediate)
	i.cacheReplaceHandles.Get(int(handle)).Replace.Options.AlwaysUseRequestedRange = true
	i.memory.WriteUint64(replaceTestBodyOptsPtr, 6)
	i.memory.WriteUint64(replaceTestBodyOptsPtr+8, 8)
	if status := i.xqd_cache_replace_get_body(handle, CacheGetBodyOptionsMaskFrom|CacheGetBodyOptionsMaskTo, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("replace_get_body status = %d", status)
	}
	ranged := i.bodies.Get(int(i.memory.Uint32(replaceTestHandleOut)))

	type result struct {
		data string
		err  error
	}
	results := make(chan result, 2)
	for _, body := range []*BodyHandle{whole, ranged} {
		go func(body *BodyHandle) {
			data, err := io.ReadAll(body)
			results <- result{string(data), err}
		}(body)
	}

	select {
	case r := <-results:
		t.Fatalf("a read finished before the writer did: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}

	if _, err := writer.Write([]byte("56789")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	got := map[string]bool{}
	for range 2 {
		select {
		case r := <-results:
			if r.err != nil {
				t.Fatalf("read failed: %v", r.err)
			}
			got[r.data] = true
		case <-time.After(2 * time.Second):
			t.Fatal("readers did not finish after the writer closed")
		}
	}
	if !got["0123456789"] || !got["678"] {
		t.Fatalf("reads = %v, want the whole body and the 6..8 range", got)
	}
}

func TestCacheReplaceStaleObjectIsStillFound(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "old", maxAge: time.Second, initial: time.Hour, finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)

	if status := i.xqd_cache_replace_get_state(handle, replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("replace_get_state status = %d", status)
	}
	want := CacheLookupStateFound | CacheLookupStateUsable | CacheLookupStateStale
	if flags := i.memory.Uint32(replaceTestValueOut); flags != want {
		t.Fatalf("replace_get_state flags = %#x, want %#x", flags, want)
	}
	if status := i.xqd_cache_replace_get_body(handle, 0, 0, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("replace_get_body status = %d", status)
	}
	if got := readTestBody(t, i, int(i.memory.Uint32(replaceTestHandleOut))); got != "old" {
		t.Fatalf("stale body = %q, want %q", got, "old")
	}
}

func TestCacheReplaceImmediateKeepsExistingVisible(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "old", maxAge: time.Minute, finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)

	if content := lookupContent(t, i, "key"); content != "old" {
		t.Fatalf("lookup during an immediate replace = %q, want the existing object", content)
	}

	executeReplace(t, i, handle, replaceTestObject{content: "new", maxAge: time.Minute, finish: true})
	if content := lookupContent(t, i, "key"); content != "new" {
		t.Fatalf("lookup after replace = %q, want %q", content, "new")
	}
}

func TestCacheReplaceForceMissHidesExistingObject(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "old", maxAge: time.Minute, finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediateForceMiss)

	if flags := lookupState(t, i, "key"); flags != 0 {
		t.Fatalf("lookup during a force-miss replace = %#x, want a miss", flags)
	}

	// The replacer itself still sees what it is replacing.
	if status := i.xqd_cache_replace_get_state(handle, replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("replace_get_state status = %d", status)
	}
	if flags := i.memory.Uint32(replaceTestValueOut); flags&CacheLookupStateFound == 0 {
		t.Fatalf("replace_get_state flags = %#x, want found", flags)
	}
	if status := i.xqd_cache_replace_get_body(handle, 0, 0, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("replace_get_body status = %d", status)
	}
	if got := readTestBody(t, i, int(i.memory.Uint32(replaceTestHandleOut))); got != "old" {
		t.Fatalf("existing body = %q, want %q", got, "old")
	}

	executeReplace(t, i, handle, replaceTestObject{content: "new", maxAge: time.Minute, finish: true})
	if content := lookupContent(t, i, "key"); content != "new" {
		t.Fatalf("lookup after replace = %q, want %q", content, "new")
	}
}

func TestCacheReplaceForceMissAbandonedStaysGone(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "old", maxAge: time.Minute, finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediateForceMiss)

	if status := i.xqd_cache_close(handle); status != XqdStatusOK {
		t.Fatalf("cache_close on a replace handle status = %d", status)
	}
	if flags := lookupState(t, i, "key"); flags != 0 {
		t.Fatalf("lookup after an abandoned force-miss replace = %#x, want a miss", flags)
	}
	if len(i.cache.replaces) != 0 {
		t.Fatalf("pending replaces after close = %d, want 0", len(i.cache.replaces))
	}
}

func TestCacheReplaceInsertConsumesHandle(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "old", maxAge: time.Minute, finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)
	executeReplace(t, i, handle, replaceTestObject{content: "new", maxAge: time.Minute, finish: true})

	if status := i.xqd_cache_replace_get_state(handle, replaceTestValueOut); status != XqdErrInvalidHandle {
		t.Fatalf("replace_get_state after insert status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_cache_replace_insert(handle, 0, replaceTestWriteOptsPtr, replaceTestHandleOut); status != XqdErrInvalidHandle {
		t.Fatalf("second replace_insert status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_cache_close(handle); status != XqdErrInvalidHandle {
		t.Fatalf("cache_close after insert status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if len(i.cache.replaces) != 0 {
		t.Fatalf("pending replaces after insert = %d, want 0", len(i.cache.replaces))
	}
}

func TestCacheReplaceHandlesDoNotCollideWithCacheHandles(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "old", maxAge: time.Minute, finish: true})

	cacheHandle := plainLookup(t, i, "key")
	replaceHandle := beginReplace(t, i, "key", CacheReplaceImmediate)

	if cacheHandle == replaceHandle {
		t.Fatalf("cache handle and replace handle share id %d", cacheHandle)
	}
	if status := i.xqd_cache_close(replaceHandle); status != XqdStatusOK {
		t.Fatalf("closing the replace handle status = %d", status)
	}
	if status := i.xqd_cache_close(replaceHandle); status != XqdErrInvalidHandle {
		t.Fatalf("closing the replace handle twice status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_cache_get_state(cacheHandle, replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("the cache handle was disturbed by closing the replace handle: status = %d", status)
	}
	if status := i.xqd_cache_close(cacheHandle); status != XqdStatusOK {
		t.Fatalf("closing the cache handle status = %d", status)
	}
}

func TestCacheReplaceOptionValidation(t *testing.T) {
	i := newCacheReplaceTestInstance()
	keyPtr, keyLen := writeCacheKey(i, "key")

	writeReplaceStrategy(i, 42)
	if status := i.xqd_cache_replace(keyPtr, keyLen, CacheReplaceOptionsMaskReplaceStrategy, replaceTestReplaceOpts, replaceTestHandleOut); status != XqdErrInvalidArgument {
		t.Fatalf("unknown strategy status = %d, want %d", status, XqdErrInvalidArgument)
	}

	if status := i.xqd_cache_replace(keyPtr, keyLen, CacheReplaceOptionsMaskService, replaceTestReplaceOpts, replaceTestHandleOut); status != XqdErrUnsupported {
		t.Fatalf("service option status = %d, want %d", status, XqdErrUnsupported)
	}

	i.memory.WriteUint32(replaceTestReplaceOpts, 77)
	if status := i.xqd_cache_replace(keyPtr, keyLen, CacheReplaceOptionsMaskRequestHeaders, replaceTestReplaceOpts, replaceTestHandleOut); status != XqdErrInvalidHandle {
		t.Fatalf("bogus request handle status = %d, want %d", status, XqdErrInvalidHandle)
	}

	// No strategy bit at all means immediate.
	if status := i.xqd_cache_replace(keyPtr, keyLen, 0, replaceTestReplaceOpts, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("default options status = %d", status)
	}
	handle := int(i.memory.Uint32(replaceTestHandleOut))
	if strategy := i.cacheReplaceHandles.Get(handle).Replace.Options.ReplaceStrategy; strategy != CacheReplaceImmediate {
		t.Fatalf("default strategy = %d, want immediate", strategy)
	}
}

func TestCacheReplaceWaitStrategyWaitsForOtherOwners(t *testing.T) {
	cache := NewCache()
	key := []byte("counter")
	first := cache.Replace(key, &CacheReplaceOptions{ReplaceStrategy: CacheReplaceImmediate}, "owner-a")

	// The same owner never waits for itself.
	sameOwner := make(chan *CacheReplace)
	go func() {
		sameOwner <- cache.Replace(key, &CacheReplaceOptions{ReplaceStrategy: CacheReplaceWait}, "owner-a")
	}()
	select {
	case r := <-sameOwner:
		cache.ReplaceAbandon(r)
	case <-time.After(2 * time.Second):
		t.Fatal("a wait replace blocked on its own owner")
	}

	second := make(chan *CacheReplace)
	go func() {
		second <- cache.Replace(key, &CacheReplaceOptions{ReplaceStrategy: CacheReplaceWait}, "owner-b")
	}()
	select {
	case <-second:
		t.Fatal("a wait replace did not wait for the pending replace")
	case <-time.After(50 * time.Millisecond):
	}

	obj := cache.ReplaceInsert(first, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute)})
	_, _ = obj.WriteBody([]byte("1"))
	obj.FinishWrite()

	select {
	case r := <-second:
		if r.Existing.Object != obj {
			t.Fatal("the waiting replace did not see the object inserted before it")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiting replace did not wake up after the insert")
	}
}

func TestCacheReplaceResetAbandonsPendingReplaces(t *testing.T) {
	i := newOwnershipTestInstance()
	i.cache = NewCache()
	i.kvStores = &KVStoreHandles{}
	i.kvLookups = &KVStoreLookupHandles{}
	i.kvInserts = &KVStoreInsertHandles{}
	i.kvDeletes = &KVStoreDeleteHandles{}
	i.kvLists = &KVStoreListHandles{}
	i.secretStoreHandles = &SecretStoreHandles{}
	i.secretHandles = &SecretHandles{}
	i.cacheHandles = &CacheHandles{}
	i.cacheBusyHandles = &CacheBusyHandles{}
	i.cacheReplaceHandles = &CacheReplaceHandles{}
	i.aclHandles = &AclHandles{}
	i.asyncItems = &AsyncItemHandles{}
	i.ds_context = context.Background()

	replace := i.cache.Replace([]byte("key"), &CacheReplaceOptions{ReplaceStrategy: CacheReplaceImmediate}, i)
	i.cacheReplaceHandles.New(replace)

	i.reset()

	if len(i.cache.replaces) != 0 {
		t.Fatalf("pending replaces after reset = %d, want 0", len(i.cache.replaces))
	}
	select {
	case <-replace.done:
	default:
		t.Fatal("reset did not wake waiters of the abandoned replace")
	}
}

func TestCacheReplaceDropsSurrogateIndexOfReplacedObject(t *testing.T) {
	cache := NewCache()
	key := []byte("key")
	old := cache.Insert(key, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute), SurrogateKeys: []string{"old-tag"}})
	old.FinishWrite()

	replace := cache.Replace(key, &CacheReplaceOptions{ReplaceStrategy: CacheReplaceImmediate}, "owner")
	replacement := cache.ReplaceInsert(replace, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute), SurrogateKeys: []string{"new-tag"}})
	replacement.FinishWrite()

	if purged := cache.PurgeSurrogateKey("old-tag"); purged != 0 {
		t.Fatalf("purging the replaced object's surrogate key removed %d keys, want 0", purged)
	}
	if entry := cache.Lookup(key, nil); !entry.State.Found || entry.Object != replacement {
		t.Fatal("the replacement disappeared after purging the old surrogate key")
	}
	if purged := cache.PurgeSurrogateKey("new-tag"); purged != 1 {
		t.Fatalf("purging the replacement's surrogate key removed %d keys, want 1", purged)
	}
}

func TestCacheReplaceFindsVaryVariantByRequestHeaders(t *testing.T) {
	cache := NewCache()
	key := []byte("asset")
	gzip := []byte("Accept-Encoding: gzip\r\n")
	br := []byte("Accept-Encoding: br\r\n")

	gzipObj := cache.Insert(key, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute), VaryRule: "accept-encoding", RequestHeaders: gzip})
	gzipObj.FinishWrite()
	brObj := cache.Insert(key, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute), VaryRule: "accept-encoding", RequestHeaders: br})
	brObj.FinishWrite()

	if got := cache.Replace(key, &CacheReplaceOptions{RequestHeaders: gzip}, "owner").Existing.Object; got != gzipObj {
		t.Fatal("replace with gzip headers did not find the gzip variant")
	}
	if got := cache.Replace(key, &CacheReplaceOptions{RequestHeaders: br}, "owner").Existing.Object; got != brObj {
		t.Fatal("replace with br headers did not find the br variant")
	}
	if got := cache.Replace(key, &CacheReplaceOptions{RequestHeaders: []byte("Accept-Encoding: zstd\r\n")}, "owner").Existing.Object; got != nil {
		t.Fatal("replace with unmatched headers found a variant")
	}
	if got := cache.Lookup(key, &CacheLookupOptions{RequestHeaders: br}).Object; got != brObj {
		t.Fatal("lookup with br headers did not find the br variant")
	}
}

func TestCacheReplaceWaitStrategyQueuesBehindTransactions(t *testing.T) {
	cache := NewCache()
	key := []byte("counter")
	tx := cache.TransactionLookup(key, nil, "owner-a")
	if !tx.Entry.State.MustInsertOrUpdate {
		t.Fatal("first transaction lookup should be obligated to insert")
	}

	waited := make(chan *CacheReplace)
	go func() {
		waited <- cache.Replace(key, &CacheReplaceOptions{ReplaceStrategy: CacheReplaceWait}, "owner-b")
	}()
	select {
	case <-waited:
		t.Fatal("a wait replace did not queue behind the pending transaction")
	case <-time.After(50 * time.Millisecond):
	}

	inserted := transactionInsertFinished(t, cache, tx, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute)})

	select {
	case r := <-waited:
		if r.Existing.Object != inserted {
			t.Fatal("the waiting replace did not see the object the transaction inserted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiting replace did not wake up when the transaction completed")
	}
}

func TestCacheTransactionLookupWaitsForForeignForceMissReplace(t *testing.T) {
	cache := NewCache()
	key := []byte("key")
	old := cache.Insert(key, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute)})
	old.FinishWrite()

	replace := cache.Replace(key, &CacheReplaceOptions{ReplaceStrategy: CacheReplaceImmediateForceMiss}, "owner-a")

	// The owner of the replace never waits for itself.
	own := cache.TransactionLookup(key, nil, "owner-a")
	if own.Entry.State.Found || !own.Entry.State.MustInsertOrUpdate {
		t.Fatal("the replacing owner should see a plain miss")
	}
	cache.TransactionCancel(own)

	waited := make(chan *CacheTransaction)
	go func() {
		waited <- cache.TransactionLookup(key, nil, "owner-b")
	}()
	select {
	case <-waited:
		t.Fatal("a foreign transaction lookup did not wait for the force-miss replace")
	case <-time.After(50 * time.Millisecond):
	}

	replacement := cache.ReplaceInsert(replace, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute)})
	replacement.FinishWrite()

	select {
	case tx := <-waited:
		if tx.Entry.Object != replacement || !tx.Entry.State.Usable {
			t.Fatal("the waiting lookup did not get the replacement")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiting lookup did not wake up after the replacement was provided")
	}
}

func TestCacheReplaceResetAbandonsPendingTransactions(t *testing.T) {
	cache := NewCache()
	key := []byte("key")
	tx := cache.TransactionLookup(key, nil, "owner-a")

	cache.AbandonTransactions("owner-b")
	if len(cache.transactions) != 1 {
		t.Fatal("abandoning another owner's transactions touched this one")
	}

	cache.AbandonTransactions("owner-a")
	if len(cache.transactions) != 0 {
		t.Fatal("the owner's pending transaction survived abandonment")
	}
	select {
	case <-tx.done:
	default:
		t.Fatal("abandonment did not wake waiters of the transaction")
	}
}

func TestCacheVaryRuleWithSeveralHeaders(t *testing.T) {
	cache := NewCache()
	key := []byte("asset")
	gzipFr := []byte("Accept-Encoding: gzip\r\nAccept-Language: fr\r\n")
	gzipEn := []byte("Accept-Encoding: gzip\r\nAccept-Language: en\r\n")

	frObj := cache.Insert(key, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute), VaryRule: "accept-encoding accept-language", RequestHeaders: gzipFr})
	frObj.FinishWrite()
	enObj := cache.Insert(key, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute), VaryRule: "accept-encoding accept-language", RequestHeaders: gzipEn})
	enObj.FinishWrite()

	if got := cache.Replace(key, &CacheReplaceOptions{RequestHeaders: gzipFr}, "owner").Existing.Object; got != frObj {
		t.Fatal("replace did not find the fr variant of a space separated vary rule")
	}
	if got := cache.Lookup(key, &CacheLookupOptions{RequestHeaders: gzipEn}).Object; got != enObj {
		t.Fatal("lookup did not find the en variant of a space separated vary rule")
	}
	if got := cache.Lookup(key, &CacheLookupOptions{RequestHeaders: []byte("Accept-Encoding: gzip\r\nAccept-Language: de\r\n")}).Object; got != nil {
		t.Fatal("lookup matched a variant whose second vary header differs")
	}
}

func TestCacheTransactionLookupWaitsDespiteOwnersMissTransaction(t *testing.T) {
	cache := NewCache()
	key := []byte("key")
	old := cache.Insert(key, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute)})
	old.FinishWrite()

	replace := cache.Replace(key, &CacheReplaceOptions{ReplaceStrategy: CacheReplaceImmediateForceMiss}, "owner-a")
	own := cache.TransactionLookup(key, nil, "owner-a")
	if own.Entry.State.Found {
		t.Fatal("the replacing owner should see a miss")
	}

	waited := make(chan *CacheTransaction)
	go func() {
		waited <- cache.TransactionLookup(key, nil, "owner-b")
	}()
	select {
	case <-waited:
		t.Fatal("a foreign lookup joined the replacer's miss transaction instead of waiting")
	case <-time.After(50 * time.Millisecond):
	}

	replacement := cache.ReplaceInsert(replace, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute)})
	replacement.FinishWrite()

	select {
	case tx := <-waited:
		if tx == own || tx.Entry.Object != replacement || !tx.Entry.State.Usable {
			t.Fatal("the waiting lookup did not get the replacement")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiting lookup did not wake up after the replacement was provided")
	}
	cache.TransactionCancel(own)
}

func TestCacheTransactionLookupCollapsesOnForeignTransaction(t *testing.T) {
	cache := NewCache()
	key := []byte("key")
	fetcher := cache.TransactionLookup(key, nil, "owner-a")
	if !fetcher.Entry.State.MustInsertOrUpdate {
		t.Fatal("the first lookup should be obligated to fetch")
	}

	waited := make(chan *CacheTransaction)
	go func() {
		waited <- cache.TransactionLookup(key, nil, "owner-b")
	}()
	select {
	case <-waited:
		t.Fatal("a foreign lookup did not collapse onto the pending transaction")
	case <-time.After(50 * time.Millisecond):
	}

	inserted := transactionInsertFinished(t, cache, fetcher, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute)})

	select {
	case tx := <-waited:
		if tx.Entry.Object != inserted || tx.Entry.State.MustInsertOrUpdate {
			t.Fatal("the collapsed lookup was not served the inserted object")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the collapsed lookup did not wake up when the fetcher completed")
	}
}

func TestCacheReplaceRejectsBadOutputPointers(t *testing.T) {
	i := newCacheReplaceTestInstance()
	keyPtr, keyLen := writeCacheKey(i, "key")
	writeReplaceStrategy(i, CacheReplaceImmediate)

	pastTheEnd := int32(i.memory.Len())
	if status := i.xqd_cache_replace(keyPtr, keyLen, CacheReplaceOptionsMaskReplaceStrategy, replaceTestReplaceOpts, pastTheEnd); status != XqdErrInvalidArgument {
		t.Fatalf("replace with a bad output pointer status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if len(i.cache.replaces) != 0 {
		t.Fatal("a rejected replace was left pending")
	}

	handle := beginReplace(t, i, "key", CacheReplaceImmediate)
	if status := i.xqd_cache_replace_insert(handle, 0, replaceTestWriteOptsPtr, pastTheEnd); status != XqdErrInvalidArgument {
		t.Fatalf("replace_insert with a bad output pointer status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if i.cacheReplaceHandles.Get(int(handle)) == nil {
		t.Fatal("a rejected replace_insert consumed the handle")
	}
	if lookupState(t, i, "key") != 0 {
		t.Fatal("a rejected replace_insert stored an object")
	}
}

func TestCacheReplaceInsertRejectsHostileOptionLengths(t *testing.T) {
	i := newCacheReplaceTestInstance()
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)

	i.memory.WriteUint64(replaceTestWriteOptsPtr, uint64(time.Minute))
	i.memory.WriteUint32(replaceTestWriteOptsPtr+replaceTestWriteMetadata, replaceTestMetadataPtr)
	i.memory.WriteUint32(replaceTestWriteOptsPtr+replaceTestWriteMetadata+4, 0x7fffffff)
	if status := i.xqd_cache_replace_insert(handle, CacheWriteOptionsMaskUserMetadata, replaceTestWriteOptsPtr, replaceTestHandleOut); status != XqdErrInvalidArgument {
		t.Fatalf("replace_insert with an oversized metadata length status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if i.cacheReplaceHandles.Get(int(handle)) == nil {
		t.Fatal("a rejected replace_insert consumed the handle")
	}

	pastTheEnd := int32(i.memory.Len()) - 8
	if status := i.xqd_cache_replace_insert(handle, 0, pastTheEnd, replaceTestHandleOut); status != XqdErrInvalidArgument {
		t.Fatalf("replace_insert with a truncated options struct status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if lookupState(t, i, "key") != 0 {
		t.Fatal("a rejected replace_insert stored an object")
	}
}

func TestCacheReplaceAccessorsRejectBadPointers(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "old", maxAge: time.Minute, metadata: "meta", finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)
	pastTheEnd := int32(i.memory.Len())
	bodies := len(i.bodies.handles)

	checks := map[string]func() int32{
		"age":     func() int32 { return i.xqd_cache_replace_get_age_ns(handle, pastTheEnd) },
		"hits":    func() int32 { return i.xqd_cache_replace_get_hits(handle, pastTheEnd) },
		"length":  func() int32 { return i.xqd_cache_replace_get_length(handle, pastTheEnd) },
		"max_age": func() int32 { return i.xqd_cache_replace_get_max_age_ns(handle, pastTheEnd) },
		"swr":     func() int32 { return i.xqd_cache_replace_get_stale_while_revalidate_ns(handle, pastTheEnd) },
		"state":   func() int32 { return i.xqd_cache_replace_get_state(handle, pastTheEnd) },
		"body":    func() int32 { return i.xqd_cache_replace_get_body(handle, 0, 0, pastTheEnd) },
		"body_options": func() int32 {
			return i.xqd_cache_replace_get_body(handle, CacheGetBodyOptionsMaskFrom, pastTheEnd-4, replaceTestHandleOut)
		},
		"metadata_buffer": func() int32 {
			return i.xqd_cache_replace_get_user_metadata(handle, pastTheEnd-2, 8, replaceTestNwrittenOut)
		},
		"metadata_nwritten": func() int32 {
			return i.xqd_cache_replace_get_user_metadata(handle, replaceTestMetadataOut, 128, pastTheEnd)
		},
	}
	checkStatuses(t, checks, XqdErrInvalidArgument)
	if len(i.bodies.handles) != bodies {
		t.Fatal("a rejected replace_get_body allocated a body handle")
	}
}

func TestCacheCloseCancelsPendingTransaction(t *testing.T) {
	i := newCacheReplaceTestInstance()
	handle := transactionLookup(t, i, "key")
	if len(i.cache.transactions) != 1 {
		t.Fatal("a miss transaction lookup did not register a pending transaction")
	}

	if status := i.xqd_cache_close(handle); status != XqdStatusOK {
		t.Fatalf("cache_close status = %d", status)
	}
	if len(i.cache.transactions) != 0 {
		t.Fatal("closing the handle left the transaction pending")
	}
}

func TestCacheWriteOptionsRejectServiceAndTruncatedRecords(t *testing.T) {
	i := newCacheReplaceTestInstance()
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)

	i.memory.WriteUint64(replaceTestWriteOptsPtr, uint64(time.Minute))
	if status := i.xqd_cache_replace_insert(handle, CacheWriteOptionsMaskService, replaceTestWriteOptsPtr, replaceTestHandleOut); status != XqdErrUnsupported {
		t.Fatalf("replace_insert on behalf of a service status = %d, want %d", status, XqdErrUnsupported)
	}

	almostTheEnd := int32(i.memory.Len()) - cacheWriteOptionsSize + 4
	if status := i.xqd_cache_replace_insert(handle, 0, almostTheEnd, replaceTestHandleOut); status != XqdErrInvalidArgument {
		t.Fatalf("replace_insert with a record short of the C layout status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if i.cacheReplaceHandles.Get(int(handle)) == nil {
		t.Fatal("a rejected replace_insert consumed the handle")
	}
}

func TestCacheReplaceRejectsTruncatedOptions(t *testing.T) {
	i := newCacheReplaceTestInstance()
	keyPtr, keyLen := writeCacheKey(i, "key")
	almostTheEnd := int32(i.memory.Len()) - cacheReplaceOptionsSize + 4
	if status := i.xqd_cache_replace(keyPtr, keyLen, 0, almostTheEnd, replaceTestHandleOut); status != XqdErrInvalidArgument {
		t.Fatalf("replace with a truncated options record status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if len(i.cache.replaces) != 0 {
		t.Fatal("a rejected replace was left pending")
	}
}

func TestCacheReplaceSuffixRangeWaitsForUnknownLength(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "01234", maxAge: time.Minute, finish: false})
	writer := i.bodies.Get(int(i.memory.Uint32(replaceTestHandleOut)))

	handle := beginReplace(t, i, "key", CacheReplaceImmediate)
	i.cacheReplaceHandles.Get(int(handle)).Replace.Options.AlwaysUseRequestedRange = true

	i.memory.WriteUint64(replaceTestBodyOptsPtr+8, 3)
	if status := i.xqd_cache_replace_get_body(handle, CacheGetBodyOptionsMaskTo, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("replace_get_body status = %d", status)
	}
	suffix := i.bodies.Get(int(i.memory.Uint32(replaceTestHandleOut)))

	result := make(chan string, 1)
	go func() {
		data, err := io.ReadAll(suffix)
		if err != nil {
			result <- "error: " + err.Error()
			return
		}
		result <- string(data)
	}()

	select {
	case got := <-result:
		t.Fatalf("a suffix read finished before the writer did: %q", got)
	case <-time.After(50 * time.Millisecond):
	}

	if _, err := writer.Write([]byte("56789")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	select {
	case got := <-result:
		if got != "789" {
			t.Fatalf("suffix read = %q, want the last three bytes", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the suffix read did not finish after the writer closed")
	}
}

func TestCacheReplaceAbandonedBodyIsDiscarded(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "old", maxAge: time.Minute, finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)
	executeReplace(t, i, handle, replaceTestObject{content: "partial", maxAge: time.Minute, finish: false})
	bodyID := int32(i.memory.Uint32(replaceTestHandleOut))

	// A reader that started before the writer gave up must not be told the
	// prefix is the whole object.
	if status := i.xqd_cache_get_body(plainLookup(t, i, "key"), 0, 0, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("cache_get_body status = %d", status)
	}
	reader := i.bodies.Get(int(i.memory.Uint32(replaceTestHandleOut)))
	readErr := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(reader)
		readErr <- err
	}()

	if status := i.xqd_body_abandon(bodyID); status != XqdStatusOK {
		t.Fatalf("body_abandon status = %d", status)
	}

	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("a reader of the abandoned replacement got a clean EOF")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a reader of the abandoned replacement never woke up")
	}
	if flags := lookupState(t, i, "key"); flags != 0 {
		t.Fatalf("lookup after an abandoned replacement = %#x, want a miss", flags)
	}
}

func TestCacheReplaceResetDiscardsUnfinishedReplacement(t *testing.T) {
	i := newOwnershipTestInstance()
	i.cache = NewCache()
	i.kvStores = &KVStoreHandles{}
	i.kvLookups = &KVStoreLookupHandles{}
	i.kvInserts = &KVStoreInsertHandles{}
	i.kvDeletes = &KVStoreDeleteHandles{}
	i.kvLists = &KVStoreListHandles{}
	i.secretStoreHandles = &SecretStoreHandles{}
	i.secretHandles = &SecretHandles{}
	i.cacheHandles = &CacheHandles{}
	i.cacheBusyHandles = &CacheBusyHandles{}
	i.cacheReplaceHandles = &CacheReplaceHandles{}
	i.aclHandles = &AclHandles{}
	i.asyncItems = &AsyncItemHandles{}
	i.ds_context = context.Background()

	insertTestObject(t, i, "key", replaceTestObject{content: "old", maxAge: time.Minute, finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)
	executeReplace(t, i, handle, replaceTestObject{content: "partial", maxAge: time.Minute, finish: false})
	cache := i.cache

	i.reset()

	if entry := cache.Lookup([]byte("key"), nil); entry.State.Found {
		t.Fatal("reset published an unfinished replacement")
	}
}

func TestCacheReplaceSuffixReaderFailsOnAbandonedWrite(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "01234", maxAge: time.Minute, finish: false})
	writerID := int32(i.memory.Uint32(replaceTestHandleOut))

	handle := beginReplace(t, i, "key", CacheReplaceImmediate)
	i.cacheReplaceHandles.Get(int(handle)).Replace.Options.AlwaysUseRequestedRange = true
	i.memory.WriteUint64(replaceTestBodyOptsPtr+8, 3)
	if status := i.xqd_cache_replace_get_body(handle, CacheGetBodyOptionsMaskTo, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("replace_get_body status = %d", status)
	}
	suffix := i.bodies.Get(int(i.memory.Uint32(replaceTestHandleOut)))

	readErr := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(suffix)
		readErr <- err
	}()
	if status := i.xqd_body_abandon(writerID); status != XqdStatusOK {
		t.Fatalf("body_abandon status = %d", status)
	}
	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("a suffix read of an abandoned object got a clean EOF")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the suffix reader never woke up")
	}
}

func TestCacheReplaceBodyRangeAboveSignedLimit(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "0123456789", maxAge: time.Minute, finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)

	i.memory.WriteUint64(replaceTestBodyOptsPtr, 0)
	i.memory.WriteUint64(replaceTestBodyOptsPtr+8, ^uint64(0))
	both := CacheGetBodyOptionsMaskFrom | CacheGetBodyOptionsMaskTo
	if status := i.xqd_cache_replace_get_body(handle, both, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("range 0..u64::MAX status = %d, want ok", status)
	}
	if got := readTestBody(t, i, int(i.memory.Uint32(replaceTestHandleOut))); got != "0123456789" {
		t.Fatalf("range 0..u64::MAX = %q, want the whole body", got)
	}

	i.memory.WriteUint64(replaceTestBodyOptsPtr, ^uint64(0))
	i.memory.WriteUint64(replaceTestBodyOptsPtr+8, 5)
	if status := i.xqd_cache_replace_get_body(handle, both, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdErrInvalidArgument {
		t.Fatalf("range u64::MAX..5 status = %d, want %d", status, XqdErrInvalidArgument)
	}
}

// With a forced range on an object still streaming, CacheD commits to the
// range once the object reaches its start, and a short object then fails the
// read after the bytes it has.
// An object that finishes before the start gets the whole object instead
// (libcached-server body_handle.rs wait_until_range_is_provided), and the
// client passes it on without checking it against the request (found.rs
// recv_body).
func TestCacheReplaceForcedRangeOfAStreamingObject(t *testing.T) {
	forcedRead := func(t *testing.T, mask uint32, from, to uint64) (string, error) {
		t.Helper()
		i := newCacheReplaceTestInstance()
		insertTestObject(t, i, "key", replaceTestObject{content: "01234", maxAge: time.Minute, finish: false})
		writer := i.bodies.Get(int(i.memory.Uint32(replaceTestHandleOut)))

		handle := beginReplace(t, i, "key", CacheReplaceImmediate)
		i.cacheReplaceHandles.Get(int(handle)).Replace.Options.AlwaysUseRequestedRange = true
		i.memory.WriteUint64(replaceTestBodyOptsPtr, from)
		i.memory.WriteUint64(replaceTestBodyOptsPtr+8, to)
		if status := i.xqd_cache_replace_get_body(handle, mask, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
			t.Fatalf("replace_get_body status = %d", status)
		}
		body := i.bodies.Get(int(i.memory.Uint32(replaceTestHandleOut)))

		type result struct {
			data string
			err  error
		}
		done := make(chan result, 1)
		go func() {
			data, err := io.ReadAll(body)
			done <- result{string(data), err}
		}()
		if err := writer.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		select {
		case r := <-done:
			return r.data, r.err
		case <-time.After(2 * time.Second):
			t.Fatal("the forced range read never finished")
			return "", nil
		}
	}

	const from, to = CacheGetBodyOptionsMaskFrom, CacheGetBodyOptionsMaskTo
	for _, tt := range []struct {
		name     string
		mask     uint32
		from, to uint64
		want     string
		wantErr  bool
	}{
		{"start past the end", from, 10, 0, "01234", false},
		{"range past the end", from | to, 7, 9, "01234", false},
		{"start inside the body", from, 3, 0, "34", false},
		{"range ending past the end", from | to, 3, 8, "34", true},
		{"suffix", to, 0, 2, "34", false},
		{"empty suffix", to, 0, 0, "01234", false},
	} {
		if data, err := forcedRead(t, tt.mask, tt.from, tt.to); data != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("forced %s = %q, %v, want %q (error %v)", tt.name, data, err, tt.want, tt.wantErr)
		}
	}
}

func TestCacheReplaceHugeDeclaredLengthFallsBackToRealSize(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "0123456789", maxAge: time.Minute, finish: true})
	huge := ^uint64(0)
	onlyObject(t, i, "key").Length = &huge
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)

	i.memory.WriteUint64(replaceTestBodyOptsPtr+8, 3)
	if status := i.xqd_cache_replace_get_body(handle, CacheGetBodyOptionsMaskTo, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("replace_get_body status = %d", status)
	}
	if got := readTestBody(t, i, int(i.memory.Uint32(replaceTestHandleOut))); got != "789" {
		t.Fatalf("suffix of an object with an unrepresentable declared length = %q, want the real suffix", got)
	}
}

func TestCacheReplaceGetBodyValidatesOptionsWithoutRangeBits(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "old", maxAge: time.Minute, finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)
	bodies := len(i.bodies.handles)

	if status := i.xqd_cache_replace_get_body(handle, 0, int32(i.memory.Len())-8, replaceTestHandleOut); status != XqdErrInvalidArgument {
		t.Fatalf("replace_get_body with a bad options pointer and no range bits status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if len(i.bodies.handles) != bodies {
		t.Fatal("a rejected replace_get_body allocated a body handle")
	}
}

func TestCacheReplaceOneBodyReaderAtATime(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "old", maxAge: time.Minute, finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)

	if status := i.xqd_cache_replace_get_body(handle, 0, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("first replace_get_body status = %d", status)
	}
	first := int32(i.memory.Uint32(replaceTestHandleOut))
	if status := i.xqd_cache_replace_get_body(handle, 0, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdErrInvalidHandle {
		t.Fatalf("second replace_get_body with the first reader open status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_body_close(first); status != XqdStatusOK {
		t.Fatalf("body_close status = %d", status)
	}
	if status := i.xqd_cache_replace_get_body(handle, 0, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("replace_get_body after closing the first reader status = %d", status)
	}
	second := int32(i.memory.Uint32(replaceTestHandleOut))
	if status := i.xqd_body_abandon(second); status != XqdStatusOK {
		t.Fatalf("body_abandon status = %d", status)
	}
	if status := i.xqd_cache_replace_get_body(handle, 0, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("replace_get_body after abandoning the second reader status = %d", status)
	}
}

func TestCacheReplaceUserMetadataProbeWithEmptyBuffer(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "old", maxAge: time.Minute, metadata: "twelve-bytes", finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)

	if status := i.xqd_cache_replace_get_user_metadata(handle, int32(i.memory.Len()), 0, replaceTestNwrittenOut); status != XqdErrBufferLength {
		t.Fatalf("metadata probe with an empty buffer status = %d, want %d", status, XqdErrBufferLength)
	}
	if n := i.memory.Uint32(replaceTestNwrittenOut); n != uint32(len("twelve-bytes")) {
		t.Fatalf("nwritten = %d, want the required size", n)
	}
	if status := i.xqd_cache_replace_get_user_metadata(handle, int32(i.memory.Len())-2, 64, replaceTestNwrittenOut); status != XqdErrInvalidArgument {
		t.Fatalf("metadata copy into a bad buffer status = %d, want %d", status, XqdErrInvalidArgument)
	}
}

func TestCacheReplaceInsertBodyCannotBeAppended(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "old", maxAge: time.Minute, finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)
	executeReplace(t, i, handle, replaceTestObject{content: "partial", maxAge: time.Minute, finish: false})
	replacement := int32(i.memory.Uint32(replaceTestHandleOut))

	// Insert bodies are streaming, so they cannot be an append source.
	dstID, _ := i.bodies.NewBuffer()
	if status := i.xqd_body_append(int32(dstID), replacement); status != XqdErrInvalidHandle {
		t.Fatalf("body_append status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_body_abandon(replacement); status != XqdStatusOK {
		t.Fatalf("body_abandon status = %d", status)
	}
	if flags := lookupState(t, i, "key"); flags != 0 {
		t.Fatalf("lookup after abandoning the replacement = %#x, want a miss", flags)
	}
}

func TestCacheReplaceInsertBodyIsStreaming(t *testing.T) {
	i := newCacheReplaceTestInstance()
	i.responses = &ResponseHandles{}
	insertTestObject(t, i, "key", replaceTestObject{content: "old", maxAge: time.Minute, finish: true})
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)
	if status := i.xqd_cache_replace_insert(handle, 0, replaceTestWriteOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("replace_insert status = %d", status)
	}
	bodyID := int32(i.memory.Uint32(replaceTestHandleOut))

	_, _ = i.memory.WriteAt([]byte("new"), replaceTestMetadataPtr)
	if status := i.xqd_body_write(bodyID, replaceTestMetadataPtr, 3, BodyWriteEndFront, replaceTestNwrittenOut); status != XqdErrUnsupported {
		t.Errorf("front write status = %d, want %d", status, XqdErrUnsupported)
	}
	if status := i.xqd_body_read(bodyID, replaceTestMetadataOut, 16, replaceTestNwrittenOut); status != XqdErrInvalidHandle {
		t.Errorf("body_read status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_body_write(bodyID, replaceTestMetadataPtr, 3, BodyWriteEndBack, replaceTestNwrittenOut); status != XqdStatusOK {
		t.Fatalf("body_write status = %d", status)
	}
	if status := i.xqd_body_known_length(bodyID, replaceTestValueOut); status != XqdErrNone {
		t.Errorf("body_known_length status = %d, want %d", status, XqdErrNone)
	}
	respID, _ := i.responses.New()
	if status := i.xqd_resp_send_downstream(int32(respID), bodyID, 1); status != XqdErrInvalidHandle {
		t.Errorf("send_downstream status = %d, want %d", status, XqdErrInvalidHandle)
	}

	if status := i.xqd_body_close(bodyID); status != XqdStatusOK {
		t.Fatalf("body_close status = %d", status)
	}
	if content := lookupContent(t, i, "key"); content != "new" {
		t.Fatalf("cache holds %q, want %q", content, "new")
	}
}

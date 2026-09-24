package fastlike

import (
	"math"
	"net/http"
	"testing"
	"time"
)

// accessorValue returns what an accessor that returned status wrote to out.
func accessorValue(t *testing.T, i *Instance, out int32, name string, status int32) uint64 {
	t.Helper()
	if status != XqdStatusOK {
		t.Fatalf("%s status = %d, want %d", name, status, XqdStatusOK)
	}
	return i.memory.Uint64(int64(out))
}

func cacheUserMetadata(t *testing.T, i *Instance, handle int32) string {
	t.Helper()
	if status := i.xqd_cache_get_user_metadata(handle, replaceTestMetadataOut, 64, replaceTestNwrittenOut); status != XqdStatusOK {
		t.Fatalf("get_user_metadata status = %d, want %d", status, XqdStatusOK)
	}
	metadata := make([]byte, i.memory.Uint32(replaceTestNwrittenOut))
	_, _ = i.memory.ReadAt(metadata, replaceTestMetadataOut)
	return string(metadata)
}

func checkDuration(t *testing.T, name string, got uint64, low, high time.Duration) {
	t.Helper()
	if got < uint64(low) || got > uint64(high) {
		t.Errorf("%s = %v, want between %v and %v", name, time.Duration(got), low, high)
	}
}

// atCacheClock is the instant ms milliseconds after CacheD's clock started.
func atCacheClock(ms float64) time.Time {
	return cacheClockBase.Add(time.Duration(ms * float64(time.Millisecond)))
}

func TestCacheAgeFollowsCacheDClock(t *testing.T) {
	obj := &CachedObject{InsertTime: atCacheClock(10.9), InitialAgeNs: uint64(5 * time.Millisecond)}
	tests := []struct {
		name string
		at   float64
		want time.Duration
	}{
		{"same millisecond", 10.95, 5 * time.Millisecond},
		{"next millisecond", 11.1, 6 * time.Millisecond},
		{"lookup that started before the insert", 7.5, 2 * time.Millisecond},
		{"lookup that started long before the insert", 1, 0},
	}
	for _, tt := range tests {
		if age := obj.ageAt(atCacheClock(tt.at)); age != uint64(tt.want) {
			t.Errorf("%s: age = %v, want %v", tt.name, time.Duration(age), tt.want)
		}
	}

	if got := cachedClock(atCacheClock(-0.5)); got != -time.Millisecond {
		t.Errorf("clock half a millisecond before its start = %v, want -1ms", got)
	}
	obj.InitialAgeNs = math.MaxUint64 - 1
	if age := obj.ageAt(atCacheClock(20)); age != math.MaxUint64 {
		t.Errorf("age = %d, want it to saturate", age)
	}
}

func TestCacheMetadataOfASoftPurgedObject(t *testing.T) {
	obj := NewCache().Insert([]byte("key"), &CacheWriteOptions{MaxAgeNs: uint64(time.Minute), InitialAgeNs: nanoseconds(5 * time.Second)})
	obj.InsertTime = atCacheClock(1000)
	obj.softPurgedAt.Store(cacheInstant(atCacheClock(3000)))

	m := obj.metadataAt(atCacheClock(4000), 3)
	if m.AgeNs != uint64(8*time.Second) || m.MaxAgeNs != uint64(time.Minute) || m.Hits != 3 {
		t.Fatalf("metadata = %+v, want an age of 8s, a max age of 1m and 3 hits", m)
	}
	// The object went stale when it was purged, at the age of 7s.
	if m.staleAtNs != uint64(7*time.Second) {
		t.Fatalf("stale at %v, want 7s", time.Duration(m.staleAtNs))
	}
}

func TestHttpCachePeriodComparesTheFrozenAgeInclusively(t *testing.T) {
	entry := &CacheEntry{Object: &CachedObject{}, Metadata: ObjectMetadata{
		staleAtNs:              uint64(time.Minute),
		StaleWhileRevalidateNs: uint64(10 * time.Second),
		StaleIfErrorNs:         uint64(30 * time.Second),
	}}
	tests := []struct {
		age  time.Duration
		want cachePeriod
	}{
		{time.Minute, periodFresh},
		{time.Minute + 1, periodStale},
		{70 * time.Second, periodStale},
		{70*time.Second + 1, periodStaleIfError},
		{90 * time.Second, periodStaleIfError},
		{90*time.Second + 1, periodExpired},
	}
	for _, tt := range tests {
		entry.Metadata.AgeNs = uint64(tt.age)
		if got := entry.httpPeriod(); got != tt.want {
			t.Errorf("age %v: period = %d, want %d", tt.age, got, tt.want)
		}
	}

	if got := (&CacheEntry{}).httpPeriod(); got != periodExpired {
		t.Errorf("period without an object = %d, want expired", got)
	}
}

func TestCacheHandleKeepsWhatItsLookupReported(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", freshObject)
	first := plainLookup(t, i, "key")
	onlyObject(t, i, "key").InsertTime = time.Now().Add(-10 * time.Second)
	second := plainLookup(t, i, "key")

	checkDuration(t, "first age", accessorValue(t, i, replaceTestValueOut, "get_age_ns", i.xqd_cache_get_age_ns(first, replaceTestValueOut)), 0, time.Second)
	checkDuration(t, "second age", accessorValue(t, i, replaceTestValueOut, "get_age_ns", i.xqd_cache_get_age_ns(second, replaceTestValueOut)), 10*time.Second, 11*time.Second)
	if hits := accessorValue(t, i, replaceTestValueOut, "get_hits", i.xqd_cache_get_hits(first, replaceTestValueOut)); hits != 1 {
		t.Errorf("first hits = %d, want 1", hits)
	}
	if hits := accessorValue(t, i, replaceTestValueOut, "get_hits", i.xqd_cache_get_hits(second, replaceTestValueOut)); hits != 2 {
		t.Errorf("second hits = %d, want 2", hits)
	}
}

func TestCacheLengthIsWhatTheLookupKnew(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", replaceTestObject{content: "0123456789", maxAge: time.Minute})
	insertBody := int32(i.memory.Uint32(replaceTestHandleOut))
	streaming := plainLookup(t, i, "key")
	closeBody(t, i, insertBody)
	complete := plainLookup(t, i, "key")

	if status := i.xqd_cache_get_length(streaming, replaceTestValueOut); status != XqdErrNone {
		t.Errorf("length of an object found while streaming: status = %d, want %d", status, XqdErrNone)
	}
	if length := accessorValue(t, i, replaceTestValueOut, "get_length", i.xqd_cache_get_length(complete, replaceTestValueOut)); length != 10 {
		t.Errorf("length of a complete object = %d, want 10", length)
	}

	// Without a length known at lookup, a range falls back to the whole body.
	for name, tt := range map[string]struct {
		handle int32
		want   string
	}{
		"streaming": {streaming, "0123456789"},
		"complete":  {complete, "23456789"},
	} {
		i.memory.WriteUint64(replaceTestBodyOptsPtr, 2)
		if status := i.xqd_cache_get_body(tt.handle, CacheGetBodyOptionsMaskFrom, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
			t.Fatalf("%s: get_body status = %d", name, status)
		}
		if got := readTestBody(t, i, int(i.memory.Uint32(replaceTestHandleOut))); got != tt.want {
			t.Errorf("%s: range from 2 = %q, want %q", name, got, tt.want)
		}
	}
}

func TestCacheUpdateOnlyReachesLaterLookups(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", staleObject)
	leader := transactionLookup(t, i, "key")
	earlier := plainLookup(t, i, "key")

	mask := writeCacheWriteOptions(i, replaceTestObject{maxAge: time.Hour, initial: 30 * time.Second, metadata: "new"})
	if status := i.xqd_cache_transaction_update(leader, mask, replaceTestWriteOptsPtr); status != XqdStatusOK {
		t.Fatalf("transaction_update status = %d", status)
	}
	later := plainLookup(t, i, "key")

	tests := []struct {
		name            string
		handle          int32
		state           uint32
		maxAge          time.Duration
		ageLow, ageHigh time.Duration
		metadata        string
	}{
		{"earlier", earlier, staleState, time.Minute, 90 * time.Second, 91 * time.Second, "meta"},
		// The update keeps the initial age it was given.
		{"later", later, foundState, time.Hour, 30 * time.Second, 31 * time.Second, "new"},
	}
	for _, tt := range tests {
		if state := handleState(t, i, tt.handle); state != tt.state {
			t.Errorf("%s: state = %#x, want %#x", tt.name, state, tt.state)
		}
		if maxAge := accessorValue(t, i, replaceTestValueOut, "get_max_age_ns", i.xqd_cache_get_max_age_ns(tt.handle, replaceTestValueOut)); maxAge != uint64(tt.maxAge) {
			t.Errorf("%s: max age = %v, want %v", tt.name, time.Duration(maxAge), tt.maxAge)
		}
		checkDuration(t, tt.name+" age", accessorValue(t, i, replaceTestValueOut, "get_age_ns", i.xqd_cache_get_age_ns(tt.handle, replaceTestValueOut)), tt.ageLow, tt.ageHigh)
		if metadata := cacheUserMetadata(t, i, tt.handle); metadata != tt.metadata {
			t.Errorf("%s: user metadata = %q, want %q", tt.name, metadata, tt.metadata)
		}
	}
}

func TestCacheUpdateResetsTheOptionsItLeavesOut(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", staleObject)
	leader := transactionLookup(t, i, "key")
	mask := writeCacheWriteOptions(i, replaceTestObject{maxAge: time.Hour})
	if status := i.xqd_cache_transaction_update(leader, mask, replaceTestWriteOptsPtr); status != XqdStatusOK {
		t.Fatalf("transaction_update status = %d", status)
	}

	later := plainLookup(t, i, "key")
	checkDuration(t, "age", accessorValue(t, i, replaceTestValueOut, "get_age_ns", i.xqd_cache_get_age_ns(later, replaceTestValueOut)), 0, time.Second)
	if swr := accessorValue(t, i, replaceTestValueOut, "get_stale_while_revalidate_ns", i.xqd_cache_get_stale_while_revalidate_ns(later, replaceTestValueOut)); swr != 0 {
		t.Errorf("stale-while-revalidate = %v, want 0", time.Duration(swr))
	}
	if metadata := cacheUserMetadata(t, i, later); metadata != "" {
		t.Errorf("user metadata = %q, want none", metadata)
	}
}

func TestCacheWaiterAgeCountsFromTheStartOfItsLookup(t *testing.T) {
	a := newCacheReplaceTestInstance()
	b := sharedCacheInstance(a)
	leader := transactionLookup(t, a, "key")
	keyPtr, keyLen := writeCacheKey(b, "key")
	waited := startWaiting(t, func() int32 {
		return b.xqd_cache_transaction_lookup(keyPtr, keyLen, 0, 0, replaceTestHandleOut)
	})

	mask := writeCacheWriteOptions(a, replaceTestObject{maxAge: time.Minute, initial: 10 * time.Millisecond})
	if status := a.xqd_cache_transaction_insert(leader, mask, replaceTestWriteOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("transaction_insert status = %d", status)
	}
	closeBody(t, a, int32(a.memory.Uint32(replaceTestHandleOut)))
	if status := receiveWithin(t, waited); status != XqdStatusOK {
		t.Fatalf("waiting transaction_lookup status = %d", status)
	}

	// The waiter started before the insert by more than the initial age.
	waiter := int32(b.memory.Uint32(replaceTestHandleOut))
	if age := accessorValue(t, b, replaceTestValueOut, "get_age_ns", b.xqd_cache_get_age_ns(waiter, replaceTestValueOut)); age != 0 {
		t.Fatalf("age = %v, want 0", time.Duration(age))
	}
}

func TestCacheWaiterJudgesTheObjectWhenItWakesUp(t *testing.T) {
	cache := NewCache()
	key := []byte("key")
	leader := cache.TransactionLookup(key, nil, "leader")
	waited := startWaiting(t, func() *CacheTransaction { return cache.TransactionLookup(key, nil, "waiter") })
	transactionInsertFinished(t, cache, leader, &CacheWriteOptions{})

	// The object had no life left when the waiter looked again.
	if entry := receiveWithin(t, waited).Entry; entry.State != (CacheState{MustInsertOrUpdate: true}) {
		t.Fatalf("state = %+v, want only the obligation", entry.State)
	}
}

func TestCacheFailedWriteHasNoLength(t *testing.T) {
	obj := NewCache().Insert([]byte("key"), &CacheWriteOptions{})
	_, _ = obj.WriteBody([]byte("partial"))
	obj.FinishWrite()
	obj.AbortWrite()
	if length, known := obj.knownLength(); known {
		t.Fatalf("length = %d after the write failed, want none", length)
	}

	obj.FinishWrite()
	if length, known := obj.knownLength(); known {
		t.Fatalf("length = %d after finishing a failed write, want none", length)
	}
}

func TestHttpCacheWaiterSeesAZeroMaxAgeResponseAsFresh(t *testing.T) {
	cache := NewCache()
	key := []byte("key")
	leader := cache.TransactionLookup(key, nil, "leader")
	waited := startWaiting(t, func() *CacheTransaction { return cache.TransactionLookup(key, nil, "waiter") })
	transactionInsertFinished(t, cache, leader, &CacheWriteOptions{StaleIfErrorNs: nanoseconds(time.Minute)})

	// CacheD finds it stale, but its age counts from before the insert.
	entry := receiveWithin(t, waited).Entry
	if !entry.State.Found || !entry.State.Stale || entry.Metadata.AgeNs != 0 {
		t.Fatalf("state = %+v, age = %v, want a stale object of age 0", entry.State, time.Duration(entry.Metadata.AgeNs))
	}
	if period := entry.httpPeriod(); period != periodFresh {
		t.Fatalf("HTTP period = %d, want fresh", period)
	}
}

func TestHttpCacheStaleWhileRevalidateLookupDoesNotWait(t *testing.T) {
	a := newHTTPCacheStoreTestInstance()
	b := sharedHTTPCacheInstance(a)
	storeHTTPObject(t, a, nil, httpStaleObject, "stale")
	leader := httpCacheTransactionLookup(t, a, httpCacheTestRequest(a, http.MethodGet, nil))
	if state := httpLookupState(t, a, leader); state != staleState|CacheLookupStateMustInsertOrUpdate {
		t.Fatalf("leader state = %#x, want the revalidation", state)
	}

	req := httpCacheTestRequest(b, http.MethodGet, nil)
	looked := make(chan int32, 1)
	go func() { looked <- b.xqd_http_cache_transaction_lookup(req, 0, 0, httpCacheTestHandleOut) }()
	if status := receiveWithin(t, looked); status != XqdStatusOK {
		t.Fatalf("transaction_lookup status = %d", status)
	}
	if state := httpLookupState(t, b, int32(b.memory.Uint32(httpCacheTestHandleOut))); state != staleState {
		t.Fatalf("state = %#x, want %#x", state, staleState)
	}
}

func TestCacheReplaceKeepsWhatItFound(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", freshObject)
	handle := beginReplace(t, i, "key", CacheReplaceImmediate)
	onlyObject(t, i, "key").InsertTime = time.Now().Add(-time.Hour)

	if status := i.xqd_cache_replace_get_state(handle, replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("replace_get_state status = %d", status)
	}
	if state := i.memory.Uint32(replaceTestValueOut); state != foundState {
		t.Errorf("state = %#x, want %#x", state, foundState)
	}
	checkDuration(t, "age", accessorValue(t, i, replaceTestValueOut, "replace_get_age_ns", i.xqd_cache_replace_get_age_ns(handle, replaceTestValueOut)), 0, time.Second)
}

func TestCacheReplaceThatWaitedJudgesTheObjectAtItsStart(t *testing.T) {
	a := newCacheReplaceTestInstance()
	b := sharedCacheInstance(a)
	leader := transactionLookup(t, a, "key")

	keyPtr, keyLen := writeCacheKey(b, "key")
	writeReplaceStrategy(b, CacheReplaceWait)
	waited := startWaiting(t, func() int32 {
		return b.xqd_cache_replace(keyPtr, keyLen, CacheReplaceOptionsMaskReplaceStrategy, replaceTestReplaceOpts, replaceTestHandleOut)
	})
	mask := writeCacheWriteOptions(a, replaceTestObject{maxAge: 0})
	if status := a.xqd_cache_transaction_insert(leader, mask, replaceTestWriteOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("transaction_insert status = %d", status)
	}
	closeBody(t, a, int32(a.memory.Uint32(replaceTestHandleOut)))
	if status := receiveWithin(t, waited); status != XqdStatusOK {
		t.Fatalf("replace status = %d", status)
	}

	// Before the object existed, even a max age of 0 is fresh.
	handle := int32(b.memory.Uint32(replaceTestHandleOut))
	if status := b.xqd_cache_replace_get_state(handle, replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("replace_get_state status = %d", status)
	}
	if state := b.memory.Uint32(replaceTestValueOut); state != foundState {
		t.Errorf("state = %#x, want %#x", state, foundState)
	}
}

func TestCacheStreamBackReportsTheObjectAsCacheDSeesIt(t *testing.T) {
	tests := []struct {
		name   string
		maxAge time.Duration
		want   uint32
	}{
		{"fresh", time.Minute, foundState},
		// CacheD calls an object stale once its age reaches its max age.
		{"max age of zero", 0, staleState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := newCacheReplaceTestInstance()
			handle := transactionLookup(t, i, "key")
			mask := writeCacheWriteOptions(i, replaceTestObject{maxAge: tt.maxAge})
			if status := i.xqd_cache_transaction_insert_and_stream_back(handle, mask, replaceTestWriteOptsPtr, replaceTestHandleOut, replaceTestValueOut); status != XqdStatusOK {
				t.Fatalf("insert_and_stream_back status = %d", status)
			}
			readback := int32(i.memory.Uint32(replaceTestValueOut))
			if state := handleState(t, i, readback); state != tt.want {
				t.Errorf("state = %#x, want %#x", state, tt.want)
			}
			// Attaching to the insert is not a hit.
			if hits := accessorValue(t, i, replaceTestValueOut, "get_hits", i.xqd_cache_get_hits(readback, replaceTestValueOut)); hits != 0 {
				t.Errorf("hits = %d, want 0", hits)
			}
		})
	}
}

func TestHttpCacheStreamBackReportsItsPeriod(t *testing.T) {
	tests := []struct {
		name      string
		freshness httpFreshness
		want      uint32
	}{
		// The age equals the max age within the millisecond of the insert.
		{"max age of zero", httpFreshness{}, foundState},
		{"stale", httpStaleObject, foundState | CacheLookupStateStale},
		{"stale-if-error", httpStaleIfErrorObject, foundState | usableIfErrorState},
		{"expired", httpExpiredObject, foundState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst, handle := newHTTPCacheTransaction(t)
			writeHTTPFreshness(inst, tt.freshness)
			_, readback := insertAndStreamBack(t, inst, handle, httpCacheTestResponse(inst, http.StatusOK, http.Header{}), httpTestObjectWriteMask)
			if state := httpLookupState(t, inst, readback); state != tt.want {
				t.Errorf("state = %#x, want %#x", state, tt.want)
			}
		})
	}
}

func TestHttpCacheReturnFreshCountsItsAgeFromTheLookup(t *testing.T) {
	tests := []struct {
		name      string
		freshness httpFreshness
		ageLow    time.Duration
		ageHigh   time.Duration
		state     uint32
	}{
		{"initial age shorter than the wait", httpFreshness{maxAge: time.Minute, initialAge: 10 * time.Millisecond}, 0, 0, foundState},
		{"initial age past the max age", httpFreshness{maxAge: time.Minute, initialAge: 2 * time.Minute, swr: 2 * time.Minute}, 2*time.Minute - time.Second, 2*time.Minute - 19*time.Millisecond, foundState | CacheLookupStateStale},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := newHTTPCacheStoreTestInstance()
			storeHTTPObject(t, inst, nil, httpExpiredObject, "body")
			handle := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
			time.Sleep(20 * time.Millisecond)

			writeHTTPFreshness(inst, tt.freshness)
			resp := httpCacheTestResponse(inst, http.StatusOK, http.Header{})
			if status := inst.xqd_http_cache_transaction_update_and_return_fresh(handle, resp, httpTestObjectWriteMask, httpCacheTestOptions, httpCacheTestHandleOut); status != XqdStatusOK {
				t.Fatalf("update_and_return_fresh status = %d", status)
			}
			fresh := int32(inst.memory.Uint32(httpCacheTestHandleOut))

			checkDuration(t, "age", accessorValue(t, inst, httpCacheTestSecondOut, "get_age_ns", inst.xqd_http_cache_get_age_ns(fresh, httpCacheTestSecondOut)), tt.ageLow, tt.ageHigh)
			if hits := accessorValue(t, inst, httpCacheTestSecondOut, "get_hits", inst.xqd_http_cache_get_hits(fresh, httpCacheTestSecondOut)); hits != 1 {
				t.Errorf("hits = %d, want 1", hits)
			}
			if state := httpLookupState(t, inst, fresh); state != tt.state {
				t.Errorf("state = %#x, want %#x", state, tt.state)
			}
		})
	}
}

func TestHttpCacheHandleKeepsWhatItsLookupReported(t *testing.T) {
	inst := newHTTPCacheStoreTestInstance()
	storeHTTPObject(t, inst, nil, httpFreshObject, "body")
	first := httpCachePlainLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
	onlyHTTPObject(t, inst).InsertTime = time.Now().Add(-10 * time.Second)
	second := httpCachePlainLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))

	checkDuration(t, "first age", accessorValue(t, inst, httpCacheTestSecondOut, "get_age_ns", inst.xqd_http_cache_get_age_ns(first, httpCacheTestSecondOut)), 0, time.Second)
	checkDuration(t, "second age", accessorValue(t, inst, httpCacheTestSecondOut, "get_age_ns", inst.xqd_http_cache_get_age_ns(second, httpCacheTestSecondOut)), 10*time.Second, 11*time.Second)
	if hits := accessorValue(t, inst, httpCacheTestSecondOut, "get_hits", inst.xqd_http_cache_get_hits(first, httpCacheTestSecondOut)); hits != 1 {
		t.Errorf("first hits = %d, want 1", hits)
	}
	if length := accessorValue(t, inst, httpCacheTestSecondOut, "get_length", inst.xqd_http_cache_get_length(first, httpCacheTestSecondOut)); length != 4 {
		t.Errorf("length = %d, want 4", length)
	}
}

func onlyHTTPObject(t *testing.T, inst *Instance) *CachedObject {
	t.Helper()
	var found []*CachedObject
	for _, variants := range inst.cache.objects {
		found = append(found, variants...)
	}
	if len(found) != 1 {
		t.Fatalf("%d objects stored, want 1", len(found))
	}
	return found[0]
}

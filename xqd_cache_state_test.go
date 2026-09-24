package fastlike

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	foundState = CacheLookupStateFound | CacheLookupStateUsable
	staleState = foundState | CacheLookupStateStale
)

var (
	freshObject   = replaceTestObject{content: "fresh", maxAge: time.Minute, metadata: "meta", length: true, finish: true}
	staleObject   = replaceTestObject{content: "stale", maxAge: time.Minute, initial: 90 * time.Second, swr: time.Minute, metadata: "meta", length: true, finish: true}
	expiredObject = replaceTestObject{content: "expired", maxAge: time.Minute, initial: 3 * time.Minute, swr: time.Minute, metadata: "meta", length: true, finish: true}
)

func transactionLookup(t *testing.T, i *Instance, key string) int32 {
	t.Helper()
	keyPtr, keyLen := writeCacheKey(i, key)
	if status := i.xqd_cache_transaction_lookup(keyPtr, keyLen, 0, 0, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("cache_transaction_lookup status = %d, want %d", status, XqdStatusOK)
	}
	return int32(i.memory.Uint32(replaceTestHandleOut))
}

func plainLookup(t *testing.T, i *Instance, key string) int32 {
	t.Helper()
	keyPtr, keyLen := writeCacheKey(i, key)
	if status := i.xqd_cache_lookup(keyPtr, keyLen, 0, 0, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("cache_lookup status = %d, want %d", status, XqdStatusOK)
	}
	return int32(i.memory.Uint32(replaceTestHandleOut))
}

func handleState(t *testing.T, i *Instance, handle int32) uint32 {
	t.Helper()
	if status := i.xqd_cache_get_state(handle, replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("cache_get_state status = %d, want %d", status, XqdStatusOK)
	}
	return i.memory.Uint32(replaceTestValueOut)
}

func nanoseconds(d time.Duration) *uint64 {
	ns := uint64(d)
	return &ns
}

func sharedCacheInstance(i *Instance) *Instance {
	other := newCacheReplaceTestInstance()
	other.cache = i.cache
	return other
}

func onlyObject(t *testing.T, i *Instance, key string) *CachedObject {
	t.Helper()
	variants := i.cache.objects[cacheKey([]byte(key))]
	if len(variants) != 1 {
		t.Fatalf("%d objects stored under %q, want 1", len(variants), key)
	}
	return variants[0]
}

func checkStatuses(t *testing.T, calls map[string]func() int32, want int32) {
	t.Helper()
	for name, call := range calls {
		if status := call(); status != want {
			t.Errorf("%s status = %d, want %d", name, status, want)
		}
	}
}

// checkNotFoundAccessors checks the accessors that need a found object.
func checkNotFoundAccessors(t *testing.T, i *Instance, handle int32) {
	t.Helper()
	accessors := map[string]func() int32{
		"get_body":   func() int32 { return i.xqd_cache_get_body(handle, 0, 0, replaceTestValueOut) },
		"get_length": func() int32 { return i.xqd_cache_get_length(handle, replaceTestValueOut) },
		"get_max_age_ns": func() int32 {
			return i.xqd_cache_get_max_age_ns(handle, replaceTestValueOut)
		},
		"get_stale_while_revalidate_ns": func() int32 {
			return i.xqd_cache_get_stale_while_revalidate_ns(handle, replaceTestValueOut)
		},
		"get_age_ns": func() int32 { return i.xqd_cache_get_age_ns(handle, replaceTestValueOut) },
		"get_hits":   func() int32 { return i.xqd_cache_get_hits(handle, replaceTestValueOut) },
	}
	checkStatuses(t, accessors, XqdErrNone)
}

func TestCacheLookupOnlyFindsUsableObjects(t *testing.T) {
	tests := []struct {
		name string
		obj  replaceTestObject
		want uint32
	}{
		{"fresh", freshObject, foundState},
		{"within stale-while-revalidate", staleObject, staleState},
		{"past stale-while-revalidate", expiredObject, 0},
		{"stale without stale-while-revalidate", replaceTestObject{content: "x", maxAge: time.Minute, initial: 2 * time.Minute, finish: true}, 0},
		{"zero max age", replaceTestObject{content: "x", finish: true}, 0},
		{"zero max age with stale-while-revalidate", replaceTestObject{content: "x", swr: time.Minute, finish: true}, staleState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := newCacheReplaceTestInstance()
			insertTestObject(t, i, "key", tt.obj)
			handle := plainLookup(t, i, "key")
			if state := handleState(t, i, handle); state != tt.want {
				t.Fatalf("state = %#x, want %#x", state, tt.want)
			}
			if tt.want != 0 {
				return
			}
			checkNotFoundAccessors(t, i, handle)
			if status := i.xqd_cache_get_user_metadata(handle, replaceTestMetadataOut, 64, replaceTestNwrittenOut); status != XqdErrNone {
				t.Fatalf("get_user_metadata status = %d, want %d", status, XqdErrNone)
			}
		})
	}
}

func TestCacheTransactionLookupOfExpiredObject(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", expiredObject)

	handle := transactionLookup(t, i, "key")
	if state := handleState(t, i, handle); state != CacheLookupStateMustInsertOrUpdate {
		t.Fatalf("state = %#x, want only must-insert-or-update", state)
	}
	checkNotFoundAccessors(t, i, handle)

	// The metadata of the expired object is still there to revalidate it.
	if status := i.xqd_cache_get_user_metadata(handle, replaceTestMetadataOut, 64, replaceTestNwrittenOut); status != XqdStatusOK {
		t.Fatalf("get_user_metadata status = %d, want %d", status, XqdStatusOK)
	}
	metadata := make([]byte, i.memory.Uint32(replaceTestNwrittenOut))
	_, _ = i.memory.ReadAt(metadata, replaceTestMetadataOut)
	if string(metadata) != "meta" {
		t.Fatalf("user metadata = %q, want %q", metadata, "meta")
	}

	// Lookups that found nothing did not count as hits.
	plainLookup(t, i, "key")
	replace := beginReplace(t, i, "key", CacheReplaceImmediate)
	if status := i.xqd_cache_replace_get_hits(replace, replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("replace_get_hits status = %d, want %d", status, XqdStatusOK)
	}
	if hits := i.memory.ReadUint64(replaceTestValueOut); hits != 0 {
		t.Fatalf("hits = %d, want 0", hits)
	}
}

func TestCacheLookupOfMissingKeyHasNoMetadata(t *testing.T) {
	i := newCacheReplaceTestInstance()
	for name, handle := range map[string]int32{
		"lookup":             plainLookup(t, i, "missing"),
		"transaction_lookup": transactionLookup(t, i, "other"),
	} {
		if status := i.xqd_cache_get_user_metadata(handle, replaceTestMetadataOut, 64, replaceTestNwrittenOut); status != XqdErrNone {
			t.Errorf("%s: get_user_metadata status = %d, want %d", name, status, XqdErrNone)
		}
	}
}

func TestCacheStaleObjectIsRevalidatedOneLookupAtATime(t *testing.T) {
	a := newCacheReplaceTestInstance()
	b := sharedCacheInstance(a)
	insertTestObject(t, a, "key", staleObject)

	leader := transactionLookup(t, a, "key")
	if state := handleState(t, a, leader); state != staleState|CacheLookupStateMustInsertOrUpdate {
		t.Fatalf("first lookup state = %#x, want %#x", state, staleState|CacheLookupStateMustInsertOrUpdate)
	}
	if state := handleState(t, b, transactionLookup(t, b, "key")); state != staleState {
		t.Fatalf("lookup of another guest during the revalidation: state = %#x, want %#x", state, staleState)
	}
	if again := transactionLookup(t, a, "key"); again == leader || handleState(t, a, again) != staleState {
		t.Fatal("a second lookup of the revalidating guest did not get a handle of its own without the obligation")
	}
	rewindRevalidation := func() {
		onlyObject(t, a, "key").lastRevalidation.Store(cacheInstant(time.Now().Add(-revalidationInterval)))
	}
	rewindRevalidation()
	if state := handleState(t, b, transactionLookup(t, b, "key")); state != staleState {
		t.Fatalf("lookup while the revalidation is pending: state = %#x, want %#x", state, staleState)
	}

	// Abandoning the revalidation does not hand it out again right away.
	if status := a.xqd_cache_transaction_cancel(leader); status != XqdStatusOK {
		t.Fatalf("transaction_cancel status = %d", status)
	}
	if state := handleState(t, b, transactionLookup(t, b, "key")); state != staleState {
		t.Fatalf("lookup right after the cancel: state = %#x, want %#x", state, staleState)
	}

	rewindRevalidation()
	if state := handleState(t, b, transactionLookup(t, b, "key")); state != staleState|CacheLookupStateMustInsertOrUpdate {
		t.Fatalf("lookup once the interval passed: state = %#x, want %#x", state, staleState|CacheLookupStateMustInsertOrUpdate)
	}
}

func TestCachePlainLookupUsesUpTheRevalidation(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", staleObject)

	if state := lookupState(t, i, "key"); state != staleState {
		t.Fatalf("plain lookup state = %#x, want %#x", state, staleState)
	}
	if state := handleState(t, i, transactionLookup(t, i, "key")); state != staleState {
		t.Fatalf("transaction lookup state = %#x, want %#x", state, staleState)
	}
}

func TestCacheStreamingStaleObjectIsNotRevalidated(t *testing.T) {
	i := newCacheReplaceTestInstance()
	obj := staleObject
	obj.finish = false
	insertTestObject(t, i, "key", obj)

	if state := lookupState(t, i, "key"); state != staleState {
		t.Fatalf("plain lookup state while streaming = %#x, want %#x", state, staleState)
	}
	if state := handleState(t, i, transactionLookup(t, i, "key")); state != staleState {
		t.Fatalf("state while streaming = %#x, want %#x", state, staleState)
	}
	onlyObject(t, i, "key").FinishWrite()
	if state := handleState(t, i, transactionLookup(t, i, "key")); state != staleState|CacheLookupStateMustInsertOrUpdate {
		t.Fatalf("state once written = %#x, want %#x", state, staleState|CacheLookupStateMustInsertOrUpdate)
	}
}

func TestCachePendingReplaceHoldsOffRevalidation(t *testing.T) {
	a := newCacheReplaceTestInstance()
	b := sharedCacheInstance(a)
	insertTestObject(t, a, "key", staleObject)

	beginReplace(t, a, "key", CacheReplaceImmediate)
	if state := handleState(t, b, transactionLookup(t, b, "key")); state&CacheLookupStateMustInsertOrUpdate != 0 {
		t.Fatal("a lookup was asked to revalidate while a replace was pending")
	}
}

func TestCacheFreshTransactionalHitDoesNotHoldTheKey(t *testing.T) {
	a := newCacheReplaceTestInstance()
	b := sharedCacheInstance(a)
	insertTestObject(t, a, "key", freshObject)

	if state := handleState(t, a, transactionLookup(t, a, "key")); state != foundState {
		t.Fatalf("state = %#x, want found and usable", state)
	}
	onlyObject(t, a, "key").InsertTime = time.Now().Add(-time.Hour)

	done := make(chan struct{})
	go func() {
		keyPtr, keyLen := writeCacheKey(b, "key")
		b.xqd_cache_transaction_lookup(keyPtr, keyLen, 0, 0, replaceTestHandleOut)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a lookup waited for a transaction that only found a fresh object")
	}
	if state := handleState(t, b, int32(b.memory.Uint32(replaceTestHandleOut))); state != CacheLookupStateMustInsertOrUpdate {
		t.Fatalf("state = %#x, want only must-insert-or-update", state)
	}
}

func TestCacheFoundObjectWithoutStaleWhileRevalidate(t *testing.T) {
	i := newCacheReplaceTestInstance()
	insertTestObject(t, i, "key", freshObject)

	i.memory.WriteUint64(replaceTestValueOut, 42)
	if status := i.xqd_cache_get_stale_while_revalidate_ns(plainLookup(t, i, "key"), replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("get_stale_while_revalidate_ns status = %d, want %d", status, XqdStatusOK)
	}
	if swr := i.memory.ReadUint64(replaceTestValueOut); swr != 0 {
		t.Fatalf("stale-while-revalidate = %d, want 0", swr)
	}
}

func TestCacheUserMetadataShortBufferReportsSize(t *testing.T) {
	i := newCacheReplaceTestInstance()
	obj := freshObject
	obj.metadata = "0123456789"
	insertTestObject(t, i, "key", obj)

	if status := i.xqd_cache_get_user_metadata(plainLookup(t, i, "key"), replaceTestMetadataOut, 4, replaceTestNwrittenOut); status != XqdErrBufferLength {
		t.Fatalf("get_user_metadata status = %d, want %d", status, XqdErrBufferLength)
	}
	if n := i.memory.Uint32(replaceTestNwrittenOut); n != 10 {
		t.Fatalf("nwritten = %d, want 10", n)
	}
}

func TestCacheChosenStaleObjectIsFound(t *testing.T) {
	cache := NewCache()
	key := []byte("key")
	cache.Insert(key, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute), InitialAgeNs: nanoseconds(90 * time.Second), StaleIfErrorNs: nanoseconds(time.Minute)}).FinishWrite()

	tx := cache.TransactionLookup(key, nil, "owner")
	if tx.Entry.State != (CacheState{MustInsertOrUpdate: true}) {
		t.Fatalf("state = %+v, want only must-insert-or-update", tx.Entry.State)
	}
	if !cache.TransactionChooseStale(tx) {
		t.Fatal("the stale object was not chosen")
	}
	if want := (CacheState{Found: true, Usable: true, Stale: true}); tx.Entry.State != want {
		t.Fatalf("state = %+v, want %+v", tx.Entry.State, want)
	}
}

func TestCacheReplaceReportsEveryUnfreshObjectAsStale(t *testing.T) {
	tests := []struct {
		name string
		obj  replaceTestObject
		want uint32
	}{
		{"fresh", freshObject, foundState},
		{"within stale-while-revalidate", staleObject, staleState},
		{"expired", expiredObject, staleState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := newCacheReplaceTestInstance()
			insertTestObject(t, i, "key", tt.obj)
			if status := i.xqd_cache_replace_get_state(beginReplace(t, i, "key", CacheReplaceImmediate), replaceTestValueOut); status != XqdStatusOK {
				t.Fatalf("replace_get_state status = %d", status)
			}
			if state := i.memory.Uint32(replaceTestValueOut); state != tt.want {
				t.Fatalf("state = %#x, want %#x", state, tt.want)
			}
		})
	}
}

func TestCacheSoftPurgeStartsTheStaleWindow(t *testing.T) {
	tests := []struct {
		name        string
		initialAge  time.Duration
		swr         time.Duration
		purgedSince time.Duration
		want        CacheState
	}{
		{"fresh with stale-while-revalidate", 0, time.Minute, 0, CacheState{Found: true, Usable: true, Stale: true}},
		{"fresh without stale-while-revalidate", 0, 0, 0, CacheState{}},
		{"purged longer ago than stale-while-revalidate", 0, time.Minute, 2 * time.Minute, CacheState{}},
		{"stale for longer than since the purge", 110 * time.Second, time.Minute, 30 * time.Second, CacheState{Found: true, Usable: true, Stale: true}},
		{"purged before it went stale", 70 * time.Second, time.Minute, 90 * time.Second, CacheState{}},
		{"stale past stale-while-revalidate before the purge", 3 * time.Minute, time.Minute, 0, CacheState{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := NewCache()
			key := []byte("key")
			obj := cache.Insert(key, &CacheWriteOptions{MaxAgeNs: uint64(time.Minute), InitialAgeNs: nanoseconds(tt.initialAge), StaleWhileRevalidateNs: nanoseconds(tt.swr), SurrogateKeys: []string{"tag"}})
			obj.FinishWrite()
			if purged := cache.SoftPurgeSurrogateKey("tag"); purged != 1 {
				t.Fatalf("soft purge reported %d objects, want 1", purged)
			}
			obj.softPurgedAt.Add(-int64(tt.purgedSince))

			if state := cache.Lookup(key, nil).State; state != tt.want {
				t.Fatalf("state = %+v, want %+v", state, tt.want)
			}
			if age := obj.GetAge(); age < uint64(tt.initialAge) || age > uint64(tt.initialAge+time.Minute) {
				t.Fatalf("age = %v after the purge, want about %v", time.Duration(age), tt.initialAge)
			}
		})
	}
}

func TestCacheSoftPurgeKeepsTheEarliestPurge(t *testing.T) {
	cache := NewCache()
	key := []byte("key")
	obj := cache.Insert(key, &CacheWriteOptions{MaxAgeNs: uint64(time.Hour), StaleWhileRevalidateNs: nanoseconds(time.Minute), SurrogateKeys: []string{"tag"}})
	obj.FinishWrite()

	cache.SoftPurgeSurrogateKey("tag")
	obj.softPurgedAt.Add(-int64(2 * time.Minute))
	cache.SoftPurgeSurrogateKey("tag")
	if state := cache.Lookup(key, nil).State; state.Found {
		t.Fatal("a second soft purge restarted the stale window")
	}
}

func TestCacheUpdateClearsSoftPurge(t *testing.T) {
	cache := NewCache()
	key := []byte("key")
	obj := cache.Insert(key, &CacheWriteOptions{MaxAgeNs: uint64(time.Hour), SurrogateKeys: []string{"tag"}})
	obj.FinishWrite()
	cache.SoftPurgeSurrogateKey("tag")

	tx := cache.TransactionLookup(key, nil, "owner")
	if tx.Entry.State != (CacheState{MustInsertOrUpdate: true}) || tx.Entry.Object != obj {
		t.Fatalf("lookup after the purge: state = %+v, want only must-insert-or-update on the purged object", tx.Entry.State)
	}
	if err := cache.TransactionUpdate(tx, &CacheWriteOptions{MaxAgeNs: uint64(time.Hour)}); err != nil {
		t.Fatalf("update: %v", err)
	}
	cache.CompleteTransaction(tx)
	if state := cache.Lookup(key, nil).State; state != (CacheState{Found: true, Usable: true}) {
		t.Fatalf("state after the update = %+v, want fresh", state)
	}
}

func TestCacheCancelGivesUpTheObligation(t *testing.T) {
	a := newCacheReplaceTestInstance()
	b := sharedCacheInstance(a)
	insertTestObject(t, a, "stale", staleObject)
	insertTestObject(t, a, "expired", expiredObject)

	leader := transactionLookup(t, a, "stale")
	if status := b.xqd_cache_transaction_cancel(transactionLookup(t, b, "stale")); status != XqdErrInvalidHandle {
		t.Fatalf("cancel without an obligation: status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := a.xqd_cache_transaction_cancel(leader); status != XqdStatusOK {
		t.Fatalf("cancel status = %d, want %d", status, XqdStatusOK)
	}
	if state := handleState(t, a, leader); state != staleState {
		t.Fatalf("state after the cancel = %#x, want %#x", state, staleState)
	}
	if status := a.xqd_cache_transaction_cancel(leader); status != XqdErrInvalidHandle {
		t.Fatalf("second cancel: status = %d, want %d", status, XqdErrInvalidHandle)
	}

	// The metadata of an expired object goes away with the obligation.
	miss := transactionLookup(t, a, "expired")
	if status := a.xqd_cache_transaction_cancel(miss); status != XqdStatusOK {
		t.Fatalf("cancel status = %d, want %d", status, XqdStatusOK)
	}
	if state := handleState(t, a, miss); state != 0 {
		t.Fatalf("state after the cancel = %#x, want 0", state)
	}
	if status := a.xqd_cache_get_user_metadata(miss, replaceTestMetadataOut, 64, replaceTestNwrittenOut); status != XqdErrNone {
		t.Fatalf("get_user_metadata after the cancel: status = %d, want %d", status, XqdErrNone)
	}
}

func TestHttpCacheAccessorsWithoutObject(t *testing.T) {
	inst := newHTTPCacheStoreTestInstance()
	inst.memory.WriteUint64(httpCacheTestOptions, 0)
	storeObject(t, inst, http.StatusOK, nil, "body")

	handle := httpCachePlainLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
	accessors := map[string]func() int32{
		"get_length":     func() int32 { return inst.xqd_http_cache_get_length(handle, httpCacheTestSecondOut) },
		"get_max_age_ns": func() int32 { return inst.xqd_http_cache_get_max_age_ns(handle, httpCacheTestSecondOut) },
		"get_stale_while_revalidate_ns": func() int32 {
			return inst.xqd_http_cache_get_stale_while_revalidate_ns(handle, httpCacheTestSecondOut)
		},
		"get_stale_if_error_ns": func() int32 { return inst.xqd_http_cache_get_stale_if_error_ns(handle, httpCacheTestSecondOut) },
		"get_age_ns":            func() int32 { return inst.xqd_http_cache_get_age_ns(handle, httpCacheTestSecondOut) },
		"get_hits":              func() int32 { return inst.xqd_http_cache_get_hits(handle, httpCacheTestSecondOut) },
		"get_sensitive_data":    func() int32 { return inst.xqd_http_cache_get_sensitive_data(handle, httpCacheTestSecondOut) },
		"get_surrogate_keys": func() int32 {
			return inst.xqd_http_cache_get_surrogate_keys(handle, httpCacheTestSecondOut, 64, httpCacheTestHandleOut)
		},
		"get_vary_rule": func() int32 {
			return inst.xqd_http_cache_get_vary_rule(handle, httpCacheTestSecondOut, 64, httpCacheTestHandleOut)
		},
	}
	checkStatuses(t, accessors, XqdErrNone)
}

func TestHttpCacheAbandonNeedsAnObligation(t *testing.T) {
	inst, leader := newHTTPCacheTransaction(t)
	if status := inst.xqd_http_cache_transaction_abandon(leader); status != XqdStatusOK {
		t.Fatalf("abandon status = %d, want %d", status, XqdStatusOK)
	}
	if status := inst.xqd_http_cache_transaction_abandon(leader); status != XqdErrInvalidHandle {
		t.Fatalf("second abandon: status = %d, want %d", status, XqdErrInvalidHandle)
	}
}

func TestCacheRevalidationIsOfferedOnceToConcurrentLookups(t *testing.T) {
	now := time.Now()
	for range 100 {
		obj := NewCache().Insert([]byte("key"), &CacheWriteOptions{})
		obj.FinishWrite()
		var offers atomic.Int32
		start := make(chan struct{})
		var done sync.WaitGroup
		done.Add(64)
		for range 64 {
			go func() {
				defer done.Done()
				<-start
				if obj.offerRevalidation(now) {
					offers.Add(1)
				}
			}()
		}
		close(start)
		done.Wait()
		if n := offers.Load(); n != 1 {
			t.Fatalf("%d concurrent lookups got the revalidation offer, want 1", n)
		}
	}
}

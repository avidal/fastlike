package fastlike

import (
	"net/http"
	"testing"
	"time"
)

const (
	usableIfErrorState      = CacheLookupStateStale | CacheLookupStateUsableIfError
	httpTestObjectETag      = `"v1"`
	httpTestObjectWriteMask = HttpCacheWriteOptionsMaskInitialAgeNs | HttpCacheWriteOptionsMaskStaleWhileRevalidateNs |
		HttpCacheWriteOptionsMaskStaleIfErrorNs
)

type httpFreshness struct {
	maxAge, initialAge, swr, sie time.Duration
}

var (
	httpFreshObject        = httpFreshness{maxAge: time.Minute}
	httpStaleObject        = httpFreshness{maxAge: time.Minute, initialAge: 90 * time.Second, swr: time.Minute}
	httpStaleIfErrorObject = httpFreshness{maxAge: time.Minute, initialAge: 90 * time.Second, sie: time.Minute}
	httpExpiredObject      = httpFreshness{maxAge: time.Minute, initialAge: 3 * time.Minute, swr: time.Minute, sie: time.Minute}
)

// writeHTTPFreshness writes f to the test write options, which
// httpTestObjectWriteMask describes.
func writeHTTPFreshness(inst *Instance, f httpFreshness) {
	inst.memory.WriteUint64(httpCacheTestOptions+httpCacheOptionsMaxAgeNs, uint64(f.maxAge))
	inst.memory.WriteUint64(httpCacheTestOptions+httpCacheOptionsInitialAgeNs, uint64(f.initialAge))
	inst.memory.WriteUint64(httpCacheTestOptions+httpCacheOptionsStaleWhileRevalidateNs, uint64(f.swr))
	inst.memory.WriteUint64(httpCacheTestOptions+httpCacheOptionsStaleIfErrorNs, uint64(f.sie))
}

// insertHTTPObject stores a complete 200 response through cacheHandle.
func insertHTTPObject(t *testing.T, inst *Instance, cacheHandle int32, header http.Header, f httpFreshness, body string) {
	t.Helper()
	if header == nil {
		header = http.Header{}
	}
	writeHTTPFreshness(inst, f)
	resp := httpCacheTestResponse(inst, http.StatusOK, header)
	if status := inst.xqd_http_cache_transaction_insert(cacheHandle, resp, httpTestObjectWriteMask, httpCacheTestOptions, httpCacheTestHandleOut); status != XqdStatusOK {
		t.Fatalf("transaction_insert status = %d", status)
	}
	insertBody := int32(inst.memory.Uint32(httpCacheTestHandleOut))
	_, _ = inst.memory.WriteAt([]byte(body), httpCacheTestDataPtr)
	if status := inst.xqd_body_write(insertBody, httpCacheTestDataPtr, int32(len(body)), BodyWriteEndBack, httpCacheTestNwrittenOut); status != XqdStatusOK {
		t.Fatalf("body_write status = %d", status)
	}
	closeBody(t, inst, insertBody)
}

func storeHTTPObject(t *testing.T, inst *Instance, header http.Header, f httpFreshness, body string) {
	t.Helper()
	insertHTTPObject(t, inst, httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil)), header, f, body)
}

// staleIfErrorWriteOptions describes an object in its stale-if-error period.
func staleIfErrorWriteOptions() *CacheWriteOptions {
	return &CacheWriteOptions{MaxAgeNs: uint64(time.Minute), InitialAgeNs: nanoseconds(90 * time.Second), StaleIfErrorNs: nanoseconds(time.Minute)}
}

// startWaiting runs lookup in the background and checks that it blocks.
func startWaiting[T any](t *testing.T, lookup func() T) <-chan T {
	t.Helper()
	done := make(chan T, 1)
	go func() {
		done <- lookup()
	}()
	select {
	case <-done:
		t.Fatal("the lookup did not wait for the pending revalidation")
	case <-time.After(50 * time.Millisecond):
	}
	return done
}

func receiveWithin[T any](t *testing.T, done <-chan T) T {
	t.Helper()
	select {
	case v := <-done:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("the lookup is still waiting")
		return *new(T)
	}
}

func httpLookupState(t *testing.T, inst *Instance, cacheHandle int32) uint32 {
	t.Helper()
	if status := inst.xqd_http_cache_get_state(cacheHandle, httpCacheTestSecondOut); status != XqdStatusOK {
		t.Fatalf("get_state status = %d", status)
	}
	return inst.memory.Uint32(httpCacheTestSecondOut)
}

func sharedHTTPCacheInstance(inst *Instance) *Instance {
	other := newHTTPCacheStoreTestInstance()
	other.cache = inst.cache
	return other
}

// checkHTTPNotFoundAccessors checks the accessors that need a found object.
func checkHTTPNotFoundAccessors(t *testing.T, inst *Instance, handle int32) {
	t.Helper()
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
		"get_found_response": func() int32 {
			return inst.xqd_http_cache_get_found_response(handle, 1, httpCacheTestHandleOut, httpCacheTestSecondOut)
		},
	}
	checkStatuses(t, accessors, XqdErrNone)
}

func TestHttpCacheStatePerPeriod(t *testing.T) {
	tests := []struct {
		name          string
		freshness     httpFreshness
		leader, plain uint32
	}{
		{"fresh", httpFreshObject, foundState, foundState},
		{"stale-while-revalidate", httpStaleObject, staleState | CacheLookupStateMustInsertOrUpdate, staleState},
		{
			"within both stale periods",
			httpFreshness{maxAge: time.Minute, initialAge: 90 * time.Second, swr: time.Minute, sie: 5 * time.Minute},
			staleState | CacheLookupStateMustInsertOrUpdate, staleState,
		},
		{"stale-if-error", httpStaleIfErrorObject, usableIfErrorState | CacheLookupStateMustInsertOrUpdate, usableIfErrorState},
		{
			"stale-if-error after stale-while-revalidate",
			httpFreshness{maxAge: time.Minute, initialAge: 150 * time.Second, swr: time.Minute, sie: 5 * time.Minute},
			usableIfErrorState | CacheLookupStateMustInsertOrUpdate, usableIfErrorState,
		},
		{"expired", httpExpiredObject, CacheLookupStateMustInsertOrUpdate, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := newHTTPCacheStoreTestInstance()
			storeHTTPObject(t, inst, nil, tt.freshness, "body")
			if state := httpLookupState(t, inst, httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))); state != tt.leader {
				t.Errorf("transaction lookup state = %#x, want %#x", state, tt.leader)
			}
			if state := httpLookupState(t, inst, httpCachePlainLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))); state != tt.plain {
				t.Errorf("lookup state = %#x, want %#x", state, tt.plain)
			}
		})
	}
}

func TestHttpCacheStaleIfErrorObjectIsFoundButNotServed(t *testing.T) {
	inst := newHTTPCacheStoreTestInstance()
	storeHTTPObject(t, inst, nil, httpFreshness{maxAge: time.Minute, initialAge: 90 * time.Second, sie: 2 * time.Minute}, "body")

	handle := httpCachePlainLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
	if status := inst.xqd_http_cache_get_stale_if_error_ns(handle, httpCacheTestSecondOut); status != XqdStatusOK {
		t.Fatalf("get_stale_if_error_ns status = %d", status)
	}
	if sie := time.Duration(inst.memory.ReadUint64(httpCacheTestSecondOut)); sie != 2*time.Minute {
		t.Fatalf("stale-if-error = %v, want 2m", sie)
	}
	if status := inst.xqd_http_cache_get_found_response(handle, 1, httpCacheTestHandleOut, httpCacheTestSecondOut); status != XqdErrNone {
		t.Fatalf("get_found_response status = %d, want %d", status, XqdErrNone)
	}
}

func TestHttpCacheExpiredObjectIsOnlyKeptForRevalidation(t *testing.T) {
	inst := newHTTPCacheStoreTestInstance()
	storeHTTPObject(t, inst, http.Header{"Etag": {httpTestObjectETag}}, httpExpiredObject, "body")

	leader := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
	checkHTTPNotFoundAccessors(t, inst, leader)
	if status := inst.xqd_http_cache_get_suggested_backend_request(leader, httpCacheTestHandleOut); status != XqdStatusOK {
		t.Fatalf("get_suggested_backend_request status = %d", status)
	}
	req := inst.requests.Get(int(inst.memory.Uint32(httpCacheTestHandleOut)))
	if got := req.Header.Get("If-None-Match"); got != httpTestObjectETag {
		t.Fatalf("If-None-Match = %q, want %q", got, httpTestObjectETag)
	}
}

func TestHttpCacheChooseStale(t *testing.T) {
	t.Run("stale-if-error", func(t *testing.T) {
		inst := newHTTPCacheStoreTestInstance()
		storeHTTPObject(t, inst, nil, httpStaleIfErrorObject, "stale")
		leader := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
		if status := inst.xqd_http_cache_transaction_choose_stale(leader); status != XqdStatusOK {
			t.Fatalf("choose_stale status = %d", status)
		}
		if state := httpLookupState(t, inst, leader); state != foundState|usableIfErrorState {
			t.Fatalf("state = %#x, want %#x", state, foundState|usableIfErrorState)
		}
		if _, body := foundResponse(t, inst, leader, 1); body != "stale" {
			t.Fatalf("found body = %q, want %q", body, "stale")
		}
		if status := inst.xqd_http_cache_transaction_choose_stale(leader); status != XqdErrInvalidHandle {
			t.Fatalf("second choose_stale status = %d, want %d", status, XqdErrInvalidHandle)
		}
	})

	t.Run("stale-while-revalidate", func(t *testing.T) {
		inst := newHTTPCacheStoreTestInstance()
		storeHTTPObject(t, inst, nil, httpStaleObject, "stale")
		leader := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
		if status := inst.xqd_http_cache_transaction_choose_stale(leader); status != XqdStatusOK {
			t.Fatalf("choose_stale status = %d", status)
		}
		if state := httpLookupState(t, inst, leader); state != staleState {
			t.Fatalf("state = %#x, want %#x", state, staleState)
		}
	})

	t.Run("without the obligation", func(t *testing.T) {
		inst := newHTTPCacheStoreTestInstance()
		storeHTTPObject(t, inst, nil, httpStaleIfErrorObject, "stale")
		plain := httpCachePlainLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
		if status := inst.xqd_http_cache_transaction_choose_stale(plain); status != XqdErrInvalidHandle {
			t.Fatalf("choose_stale status = %d, want %d", status, XqdErrInvalidHandle)
		}
	})

	t.Run("without an object", func(t *testing.T) {
		for name, store := range map[string]bool{"expired": true, "missing": false} {
			inst := newHTTPCacheStoreTestInstance()
			if store {
				storeHTTPObject(t, inst, nil, httpExpiredObject, "expired")
			}
			leader := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
			if status := inst.xqd_http_cache_transaction_choose_stale(leader); status != XqdErrInvalidHandle {
				t.Errorf("%s: choose_stale status = %d, want %d", name, status, XqdErrInvalidHandle)
			}
			if state := httpLookupState(t, inst, leader); state != CacheLookupStateMustInsertOrUpdate {
				t.Errorf("%s: state after choose_stale = %#x, want only must-insert-or-update", name, state)
			}
		}
	})
}

func TestHttpCacheStaleIfErrorLookupWaitsForTheRevalidation(t *testing.T) {
	tests := []struct {
		name    string
		resolve func(t *testing.T, a *Instance, leader int32)
		state   uint32
		body    string // what the waiter serves, choosing stale if it leads
	}{
		{
			"stale chosen",
			func(t *testing.T, a *Instance, leader int32) {
				if status := a.xqd_http_cache_transaction_choose_stale(leader); status != XqdStatusOK {
					t.Fatalf("choose_stale status = %d", status)
				}
			},
			foundState | usableIfErrorState, "stale",
		},
		{
			"abandoned",
			func(t *testing.T, a *Instance, leader int32) {
				if status := a.xqd_http_cache_transaction_abandon(leader); status != XqdStatusOK {
					t.Fatalf("abandon status = %d", status)
				}
			},
			usableIfErrorState | CacheLookupStateMustInsertOrUpdate, "stale",
		},
		{
			"revalidated",
			func(t *testing.T, a *Instance, leader int32) {
				insertHTTPObject(t, a, leader, nil, httpFreshObject, "fresh")
			},
			foundState, "fresh",
		},
		{
			// Like production, the waiter keeps the object it found.
			"replaced by a stale response",
			func(t *testing.T, a *Instance, leader int32) {
				insertHTTPObject(t, a, leader, nil, httpStaleIfErrorObject, "still stale")
			},
			usableIfErrorState | CacheLookupStateMustInsertOrUpdate, "stale",
		},
		{
			// Judged when the waiter wakes up, a max-age=0 response has expired.
			"replaced by an expired response",
			func(t *testing.T, a *Instance, leader int32) {
				insertHTTPObject(t, a, leader, nil, httpFreshness{}, "expired")
			},
			usableIfErrorState | CacheLookupStateMustInsertOrUpdate, "stale",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newHTTPCacheStoreTestInstance()
			b := sharedHTTPCacheInstance(a)
			storeHTTPObject(t, a, nil, httpStaleIfErrorObject, "stale")
			leader := httpCacheTransactionLookup(t, a, httpCacheTestRequest(a, http.MethodGet, nil))

			req := httpCacheTestRequest(b, http.MethodGet, nil)
			looked := startWaiting(t, func() int32 {
				return b.xqd_http_cache_transaction_lookup(req, 0, 0, httpCacheTestHandleOut)
			})
			tt.resolve(t, a, leader)
			if status := receiveWithin(t, looked); status != XqdStatusOK {
				t.Fatalf("transaction_lookup status = %d", status)
			}
			handle := int32(b.memory.Uint32(httpCacheTestHandleOut))
			if state := httpLookupState(t, b, handle); state != tt.state {
				t.Fatalf("state = %#x, want %#x", state, tt.state)
			}
			if tt.state&CacheLookupStateMustInsertOrUpdate != 0 {
				if status := b.xqd_http_cache_transaction_choose_stale(handle); status != XqdStatusOK {
					t.Fatalf("choose_stale status = %d", status)
				}
			}
			if _, body := foundResponse(t, b, handle, 1); body != tt.body {
				t.Fatalf("found body = %q, want %q", body, tt.body)
			}
		})
	}
}

func TestHttpCacheStaleIfErrorLookupLeadsWithoutTheOffer(t *testing.T) {
	inst := newHTTPCacheStoreTestInstance()
	storeHTTPObject(t, inst, nil, httpStaleIfErrorObject, "stale")

	// The plain lookup uses up the revalidation offer, but nobody revalidates.
	httpCachePlainLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
	leader := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
	if state := httpLookupState(t, inst, leader); state != usableIfErrorState|CacheLookupStateMustInsertOrUpdate {
		t.Fatalf("state = %#x, want %#x", state, usableIfErrorState|CacheLookupStateMustInsertOrUpdate)
	}
}

func TestHttpCacheStaleIfErrorLookupJoinsItsOwnRevalidation(t *testing.T) {
	inst := newHTTPCacheStoreTestInstance()
	storeHTTPObject(t, inst, nil, httpStaleIfErrorObject, "stale")
	leader := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))

	looked := make(chan int32, 1)
	req := httpCacheTestRequest(inst, http.MethodGet, nil)
	go func() {
		looked <- inst.xqd_http_cache_transaction_lookup(req, 0, 0, httpCacheTestHandleOut)
	}()
	if status := receiveWithin(t, looked); status != XqdStatusOK {
		t.Fatalf("transaction_lookup status = %d", status)
	}
	again := int32(inst.memory.Uint32(httpCacheTestHandleOut))
	if inst.cacheHandles.Get(int(again)).Transaction != inst.cacheHandles.Get(int(leader)).Transaction {
		t.Fatal("the second lookup did not join the guest's revalidation")
	}
}

func TestCacheChosenStaleObjectReachesNewerObjectsOfItsVariant(t *testing.T) {
	cache := NewCache()
	key := []byte("key")
	older := cache.Insert(key, staleIfErrorWriteOptions())
	older.FinishWrite()
	refresh := func(owner string) func() *CacheTransaction {
		return func() *CacheTransaction { return cache.TransactionRefresh(cache.TransactionLookup(key, nil, owner)) }
	}

	// The leader stores a response that is already stale, so the waiter leads.
	first := cache.TransactionLookup(key, nil, "owner-a")
	waitedB := startWaiting(t, refresh("owner-b"))
	newer := transactionInsertFinished(t, cache, first, staleIfErrorWriteOptions())
	second := receiveWithin(t, waitedB)
	if !second.Entry.State.MustInsertOrUpdate || second.Entry.Object != older {
		t.Fatalf("the waiter did not take over the revalidation of the object it found")
	}

	// A later lookup finds the newer object and waits for that revalidation.
	waitedC := startWaiting(t, refresh("owner-c"))
	cache.TransactionChooseStale(second)
	third := receiveWithin(t, waitedC)
	if !third.Entry.State.RevalidationFailed || third.Entry.State.MustInsertOrUpdate || third.Entry.Object != newer {
		t.Fatalf("state = %+v, want the newer object of the same variant, as chosen after the failed revalidation", third.Entry.State)
	}
}

// Fastlike collapses per key, so this lookup waits where CacheD would not,
// but it must not take the stale object chosen for another variant.
func TestCacheChosenStaleObjectOnlyReachesItsVariant(t *testing.T) {
	cache := NewCache()
	key := []byte("key")
	options := func(lang string) *CacheLookupOptions {
		return &CacheLookupOptions{RequestHeaders: []byte("Accept-Language: " + lang + "\r\n")}
	}
	for _, lang := range []string{"en", "fr"} {
		write := staleIfErrorWriteOptions()
		write.VaryRule = "accept-language"
		write.RequestHeaders = options(lang).RequestHeaders
		cache.Insert(key, write).FinishWrite()
	}

	leader := cache.TransactionLookup(key, options("en"), "owner-a")
	if !leader.Entry.State.MustInsertOrUpdate {
		t.Fatal("the first lookup should revalidate")
	}
	waited := startWaiting(t, func() *CacheTransaction {
		return cache.TransactionRefresh(cache.TransactionLookup(key, options("fr"), "owner-b"))
	})
	cache.TransactionChooseStale(leader)
	if tx := receiveWithin(t, waited); tx.Entry.State.RevalidationFailed || !tx.Entry.State.MustInsertOrUpdate {
		t.Fatalf("state = %+v, want the obligation to revalidate its own variant", tx.Entry.State)
	}
}

func TestCacheStaleIfErrorKeepsObjectsAround(t *testing.T) {
	cache := NewCache()
	key := []byte("key")
	cache.Insert(key, staleIfErrorWriteOptions()).FinishWrite()

	entry := cache.Lookup(key, nil)
	if want := (CacheState{Found: true, Usable: true, Stale: true}); entry.State != want {
		t.Fatalf("state = %+v, want %+v", entry.State, want)
	}
	if period := entry.httpPeriod(); period != periodStaleIfError {
		t.Fatalf("HTTP period = %d, want stale-if-error", period)
	}
}

func TestHttpCacheInsertUsesUpTheObligation(t *testing.T) {
	inst := newHTTPCacheStoreTestInstance()
	storeHTTPObject(t, inst, nil, httpStaleIfErrorObject, "stale")
	leader := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))

	insertHTTPObject(t, inst, leader, nil, httpFreshObject, "fresh")
	if state := httpLookupState(t, inst, leader); state != usableIfErrorState {
		t.Fatalf("state after the insert = %#x, want %#x", state, usableIfErrorState)
	}
	resp := func() int32 { return httpCacheTestResponse(inst, http.StatusOK, nil) }
	calls := map[string]func() int32{
		"transaction_choose_stale": func() int32 { return inst.xqd_http_cache_transaction_choose_stale(leader) },
		"transaction_abandon":      func() int32 { return inst.xqd_http_cache_transaction_abandon(leader) },
		"transaction_insert": func() int32 {
			return inst.xqd_http_cache_transaction_insert(leader, resp(), 0, httpCacheTestOptions, httpCacheTestHandleOut)
		},
		"transaction_insert_and_stream_back": func() int32 {
			return inst.xqd_http_cache_transaction_insert_and_stream_back(leader, resp(), 0, httpCacheTestOptions, httpCacheTestHandleOut, httpCacheTestSecondOut)
		},
		"transaction_update": func() int32 {
			return inst.xqd_http_cache_transaction_update(leader, resp(), 0, httpCacheTestOptions)
		},
		"transaction_update_and_return_fresh": func() int32 {
			return inst.xqd_http_cache_transaction_update_and_return_fresh(leader, resp(), 0, httpCacheTestOptions, httpCacheTestHandleOut)
		},
		"transaction_record_not_cacheable": func() int32 {
			return inst.xqd_http_cache_transaction_record_not_cacheable(leader, 0, httpCacheTestOptions)
		},
	}
	checkStatuses(t, calls, XqdErrInvalidHandle)
	if _, body := foundResponse(t, inst, httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil)), 1); body != "fresh" {
		t.Fatalf("found body = %q, want the inserted one", body)
	}
}

func TestHttpCacheWriteWithoutObligationConsumesTheResponse(t *testing.T) {
	inst := newHTTPCacheStoreTestInstance()
	storeHTTPObject(t, inst, nil, httpFreshObject, "fresh")
	hit := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))

	resp := httpCacheTestResponse(inst, http.StatusOK, nil)
	if status := inst.xqd_http_cache_transaction_insert(hit, resp, 0, httpCacheTestOptions, httpCacheTestHandleOut); status != XqdErrInvalidHandle {
		t.Fatalf("transaction_insert status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if inst.responses.Get(int(resp)) != nil {
		t.Fatal("the response handle is still open")
	}
}

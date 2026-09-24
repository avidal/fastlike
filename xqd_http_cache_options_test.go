package fastlike

import (
	"crypto/sha256"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Guest memory layout of the suggested options and lookup options tests, on
// top of the one the storage tests use.
const (
	suggestTestMaskOut  = 32
	suggestTestPointers = 1024
	suggestTestOut      = 1536
	suggestTestVaryBuf  = 2048
	suggestTestKeysBuf  = 2304
	lookupTestOptions   = 1024
	lookupTestKey       = 1100
	lookupTestBackend   = 1200
)

func TestLogicalCacheKey(t *testing.T) {
	// The vector of production's logical_key test.
	want := sha256.Sum256([]byte(logicalCacheKeySalt + "\x00example.com\x00/path?query\x00"))

	fromURI := &http.Request{URL: mustParseURL(t, "http://ExAmPlE.cOm/path?query"), Header: http.Header{}}
	fromHeader := &http.Request{URL: mustParseURL(t, "http://something.else/path?query"), Header: http.Header{"Host": {"Example.COM"}}}
	downstream := &http.Request{URL: mustParseURL(t, "http://something.else/path?query"), Header: http.Header{}, Host: "example.com"}
	withPort := &http.Request{URL: mustParseURL(t, "http://example.com:8080/path?query"), Header: http.Header{}}
	for name, r := range map[string]*http.Request{"uri": fromURI, "host header": fromHeader, "downstream host": downstream, "uri port": withPort} {
		if got := logicalCacheKey(r); got != want {
			t.Errorf("%s: key %x, want %x", name, got, want)
		}
	}

	ipv6 := &http.Request{URL: mustParseURL(t, "http://[::1]:8080"), Header: http.Header{}}
	if got, want := logicalCacheKey(ipv6), sha256.Sum256([]byte(logicalCacheKeySalt+"\x00[::1]\x00/\x00")); got != want {
		t.Errorf("ipv6 without path: key %x, want %x", got, want)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSuggestCacheOptions(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 250_000_000, time.UTC)
	date := "Thu, 24 Sep 2026 12:00:00 GMT"
	tests := []struct {
		name   string
		header http.Header
		want   suggestedCacheOptions
	}{
		{"no directives", http.Header{}, suggestedCacheOptions{maxAgeNs: uint64(time.Hour)}},
		{"max-age", http.Header{"Cache-Control": {"max-age=60"}}, suggestedCacheOptions{maxAgeNs: uint64(time.Minute)}},
		{"s-maxage wins", http.Header{"Cache-Control": {"max-age=60, s-maxage=120"}}, suggestedCacheOptions{maxAgeNs: uint64(2 * time.Minute)}},
		{"surrogate-control first", http.Header{"Surrogate-Control": {"max-age=10"}, "Cache-Control": {"max-age=60, stale-while-revalidate=5"}}, suggestedCacheOptions{maxAgeNs: uint64(10 * time.Second)}},
		{
			"stale periods and age",
			http.Header{"Cache-Control": {"max-age=60, stale-while-revalidate=30, stale-if-error=300"}, "Age": {"15, 20"}},
			suggestedCacheOptions{maxAgeNs: uint64(time.Minute), initialAgeNs: uint64(15 * time.Second), staleWhileRevalidateNs: uint64(30 * time.Second), staleIfErrorNs: uint64(5 * time.Minute)},
		},
		{"invalid age", http.Header{"Age": {"soon"}}, suggestedCacheOptions{maxAgeNs: uint64(time.Hour)}},
		{"expires minus date", http.Header{"Date": {date}, "Expires": {"Thu, 24 Sep 2026 12:01:40 GMT"}}, suggestedCacheOptions{maxAgeNs: uint64(100 * time.Second)}},
		{"expires before date", http.Header{"Date": {date}, "Expires": {"Thu, 24 Sep 2026 11:00:00 GMT"}}, suggestedCacheOptions{}},
		{"expires without date", http.Header{"Expires": {"Thu, 24 Sep 2026 12:01:00 GMT"}}, suggestedCacheOptions{maxAgeNs: uint64(59*time.Second + 750*time.Millisecond)}},
		{"unparsable expires", http.Header{"Expires": {"0"}}, suggestedCacheOptions{maxAgeNs: uint64(time.Hour)}},
		{"expires past u64 nanoseconds", http.Header{"Date": {date}, "Expires": {"Fri, 31 Dec 9999 23:59:59 GMT"}}, suggestedCacheOptions{maxAgeNs: math.MaxUint64}},
		{"malformed cache-control keeps expires", http.Header{"Cache-Control": {"max-age=60, stale-while-revalidate=30, ="}, "Date": {date}, "Expires": {"Thu, 24 Sep 2026 12:00:10 GMT"}}, suggestedCacheOptions{maxAgeNs: uint64(10 * time.Second)}},
		{"vary", http.Header{"Vary": {"Accept-Encoding, X-Foo", "accept-encoding"}}, suggestedCacheOptions{maxAgeNs: uint64(time.Hour), varyRule: "accept-encoding x-foo accept-encoding"}},
		{"vary wildcard", http.Header{"Vary": {"*"}}, suggestedCacheOptions{maxAgeNs: uint64(time.Hour), varyRule: "*"}},
		{"vary line with a keyword is skipped", http.Header{"Vary": {"Accept-Encoding, Public", "Cookie"}}, suggestedCacheOptions{maxAgeNs: uint64(time.Hour), varyRule: "cookie"}},
	}
	for _, tt := range tests {
		if got := suggestCacheOptions(tt.header, now); got != tt.want {
			t.Errorf("%s: got %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestParseHTTPDate(t *testing.T) {
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	nov6 := func(year int) time.Time { return time.Date(year, 11, 6, 8, 49, 37, 0, time.UTC) }
	valid := map[string]time.Time{
		"Sun, 06 Nov 1994 08:49:37 GMT":    nov6(1994),
		"SUN, 06 nov 1994 08:49:37 gmt":    nov6(1994),
		"Sunday, 06-Nov-94 08:49:37 GMT":   nov6(1994),
		"Saturday, 06-Nov-04 08:49:37 GMT": nov6(2004),
		// Within 50 years ahead, so not 1970 as http.ParseTime would say.
		"Thursday, 06-Nov-70 08:49:37 GMT": nov6(2070),
		"Sun Nov  6 08:49:37 1994":         nov6(1994),
		"Sun Nov 06 08:49:37 1994":         nov6(1994),
	}
	for s, want := range valid {
		if got, ok := parseHTTPDate(now, s); !ok || !got.Equal(want) {
			t.Errorf("%q: got %v, %v, want %v", s, got, ok, want)
		}
	}
	for _, s := range []string{
		"Mon, 06 Nov 1994 08:49:37 GMT",
		"Sun, 6 Nov 1994 08:49:37 GMT",
		"Sun, 06 Nov 1994 08:49:37 GMT ",
		"Sun, 06 Nov 1994 08:49:60 GMT",
		"Sun, 06 Nov 1994 08:49:37 UTC",
		"Thu, 30 Feb 2026 00:00:00 GMT",
		"Sund, 06 Nov 1994 08:49:37 GMT",
		"Sun Nov  16 08:49:37 1994",
		"0",
	} {
		if got, ok := parseHTTPDate(now, s); ok {
			t.Errorf("%q: parsed as %v", s, got)
		}
	}
}

func TestRequestedRange(t *testing.T) {
	tests := []struct {
		value string
		want  byteRange
		ok    bool
	}{
		{"bytes=0-1023", byteRange{first: 0, last: 1023, hasFirst: true, hasLast: true}, true},
		{"bytes=500-", byteRange{first: 500, hasFirst: true}, true},
		{"bytes=-500", byteRange{last: 500, hasLast: true}, true},
		{"bytes=0-1,3-4", byteRange{first: 0, last: 1, hasFirst: true, hasLast: true}, true},
		{"bytes=1-abc", byteRange{first: 1, hasFirst: true}, true},
		{"bytes=0-18446744073709551616", byteRange{first: 0, hasFirst: true}, true},
		{"bytes=18446744073709551616-5", byteRange{last: 5, hasLast: true}, true},
		{"bytes=3-3", byteRange{}, false},
		{"bytes=10-5", byteRange{}, false},
		{"bytes=-0", byteRange{}, false},
		{"bytes=-", byteRange{}, false},
		{"Bytes=0-5", byteRange{}, false},
		{"bytes= 0-5", byteRange{}, false},
		{"items=0-5", byteRange{}, false},
		{"bytes=0-5\x80", byteRange{}, false},
	}
	for _, tt := range tests {
		req := &http.Request{Method: http.MethodGet, Header: http.Header{"Range": {tt.value}}}
		got, ok := requestedRange(req)
		if ok != tt.ok || ok && got != tt.want {
			t.Errorf("%q: got %+v, %v, want %+v, %v", tt.value, got, ok, tt.want, tt.ok)
		}
	}

	head := &http.Request{Method: http.MethodHead, Header: http.Header{"Range": {"bytes=0-5"}}}
	ifRange := &http.Request{Method: http.MethodGet, Header: http.Header{"Range": {"bytes=0-5"}, "If-Range": {""}}}
	for name, req := range map[string]*http.Request{"HEAD": head, "If-Range": ifRange} {
		if _, ok := requestedRange(req); ok {
			t.Errorf("%s: range served", name)
		}
	}
}

func TestContentRange(t *testing.T) {
	tests := []struct {
		r          byteRange
		total      int64
		totalKnown bool
		want       string
	}{
		{byteRange{first: 2, last: 5, hasFirst: true, hasLast: true}, 10, true, "bytes 2-5/10"},
		{byteRange{first: 0, last: 100, hasFirst: true, hasLast: true}, 10, true, "bytes 0-100/10"},
		{byteRange{first: 5, hasFirst: true}, 10, true, "bytes 5-9/10"},
		{byteRange{first: 30, hasFirst: true}, 10, true, "bytes 30-9/10"},
		{byteRange{last: 3, hasLast: true}, 10, true, "bytes 7-9/10"},
		{byteRange{last: 20, hasLast: true}, 10, true, "bytes 18446744073709551606-9/10"},
		{byteRange{first: 2, last: 5, hasFirst: true, hasLast: true}, 0, false, "bytes 2-5/*"},
		{byteRange{first: 5, hasFirst: true}, 0, false, ""},
		{byteRange{last: 3, hasLast: true}, 0, false, ""},
	}
	for _, tt := range tests {
		got, ok := tt.r.contentRange(tt.total, tt.totalKnown)
		if ok != (tt.want != "") || got != tt.want {
			t.Errorf("%+v of %d (known %v): got %q, %v, want %q", tt.r, tt.total, tt.totalKnown, got, ok, tt.want)
		}
	}
}

func TestCacheControlKeywordsAreNotTokens(t *testing.T) {
	if _, ok := parseFieldNames("Accept, Public"); ok {
		t.Error("a field name list naming public parsed")
	}
	if _, ok := parseFieldNames(strings.Repeat("a", maxHeaderNameLen+1)); ok {
		t.Error("an overlong field name parsed")
	}
	if _, ok := parseFieldNames("no-cache-please"); !ok {
		t.Error("a field name that only starts with a keyword did not parse")
	}
	if _, ok := parseCacheDirectives("max-age=60, x=private"); ok {
		t.Error("a directive with a keyword argument parsed")
	}
	if _, ok := parseCacheDirectives(`max-age=60, x="private"`); !ok {
		t.Error("a quoted keyword argument did not parse")
	}
}

// suggestedOptions calls get_suggested_cache_options with guest buffers of
// the given sizes.
func suggestedOptions(inst *Instance, cacheHandle, respID int32, mask uint32, varyBufLen, keysBufLen uint32) int32 {
	for _, field := range []struct{ offset, value uint32 }{
		{httpCacheOptionsVaryRule, suggestTestVaryBuf}, {httpCacheOptionsVaryRule + 4, varyBufLen},
		{httpCacheOptionsSurrogateKeys, suggestTestKeysBuf}, {httpCacheOptionsSurrogateKeys + 4, keysBufLen},
	} {
		inst.memory.PutUint32(field.value, int64(suggestTestPointers+field.offset))
	}
	clear(inst.memory.Data()[suggestTestOut : suggestTestOut+httpCacheOptionsStaleIfErrorNs+8])
	return inst.xqd_http_cache_get_suggested_cache_options(cacheHandle, respID, mask, suggestTestPointers, suggestTestMaskOut, suggestTestOut)
}

func TestHttpCacheGetSuggestedCacheOptions(t *testing.T) {
	inst, cacheHandle := newHTTPCacheTransaction(t)
	respID := httpCacheTestResponse(inst, http.StatusOK, http.Header{
		"Cache-Control": {"s-maxage=120, stale-while-revalidate=30, stale-if-error=300"},
		"Age":           {"10"},
		"Vary":          {"Accept-Encoding"},
	})
	wantKeys := keySurrogateKey(inst.cacheHandles.Get(int(cacheHandle)).Transaction.Key)
	out := func(offset uint32) int64 { return int64(suggestTestOut + offset) }

	if status := suggestedOptions(inst, cacheHandle, respID, httpCacheWriteOptionsKnown, 4, 4); status != XqdErrBufferLength {
		t.Fatalf("small buffers: status %d, want %d", status, XqdErrBufferLength)
	}
	if got := inst.memory.Uint32(out(httpCacheOptionsVaryRule + 4)); got != uint32(len("accept-encoding")) {
		t.Errorf("small buffers: vary rule length %d", got)
	}
	if got := inst.memory.Uint32(out(httpCacheOptionsSurrogateKeys + 4)); got != uint32(len(wantKeys)) {
		t.Errorf("small buffers: surrogate keys length %d", got)
	}
	if got := inst.memory.Uint32(out(httpCacheOptionsVaryRule)); got != 0 {
		t.Errorf("small buffers: vary rule pointer written as %d", got)
	}

	if status := suggestedOptions(inst, cacheHandle, respID, httpCacheWriteOptionsKnown, 64, 128); status != XqdStatusOK {
		t.Fatalf("status %d", status)
	}
	wantMask := HttpCacheWriteOptionsMaskVaryRule | HttpCacheWriteOptionsMaskInitialAgeNs |
		HttpCacheWriteOptionsMaskStaleWhileRevalidateNs | HttpCacheWriteOptionsMaskStaleIfErrorNs |
		HttpCacheWriteOptionsMaskSurrogateKeys
	if got := inst.memory.Uint32(suggestTestMaskOut); got != wantMask {
		t.Errorf("mask %#x, want %#x", got, wantMask)
	}
	for _, field := range []struct {
		name   string
		offset uint32
		want   time.Duration
	}{
		{"max age", httpCacheOptionsMaxAgeNs, 2 * time.Minute},
		{"initial age", httpCacheOptionsInitialAgeNs, 10 * time.Second},
		{"stale-while-revalidate", httpCacheOptionsStaleWhileRevalidateNs, 30 * time.Second},
		{"stale-if-error", httpCacheOptionsStaleIfErrorNs, 5 * time.Minute},
	} {
		if got := time.Duration(inst.memory.Uint64(out(field.offset))); got != field.want {
			t.Errorf("%s: %v, want %v", field.name, got, field.want)
		}
	}
	if got, _ := inst.readGuestBytes(int32(out(httpCacheOptionsVaryRule))); string(got) != "accept-encoding" {
		t.Errorf("vary rule %q", got)
	}
	if got, _ := inst.readGuestBytes(int32(out(httpCacheOptionsSurrogateKeys))); string(got) != wantKeys {
		t.Errorf("surrogate keys %q, want %q", got, wantKeys)
	}

	if status := suggestedOptions(inst, cacheHandle, respID, 0, 0, 0); status != XqdStatusOK || inst.memory.Uint32(suggestTestMaskOut) != 0 {
		t.Errorf("nothing requested: status %d, mask %#x", status, inst.memory.Uint32(suggestTestMaskOut))
	}
	if got := time.Duration(inst.memory.Uint64(out(httpCacheOptionsMaxAgeNs))); got != 2*time.Minute {
		t.Errorf("nothing requested: max age %v", got)
	}
}

func TestHttpCacheGetSuggestedCacheOptionsValidation(t *testing.T) {
	inst, cacheHandle := newHTTPCacheTransaction(t)
	respID := httpCacheTestResponse(inst, http.StatusOK, http.Header{})
	call := func(out int32) int32 {
		return inst.xqd_http_cache_get_suggested_cache_options(cacheHandle, respID, 0, suggestTestPointers, suggestTestMaskOut, out)
	}
	if status := call(suggestTestOut + 4); status != XqdErrBadAlignment {
		t.Errorf("misaligned options: status %d, want %d", status, XqdErrBadAlignment)
	}
	if status := call(4096 - 4); status != XqdErrInvalidArgument {
		t.Errorf("options out of bounds: status %d, want %d", status, XqdErrInvalidArgument)
	}
	if status := inst.xqd_http_cache_get_suggested_cache_options(cacheHandle, 99, 0, suggestTestPointers, suggestTestMaskOut, suggestTestOut); status != XqdErrInvalidHandle {
		t.Errorf("bad response handle: status %d", status)
	}
	expectGuestTrap(t, "unknown mask bit", func() {
		inst.xqd_http_cache_get_suggested_cache_options(cacheHandle, respID, 1<<8, suggestTestPointers, suggestTestMaskOut, suggestTestOut)
	})
}

func expectGuestTrap(t *testing.T, what string, call func()) {
	t.Helper()
	defer func() {
		t.Helper()
		if _, ok := recover().(guestTrap); !ok {
			t.Errorf("%s: did not trap", what)
		}
	}()
	call()
}

// writeLookupOptions lays out lookup options naming an override key and a
// backend.
func writeLookupOptions(inst *Instance, key []byte, backend string) {
	copy(inst.memory.Data()[lookupTestKey:], key)
	copy(inst.memory.Data()[lookupTestBackend:], backend)
	for idx, v := range []uint32{lookupTestKey, uint32(len(key)), lookupTestBackend, uint32(len(backend))} {
		inst.memory.PutUint32(v, int64(lookupTestOptions+idx*4))
	}
}

func TestHttpCacheLookupOverrideKey(t *testing.T) {
	inst := newHTTPCacheStoreTestInstance()
	overrideKey := []byte(strings.Repeat("k", 32))
	writeLookupOptions(inst, overrideKey, "origin")
	mask := HttpCacheLookupOptionsMaskOverrideKey | HttpCacheLookupOptionsMaskBackendName

	first := httpCacheTestRequest(inst, http.MethodGet, nil)
	if status := inst.xqd_http_cache_transaction_lookup(first, mask, lookupTestOptions, httpCacheTestHandleOut); status != XqdStatusOK {
		t.Fatalf("transaction_lookup status %d", status)
	}
	readback := storeThroughStreamBack(t, inst, int32(inst.memory.Uint32(httpCacheTestHandleOut)), httpCacheTestResponse(inst, http.StatusOK, http.Header{}), "shared")
	foundResponse(t, inst, readback, 0)

	other := httpCacheTestRequest(inst, http.MethodGet, nil)
	inst.requests.Get(int(other)).URL = mustParseURL(t, "https://elsewhere.example/other")
	if status := inst.xqd_http_cache_lookup(other, mask, lookupTestOptions, httpCacheTestHandleOut); status != XqdStatusOK {
		t.Fatalf("lookup status %d", status)
	}
	if _, body := foundResponse(t, inst, int32(inst.memory.Uint32(httpCacheTestHandleOut)), 1); body != "shared" {
		t.Errorf("override key lookup found %q", body)
	}

	if keys := httpCacheSurrogateKeys(t, inst, readback); keys != keySurrogateKey(overrideKey) {
		t.Errorf("surrogate keys %q, want the override key", keys)
	}
}

func httpCacheSurrogateKeys(t *testing.T, inst *Instance, cacheHandle int32) string {
	t.Helper()
	if status := inst.xqd_http_cache_get_surrogate_keys(cacheHandle, httpCacheTestDataPtr, 512, httpCacheTestNwrittenOut); status != XqdStatusOK {
		t.Fatalf("get_surrogate_keys status %d", status)
	}
	n := inst.memory.Uint32(httpCacheTestNwrittenOut)
	return string(inst.memory.Data()[httpCacheTestDataPtr : httpCacheTestDataPtr+n])
}

func TestHttpCacheLookupOptionsValidation(t *testing.T) {
	inst := newHTTPCacheStoreTestInstance()
	reqID := httpCacheTestRequest(inst, http.MethodGet, nil)
	lookup := func(req int32, mask uint32, options int32) int32 {
		return inst.xqd_http_cache_lookup(req, mask, options, httpCacheTestHandleOut)
	}

	writeLookupOptions(inst, []byte("short"), "origin")
	if status := lookup(reqID, HttpCacheLookupOptionsMaskOverrideKey, lookupTestOptions); status != XqdErrInvalidArgument {
		t.Errorf("short override key: status %d, want %d", status, XqdErrInvalidArgument)
	}
	writeLookupOptions(inst, nil, "\xff")
	if status := lookup(reqID, HttpCacheLookupOptionsMaskBackendName, lookupTestOptions); status != XqdErrInvalidArgument {
		t.Errorf("backend name that is not UTF-8: status %d, want %d", status, XqdErrInvalidArgument)
	}
	// Options are read before the request handle is checked.
	if status := lookup(99, 0, lookupTestOptions+2); status != XqdErrBadAlignment {
		t.Errorf("misaligned options: status %d, want %d", status, XqdErrBadAlignment)
	}
	if status := lookup(99, 0, 4096-8); status != XqdErrInvalidArgument {
		t.Errorf("options out of bounds: status %d, want %d", status, XqdErrInvalidArgument)
	}
	if status := lookup(99, 0, lookupTestOptions); status != XqdErrInvalidHandle {
		t.Errorf("bad request handle: status %d, want %d", status, XqdErrInvalidHandle)
	}
	// Fastlike routes unknown backends to the default one, so any name goes.
	writeLookupOptions(inst, nil, "no-such-backend")
	if status := lookup(reqID, HttpCacheLookupOptionsMaskReserved|HttpCacheLookupOptionsMaskBackendName, lookupTestOptions); status != XqdStatusOK {
		t.Errorf("unknown backend name: status %d", status)
	}
	expectGuestTrap(t, "unknown mask bit", func() { lookup(reqID, 1<<3, lookupTestOptions) })
}

func TestHttpCacheInsertAddsTheKeySurrogateKey(t *testing.T) {
	inst := newHTTPCacheStoreTestInstance()
	key := logicalCacheKey(inst.requests.Get(int(httpCacheTestRequest(inst, http.MethodGet, nil))).Request)
	keySK := keySurrogateKey(key[:])

	for _, tt := range []struct{ guestKeys, want string }{
		{"", keySK},
		{"a b", "a b " + keySK},
		{keySK + " a", keySK + " a"},
	} {
		cacheHandle := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
		copy(inst.memory.Data()[httpCacheTestDataPtr:], tt.guestKeys)
		inst.memory.PutUint32(httpCacheTestDataPtr, httpCacheTestOptions+httpCacheOptionsSurrogateKeys)
		inst.memory.PutUint32(uint32(len(tt.guestKeys)), httpCacheTestOptions+httpCacheOptionsSurrogateKeys+4)
		insertBody, readback := insertAndStreamBack(t, inst, cacheHandle, httpCacheTestResponse(inst, http.StatusOK, http.Header{}), HttpCacheWriteOptionsMaskSurrogateKeys)
		closeBody(t, inst, insertBody)
		if got := httpCacheSurrogateKeys(t, inst, readback); got != tt.want {
			t.Errorf("guest keys %q: stored %q, want %q", tt.guestKeys, got, tt.want)
		}
	}
}

// checkFound checks the transformed found response of cacheHandle.
func checkFound(t *testing.T, inst *Instance, name string, cacheHandle int32, status int, contentRange, body string) {
	t.Helper()
	resp, got := foundResponse(t, inst, cacheHandle, 1)
	if resp.StatusCode != status || resp.Header.Get("Content-Range") != contentRange || got != body {
		t.Errorf("%s: got %d %q %q, want %d %q %q", name, resp.StatusCode, resp.Header.Get("Content-Range"), got, status, contentRange, body)
	}
}

func TestHttpCacheFoundResponseRanges(t *testing.T) {
	inst := newHTTPCacheStoreTestInstance()
	foundResponse(t, inst, storeObject(t, inst, http.StatusOK, nil, "0123456789"), 0)

	for _, tt := range []struct {
		name         string
		method       string
		header       http.Header
		status       int
		contentRange string
		body         string
	}{
		{"int range", http.MethodGet, http.Header{"Range": {"bytes=2-5"}}, 206, "bytes 2-5/10", "2345"},
		{"prefix", http.MethodGet, http.Header{"Range": {"bytes=5-"}}, 206, "bytes 5-9/10", "56789"},
		{"suffix", http.MethodGet, http.Header{"Range": {"bytes=-3"}}, 206, "bytes 7-9/10", "789"},
		{"past the end", http.MethodGet, http.Header{"Range": {"bytes=0-100"}}, 206, "bytes 0-100/10", "0123456789"},
		{"single byte", http.MethodGet, http.Header{"Range": {"bytes=3-3"}}, 200, "", "0123456789"},
		{"if-range", http.MethodGet, http.Header{"Range": {"bytes=2-5"}, "If-Range": {`"x"`}}, 200, "", "0123456789"},
		{"head", http.MethodHead, http.Header{"Range": {"bytes=2-5"}}, 200, "", ""},
		{"conditional first", http.MethodGet, http.Header{"Range": {"bytes=2-5"}, "If-None-Match": {"*"}}, 304, "", ""},
	} {
		checkFound(t, inst, tt.name, httpCachePlainLookup(t, inst, httpCacheTestRequest(inst, tt.method, tt.header)), tt.status, tt.contentRange, tt.body)
	}

	cacheHandle := httpCachePlainLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, http.Header{"Range": {"bytes=2-5"}}))
	if resp, body := foundResponse(t, inst, cacheHandle, 0); resp.StatusCode != 200 || body != "0123456789" {
		t.Errorf("untransformed: got %d %q", resp.StatusCode, body)
	}

	notFound := newHTTPCacheStoreTestInstance()
	foundResponse(t, notFound, storeObject(t, notFound, http.StatusNotFound, nil, "missing"), 0)
	cacheHandle = httpCachePlainLookup(t, notFound, httpCacheTestRequest(notFound, http.MethodGet, http.Header{"Range": {"bytes=0-2"}}))
	checkFound(t, notFound, "stored 404", cacheHandle, 206, "bytes 0-2/7", "mis")
}

func TestHttpCacheFoundResponseRangeOfAStreamingObject(t *testing.T) {
	inst, cacheHandle := newHTTPCacheTransaction(t)
	insertBody, _ := insertAndStreamBack(t, inst, cacheHandle, httpCacheTestResponse(inst, http.StatusOK, http.Header{}), 0)

	lookup := func(rangeHeader string) int32 {
		return httpCachePlainLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, http.Header{"Range": {rangeHeader}}))
	}
	intRange, prefix, suffix, pastTheEnd := lookup("bytes=2-5"), lookup("bytes=5-"), lookup("bytes=-3"), lookup("bytes=20-30")
	appendSource(t, inst, insertBody, io.NopCloser(strings.NewReader("0123456789")))
	closeBody(t, inst, insertBody)

	// Production serves the requested bytes, but keeps the stored status when
	// it cannot state the range, and serves the whole object when the object
	// ends before the range starts.
	for _, tt := range []struct {
		name         string
		cacheHandle  int32
		status       int
		contentRange string
		body         string
	}{
		{"int range", intRange, 206, "bytes 2-5/*", "2345"},
		{"prefix", prefix, 200, "", "56789"},
		{"suffix", suffix, 200, "", "789"},
		{"past the end", pastTheEnd, 206, "bytes 20-30/*", "0123456789"},
	} {
		checkFound(t, inst, tt.name, tt.cacheHandle, tt.status, tt.contentRange, tt.body)
	}
}

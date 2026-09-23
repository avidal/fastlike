package fastlike

import (
	"cmp"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

// Guest memory layout shared by the HTTP cache storage tests.
const (
	httpCacheTestHandleOut   = 0
	httpCacheTestSecondOut   = 8
	httpCacheTestActionOut   = 16
	httpCacheTestNwrittenOut = 24
	httpCacheTestOptions     = 256
	httpCacheTestDataPtr     = 512
)

func newHTTPCacheStoreTestInstance() *Instance {
	inst := newHttpCacheHandleTestInstance()
	inst.bodies = &BodyHandles{}
	inst.memory.WriteUint64(httpCacheTestOptions, uint64(time.Minute))
	return inst
}

func httpCacheTestRequest(inst *Instance, method string, header http.Header) int32 {
	reqID, req := inst.requests.New()
	req.Method = method
	req.URL, _ = url.Parse("https://example.com/object")
	maps.Copy(req.Header, header)
	return int32(reqID)
}

func httpCacheTestResponse(inst *Instance, status int, header http.Header) int32 {
	respID, resp := inst.responses.New()
	resp.StatusCode = status
	resp.Header = header
	return int32(respID)
}

func httpCacheTransactionLookup(t *testing.T, inst *Instance, reqID int32) int32 {
	t.Helper()
	if status := inst.xqd_http_cache_transaction_lookup(reqID, 0, 0, httpCacheTestHandleOut); status != XqdStatusOK {
		t.Fatalf("transaction_lookup status = %d", status)
	}
	return int32(inst.memory.Uint32(httpCacheTestHandleOut))
}

func httpCachePlainLookup(t *testing.T, inst *Instance, reqID int32) int32 {
	t.Helper()
	if status := inst.xqd_http_cache_lookup(reqID, 0, 0, httpCacheTestHandleOut); status != XqdStatusOK {
		t.Fatalf("lookup status = %d", status)
	}
	return int32(inst.memory.Uint32(httpCacheTestHandleOut))
}

// newHTTPCacheTransaction returns an instance and the handle of a GET lookup
// that must insert.
func newHTTPCacheTransaction(t *testing.T) (*Instance, int32) {
	t.Helper()
	inst := newHTTPCacheStoreTestInstance()
	return inst, httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
}

func transactionInsert(t *testing.T, inst *Instance, cacheHandle, respID int32) int32 {
	t.Helper()
	if status := inst.xqd_http_cache_transaction_insert(cacheHandle, respID, 0, httpCacheTestOptions, httpCacheTestHandleOut); status != XqdStatusOK {
		t.Fatalf("transaction_insert status = %d", status)
	}
	return int32(inst.memory.Uint32(httpCacheTestHandleOut))
}

func insertAndStreamBack(t *testing.T, inst *Instance, cacheHandle, respID int32, optionsMask uint32) (insertBody, readback int32) {
	t.Helper()
	if status := inst.xqd_http_cache_transaction_insert_and_stream_back(cacheHandle, respID, optionsMask, httpCacheTestOptions, httpCacheTestHandleOut, httpCacheTestSecondOut); status != XqdStatusOK {
		t.Fatalf("insert_and_stream_back status = %d", status)
	}
	return int32(inst.memory.Uint32(httpCacheTestHandleOut)), int32(inst.memory.Uint32(httpCacheTestSecondOut))
}

func appendSource(t *testing.T, inst *Instance, insertBody int32, source io.ReadCloser) {
	t.Helper()
	srcID, _ := inst.bodies.NewReader(source)
	if status := inst.xqd_body_append(insertBody, int32(srcID)); status != XqdStatusOK {
		t.Fatalf("body_append status = %d", status)
	}
}

func closeBody(t *testing.T, inst *Instance, body int32) {
	t.Helper()
	if status := inst.xqd_body_close(body); status != XqdStatusOK {
		t.Fatalf("body_close status = %d", status)
	}
}

// storeThroughStreamBack stores a response like the fastly 0.13 SDK does and
// returns the readback handle.
func storeThroughStreamBack(t *testing.T, inst *Instance, cacheHandle, respID int32, body string) int32 {
	t.Helper()
	insertBody, readback := insertAndStreamBack(t, inst, cacheHandle, respID, 0)
	appendSource(t, inst, insertBody, io.NopCloser(strings.NewReader(body)))
	closeBody(t, inst, insertBody)
	return readback
}

func storeObject(t *testing.T, inst *Instance, header http.Header, body string) int32 {
	t.Helper()
	cacheHandle := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
	return storeThroughStreamBack(t, inst, cacheHandle, httpCacheTestResponse(inst, http.StatusOK, header), body)
}

func foundResponse(t *testing.T, inst *Instance, cacheHandle int32, transform uint32) (*ResponseHandle, string) {
	t.Helper()
	if status := inst.xqd_http_cache_get_found_response(cacheHandle, transform, httpCacheTestHandleOut, httpCacheTestSecondOut); status != XqdStatusOK {
		t.Fatalf("get_found_response status = %d", status)
	}
	resp := inst.responses.Get(int(inst.memory.Uint32(httpCacheTestHandleOut)))
	body, err := readWithin(inst.bodies.Get(int(inst.memory.Uint32(httpCacheTestSecondOut))))
	if err != nil {
		t.Fatalf("reading the found body: %v", err)
	}
	return resp, body
}

// readWithin fails instead of hanging when a cache body never completes.
func readWithin(r io.Reader) (string, error) {
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(r)
		done <- result{data, err}
	}()
	select {
	case res := <-done:
		return string(res.data), res.err
	case <-time.After(5 * time.Second):
		return "", errors.New("body did not complete")
	}
}

func TestHttpCacheStreamBackServesStoredResponse(t *testing.T) {
	inst, cacheHandle := newHTTPCacheTransaction(t)
	respID := httpCacheTestResponse(inst, http.StatusMovedPermanently, http.Header{"Location": {"/elsewhere"}, "X-Backend": {"a", "b"}})
	readback := storeThroughStreamBack(t, inst, cacheHandle, respID, "moved")

	if inst.responses.Get(int(respID)) != nil {
		t.Error("the inserted response handle is still open")
	}
	resp, body := foundResponse(t, inst, readback, 0)
	if resp.StatusCode != http.StatusMovedPermanently || body != "moved" {
		t.Errorf("got %d %q, want 301 %q", resp.StatusCode, body, "moved")
	}
	if got := resp.Header.Values("X-Backend"); len(got) != 2 || resp.Header.Get("Location") != "/elsewhere" {
		t.Errorf("stored headers %v", resp.Header)
	}
	if resp.Header.Get("Age") != "" || resp.Header.Get("Accept-Ranges") != "" {
		t.Errorf("an untransformed response got Age %q and Accept-Ranges %q", resp.Header.Get("Age"), resp.Header.Get("Accept-Ranges"))
	}

	// A later request is a hit with the same head and body.
	resp, body = foundResponse(t, inst, httpCachePlainLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil)), 1)
	if resp.StatusCode != http.StatusMovedPermanently || body != "moved" || resp.Header.Get("Location") != "/elsewhere" {
		t.Errorf("hit: got %d %q with Location %q", resp.StatusCode, body, resp.Header.Get("Location"))
	}
}

func TestHttpCacheTransactionInsertFinishesOnClose(t *testing.T) {
	inst, cacheHandle := newHTTPCacheTransaction(t)
	insertBody := transactionInsert(t, inst, cacheHandle, httpCacheTestResponse(inst, http.StatusOK, http.Header{"X-A": {"1"}}))
	_, _ = inst.memory.WriteAt([]byte("abc"), httpCacheTestDataPtr)
	if status := inst.xqd_body_write(insertBody, httpCacheTestDataPtr, 3, BodyWriteEndBack, httpCacheTestNwrittenOut); status != XqdStatusOK {
		t.Fatalf("body_write status = %d", status)
	}
	closeBody(t, inst, insertBody)

	resp, body := foundResponse(t, inst, httpCachePlainLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil)), 0)
	if body != "abc" || resp.Header.Get("X-A") != "1" {
		t.Errorf("got %q with X-A %q", body, resp.Header.Get("X-A"))
	}
}

func TestCacheObjectBodyLengthIsUnknown(t *testing.T) {
	inst, cacheHandle := newHTTPCacheTransaction(t)
	readback := storeThroughStreamBack(t, inst, cacheHandle, httpCacheTestResponse(inst, http.StatusOK, http.Header{}), "abc")
	if status := inst.xqd_http_cache_get_found_response(readback, 0, httpCacheTestHandleOut, httpCacheTestSecondOut); status != XqdStatusOK {
		t.Fatalf("get_found_response status = %d", status)
	}
	if status := inst.xqd_body_known_length(int32(inst.memory.Uint32(httpCacheTestSecondOut)), httpCacheTestNwrittenOut); status != XqdErrNone {
		t.Errorf("body_known_length status = %d, want %d", status, XqdErrNone)
	}
}

func TestHttpCacheAppendDoesNotWaitForTheSource(t *testing.T) {
	inst, cacheHandle := newHTTPCacheTransaction(t)
	insertBody, readback := insertAndStreamBack(t, inst, cacheHandle, httpCacheTestResponse(inst, http.StatusOK, http.Header{}), 0)

	// The source stays empty until the insert hostcalls have returned.
	backend, backendWriter := io.Pipe()
	srcID, _ := inst.bodies.NewReader(backend)
	_, _ = inst.memory.WriteAt([]byte("!?"), httpCacheTestDataPtr)
	hostcalls := make(chan int32, 4)
	go func() {
		hostcalls <- inst.xqd_body_append(insertBody, int32(srcID))
		hostcalls <- inst.xqd_body_write(insertBody, httpCacheTestDataPtr, 1, BodyWriteEndBack, httpCacheTestNwrittenOut)
		hostcalls <- inst.xqd_body_write(insertBody, httpCacheTestDataPtr+1, 1, BodyWriteEndBack, httpCacheTestNwrittenOut)
		hostcalls <- inst.xqd_body_close(insertBody)
	}()
	for range 4 {
		select {
		case status := <-hostcalls:
			if status != XqdStatusOK {
				t.Fatalf("insert body hostcall status = %d", status)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("an insert body hostcall waited for the appended source")
		}
	}

	go func() {
		_, _ = backendWriter.Write([]byte("streamed"))
		_ = backendWriter.Close()
	}()
	if _, body := foundResponse(t, inst, readback, 0); body != "streamed!?" {
		t.Errorf("read back %q, want %q", body, "streamed!?")
	}
}

func TestHttpCacheTruncatedSourceIsDiscarded(t *testing.T) {
	inst, cacheHandle := newHTTPCacheTransaction(t)
	insertBody, readback := insertAndStreamBack(t, inst, cacheHandle, httpCacheTestResponse(inst, http.StatusOK, http.Header{}), 0)
	backend, backendWriter := io.Pipe()
	appendSource(t, inst, insertBody, backend)
	closeBody(t, inst, insertBody)
	go func() {
		_, _ = backendWriter.Write([]byte("par"))
		_ = backendWriter.CloseWithError(io.ErrUnexpectedEOF)
	}()

	if status := inst.xqd_http_cache_get_found_response(readback, 0, httpCacheTestHandleOut, httpCacheTestSecondOut); status != XqdStatusOK {
		t.Fatalf("get_found_response status = %d", status)
	}
	body, err := readWithin(inst.bodies.Get(int(inst.memory.Uint32(httpCacheTestSecondOut))))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read back %q with error %v, want %v", body, err, io.ErrUnexpectedEOF)
	}

	hit := httpCachePlainLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
	if status := inst.xqd_http_cache_get_found_response(hit, 0, httpCacheTestHandleOut, httpCacheTestSecondOut); status != XqdErrNone {
		t.Errorf("a truncated object was stored: get_found_response status = %d", status)
	}
}

func TestHttpCacheFoundResponseTransformForClient(t *testing.T) {
	lastModified := "Tue, 22 Sep 2026 10:00:00 GMT"
	stored := http.Header{
		"Etag":           {`"v1"`},
		"Last-Modified":  {lastModified},
		"Date":           {time.Now().Add(-10 * time.Second).UTC().Format(http.TimeFormat)},
		"Cache-Control":  {"max-age=60"},
		"Content-Length": {"4"},
		"X-Other":        {"1"},
	}
	tests := []struct {
		name         string
		method       string
		header       http.Header
		wantStatus   int
		wantBody     string
		wantDropped  []string
		wantRetained []string
	}{
		{"plain", http.MethodGet, nil, http.StatusOK, "body", nil, []string{"X-Other", "Age"}},
		{"matching etag", http.MethodGet, http.Header{"If-None-Match": {`"v0","v1"`}}, http.StatusNotModified, "", []string{"X-Other", "Age", "Content-Length"}, []string{"Etag", "Cache-Control", "Date", "Last-Modified"}},
		{"etag list with spaces", http.MethodGet, http.Header{"If-None-Match": {`"v0", "v1"`}}, http.StatusOK, "body", nil, nil},
		{"weak etag", http.MethodGet, http.Header{"If-None-Match": {`W/"v1"`}}, http.StatusOK, "body", nil, nil},
		{"etag wins over date", http.MethodGet, http.Header{"If-None-Match": {`"v2"`}, "If-Modified-Since": {lastModified}}, http.StatusOK, "body", nil, nil},
		{"not modified since", http.MethodGet, http.Header{"If-Modified-Since": {lastModified}}, http.StatusNotModified, "", nil, nil},
		{"modified since", http.MethodGet, http.Header{"If-Modified-Since": {"Mon, 21 Sep 2026 10:00:00 GMT"}}, http.StatusOK, "body", nil, nil},
		{"head", http.MethodHead, nil, http.StatusOK, "", nil, []string{"Content-Length", "X-Other"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := newHTTPCacheStoreTestInstance()
			cacheHandle := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, tt.method, tt.header))
			readback := storeThroughStreamBack(t, inst, cacheHandle, httpCacheTestResponse(inst, http.StatusOK, stored.Clone()), "body")

			resp, body := foundResponse(t, inst, readback, 1)
			if resp.StatusCode != tt.wantStatus || body != tt.wantBody {
				t.Errorf("got %d %q, want %d %q", resp.StatusCode, body, tt.wantStatus, tt.wantBody)
			}
			if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
				t.Errorf("Accept-Ranges = %q", got)
			}
			for _, name := range tt.wantDropped {
				if resp.Header.Get(name) != "" {
					t.Errorf("%s = %q, want it dropped", name, resp.Header.Get(name))
				}
			}
			for _, name := range tt.wantRetained {
				if resp.Header.Get(name) == "" {
					t.Errorf("%s is missing", name)
				}
			}
			if resp.StatusCode == http.StatusOK {
				if age, _ := parseDeltaSeconds(resp.Header.Get("Age")); age < 10 || age > 12 {
					t.Errorf("Age = %q, want about ten seconds", resp.Header.Get("Age"))
				}
			}
		})
	}
}

func TestHttpCacheNotModifiedHeader(t *testing.T) {
	header := notModifiedHeader(http.Header{
		"Last-Modified": {"Tue, 22 Sep 2026 10:00:00 GMT"},
		"Vary":          {"Accept", "Accept-Encoding"},
		"X-Other":       {"1"},
	})
	if header.Get("Last-Modified") == "" || len(header.Values("Vary")) != 2 || header.Get("X-Other") != "" {
		t.Errorf("304 header %v", header)
	}
}

func TestHttpCacheRevalidation(t *testing.T) {
	inst := newHTTPCacheStoreTestInstance()
	storeObject(t, inst, http.Header{
		"Etag":           {`"v1"`},
		"Last-Modified":  {"Tue, 22 Sep 2026 10:00:00 GMT"},
		"Content-Length": {"5"},
		"X-Version":      {"1"},
	}, "hello")

	reqID := httpCacheTestRequest(inst, http.MethodHead, http.Header{"Range": {"bytes=0-1"}, "If-Match": {`"v1"`}})
	cacheHandle := httpCacheTransactionLookup(t, inst, reqID)
	if status := inst.xqd_http_cache_get_suggested_backend_request(cacheHandle, httpCacheTestSecondOut); status != XqdStatusOK {
		t.Fatalf("get_suggested_backend_request status = %d", status)
	}
	backendReq := inst.requests.Get(int(inst.memory.Uint32(httpCacheTestSecondOut)))
	if backendReq.Method != http.MethodGet || backendReq.Header.Get("Range") != "" {
		t.Errorf("revalidation request %s with Range %q", backendReq.Method, backendReq.Header.Get("Range"))
	}
	if backendReq.Header.Get("If-None-Match") != `"v1"` || backendReq.Header.Get("If-Modified-Since") != "Tue, 22 Sep 2026 10:00:00 GMT" {
		t.Errorf("revalidation validators: %v", backendReq.Header)
	}
	if backendReq.Header.Get("If-Match") != `"v1"` {
		t.Error("the client's own conditional header was removed")
	}

	notModified := httpCacheTestResponse(inst, http.StatusNotModified, http.Header{
		"Etag":           {`"v1"`},
		"X-Version":      {"2"},
		"Content-Length": {"0"},
		"Connection":     {"X-Hop"},
		"X-Hop":          {"1"},
	})
	if status := inst.xqd_http_cache_prepare_response_for_storage(cacheHandle, notModified, httpCacheTestActionOut, httpCacheTestSecondOut); status != XqdStatusOK {
		t.Fatalf("prepare_response_for_storage status = %d", status)
	}
	if action := inst.memory.Uint32(httpCacheTestActionOut); action != HttpStorageActionUpdate {
		t.Fatalf("storage action = %d, want update", action)
	}
	prepared := int32(inst.memory.Uint32(httpCacheTestSecondOut))
	merged := inst.responses.Get(int(prepared))
	if merged.StatusCode != http.StatusOK || merged.Header.Get("X-Version") != "2" || merged.Header.Get("Content-Length") != "5" || merged.Header.Get("X-Hop") != "" {
		t.Errorf("merged response: %d %v", merged.StatusCode, merged.Header)
	}

	if status := inst.xqd_http_cache_transaction_update_and_return_fresh(cacheHandle, prepared, 0, httpCacheTestOptions, httpCacheTestSecondOut); status != XqdStatusOK {
		t.Fatalf("update_and_return_fresh status = %d", status)
	}
	fresh := int32(inst.memory.Uint32(httpCacheTestSecondOut))
	if resp, body := foundResponse(t, inst, fresh, 0); resp.Header.Get("X-Version") != "2" || body != "hello" {
		t.Errorf("fresh handle: X-Version %q, body %q", resp.Header.Get("X-Version"), body)
	}
	// The handle that found the object keeps the head it found.
	if resp, _ := foundResponse(t, inst, cacheHandle, 0); resp.Header.Get("X-Version") != "1" {
		t.Errorf("original handle: X-Version %q, want 1", resp.Header.Get("X-Version"))
	}
}

func TestHttpCachePrepareResponseForStorage(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		status     int
		header     http.Header
		wantAction uint32
		wantKept   []string
		wantGone   []string
	}{
		{
			name:   "hop by hop and qualified directives",
			status: http.StatusOK,
			header: http.Header{
				"Connection":         {"X-Hop, keep-alive"},
				"X-Hop":              {"1"},
				"Keep-Alive":         {"timeout=5"},
				"Transfer-Encoding":  {"chunked"},
				"Upgrade":            {"h2c"},
				"Proxy-Authenticate": {"Basic"},
				"Cache-Control":      {`max-age=60, no-cache="Set-Cookie, X-Secret"`, `private="X-User"`},
				"Set-Cookie":         {"a=b"},
				"X-Secret":           {"1"},
				"X-User":             {"1"},
				"X-Keep":             {"1"},
			},
			wantAction: HttpStorageActionInsert,
			wantKept:   []string{"X-Keep", "Cache-Control"},
			wantGone:   []string{"Connection", "X-Hop", "Keep-Alive", "Transfer-Encoding", "Upgrade", "Proxy-Authenticate", "Set-Cookie", "X-Secret", "X-User"},
		},
		{
			name:       "surrogate control wins",
			status:     http.StatusOK,
			header:     http.Header{"Surrogate-Control": {"max-age=60"}, "Cache-Control": {`no-cache="X-Secret"`}, "X-Secret": {"1"}},
			wantAction: HttpStorageActionInsert,
			wantKept:   []string{"X-Secret"},
		},
		{
			name:       "malformed cache control",
			status:     http.StatusOK,
			header:     http.Header{"Cache-Control": {`no-cache="X-Secret", bad value`}, "X-Secret": {"1"}},
			wantAction: HttpStorageActionDoNotStore,
			wantKept:   []string{"X-Secret"},
		},
		{
			name:       "first directive wins",
			status:     http.StatusOK,
			header:     http.Header{"Cache-Control": {`no-cache="", no-cache="X-B", no-cache="X-C"`}, "X-B": {"1"}, "X-C": {"1"}},
			wantAction: HttpStorageActionInsert,
			wantKept:   []string{"X-C"},
			wantGone:   []string{"X-B"},
		},
		{
			name:       "server error is left alone",
			status:     http.StatusInternalServerError,
			header:     http.Header{"Connection": {"X-Hop"}, "X-Hop": {"1"}},
			wantAction: HttpStorageActionDoNotStore,
			wantKept:   []string{"Connection", "X-Hop"},
		},
		{
			name:       "lookup made with post",
			method:     http.MethodPost,
			status:     http.StatusOK,
			header:     http.Header{"Connection": {"X-Hop"}, "X-Hop": {"1"}},
			wantAction: HttpStorageActionDoNotStore,
			wantKept:   []string{"X-Hop"},
		},
		{
			name:       "304 with nothing to revalidate",
			status:     http.StatusNotModified,
			header:     http.Header{},
			wantAction: HttpStorageActionDoNotStore,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := newHTTPCacheStoreTestInstance()
			method := cmp.Or(tt.method, http.MethodGet)
			cacheHandle := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, method, nil))
			respID := httpCacheTestResponse(inst, tt.status, tt.header)
			if status := inst.xqd_http_cache_prepare_response_for_storage(cacheHandle, respID, httpCacheTestActionOut, httpCacheTestSecondOut); status != XqdStatusOK {
				t.Fatalf("prepare_response_for_storage status = %d", status)
			}
			if action := inst.memory.Uint32(httpCacheTestActionOut); action != tt.wantAction {
				t.Errorf("storage action = %d, want %d", action, tt.wantAction)
			}
			prepared := inst.responses.Get(int(inst.memory.Uint32(httpCacheTestSecondOut)))
			for _, name := range tt.wantKept {
				if prepared.Header.Get(name) == "" {
					t.Errorf("%s was removed", name)
				}
			}
			for _, name := range tt.wantGone {
				if prepared.Header.Get(name) != "" {
					t.Errorf("%s = %q, want it removed", name, prepared.Header.Get(name))
				}
			}
			if len(tt.wantGone) > 0 && inst.responses.Get(int(respID)).Header.Get(tt.wantGone[0]) == "" {
				t.Error("the original response lost its headers")
			}
		})
	}
}

func TestStorageActionFor(t *testing.T) {
	tests := []struct {
		name   string
		method string
		status int
		header http.Header
		want   uint32
	}{
		{"heuristically cacheable", http.MethodGet, http.StatusNotFound, nil, HttpStorageActionInsert},
		{"head", http.MethodHead, http.StatusOK, nil, HttpStorageActionInsert},
		{"post", http.MethodPost, http.StatusOK, nil, HttpStorageActionDoNotStore},
		{"no freshness", http.MethodGet, http.StatusFound, nil, HttpStorageActionDoNotStore},
		{"max-age", http.MethodGet, http.StatusFound, http.Header{"Cache-Control": {"max-age=60"}}, HttpStorageActionInsert},
		{"invalid max-age", http.MethodGet, http.StatusFound, http.Header{"Cache-Control": {"max-age=soon"}}, HttpStorageActionDoNotStore},
		{"s-maxage", http.MethodGet, http.StatusFound, http.Header{"Cache-Control": {"s-maxage=60"}}, HttpStorageActionInsert},
		{"expires", http.MethodGet, http.StatusFound, http.Header{"Expires": {"garbage"}}, HttpStorageActionInsert},
		{"public error", http.MethodGet, http.StatusInternalServerError, http.Header{"Cache-Control": {"public"}}, HttpStorageActionInsert},
		{"no-store", http.MethodGet, http.StatusOK, http.Header{"Cache-Control": {"no-store"}}, HttpStorageActionRecordUncacheable},
		{"no-store with an argument", http.MethodGet, http.StatusOK, http.Header{"Cache-Control": {"no-store=1"}}, HttpStorageActionInsert},
		{"no-store understood", http.MethodGet, http.StatusOK, http.Header{"Cache-Control": {"no-store, must-understand"}}, HttpStorageActionInsert},
		{"must-understand unknown status", http.MethodGet, http.StatusFound, http.Header{"Cache-Control": {"must-understand, max-age=60"}}, HttpStorageActionDoNotStore},
		{"private", http.MethodGet, http.StatusOK, http.Header{"Cache-Control": {"private"}}, HttpStorageActionRecordUncacheable},
		{"qualified private", http.MethodGet, http.StatusOK, http.Header{"Cache-Control": {`private="X-User"`}}, HttpStorageActionInsert},
		{"private redirect", http.MethodGet, http.StatusFound, http.Header{"Cache-Control": {"private, max-age=60"}}, HttpStorageActionDoNotStore},
		{"surrogate control wins", http.MethodGet, http.StatusOK, http.Header{"Surrogate-Control": {"no-store"}, "Cache-Control": {"public"}}, HttpStorageActionRecordUncacheable},
		{"malformed", http.MethodGet, http.StatusOK, http.Header{"Cache-Control": {"public private"}}, HttpStorageActionDoNotStore},
		{"partial content", http.MethodGet, http.StatusPartialContent, http.Header{"Cache-Control": {"max-age=60"}}, HttpStorageActionDoNotStore},
		{"not modified", http.MethodGet, http.StatusNotModified, http.Header{"Cache-Control": {"max-age=60"}}, HttpStorageActionDoNotStore},
		{"informational", http.MethodGet, http.StatusEarlyHints, nil, HttpStorageActionDoNotStore},
	}
	for _, tt := range tests {
		if got := storageActionFor(tt.method, &http.Response{StatusCode: tt.status, Header: tt.header}); got != tt.want {
			t.Errorf("%s: storage action %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestHttpCacheHonorsVaryRules(t *testing.T) {
	tests := []struct {
		name   string
		rule   string
		stored func(*http.Request)
		lookup map[string]func(*http.Request)
	}{
		{
			name:   "single value",
			rule:   "Accept-Encoding",
			stored: func(r *http.Request) { r.Header.Set("Accept-Encoding", "gzip") },
			lookup: map[string]func(*http.Request){
				"same":  func(r *http.Request) { r.Header.Set("Accept-Encoding", "gzip") },
				"other": func(r *http.Request) { r.Header.Set("Accept-Encoding", "br") },
			},
		},
		{
			name:   "host kept apart by Go",
			rule:   "Host",
			stored: func(r *http.Request) { r.Host = "a.example" },
			lookup: map[string]func(*http.Request){
				"same":  func(r *http.Request) { r.Host = "a.example" },
				"other": func(r *http.Request) { r.Host = "b.example" },
			},
		},
		{
			name:   "repeated values keep their order",
			rule:   "X-V",
			stored: func(r *http.Request) { r.Header["X-V"] = []string{"a", "b"} },
			lookup: map[string]func(*http.Request){
				"same":  func(r *http.Request) { r.Header["X-V"] = []string{"a", "b"} },
				"other": func(r *http.Request) { r.Header["X-V"] = []string{"b", "a"} },
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := newHTTPCacheStoreTestInstance()
			reqID := httpCacheTestRequest(inst, http.MethodGet, nil)
			tt.stored(inst.requests.Get(int(reqID)).Request)
			cacheHandle := httpCacheTransactionLookup(t, inst, reqID)
			_, _ = inst.memory.WriteAt([]byte(tt.rule), httpCacheTestDataPtr)
			inst.memory.WriteUint32(httpCacheTestOptions+8, httpCacheTestDataPtr)
			inst.memory.WriteUint32(httpCacheTestOptions+12, uint32(len(tt.rule)))
			insertBody, _ := insertAndStreamBack(t, inst, cacheHandle, httpCacheTestResponse(inst, http.StatusOK, http.Header{}), HttpCacheWriteOptionsMaskVaryRule)
			closeBody(t, inst, insertBody)

			for label, want := range map[string]int32{"same": XqdStatusOK, "other": XqdErrNone} {
				reqID := httpCacheTestRequest(inst, http.MethodGet, nil)
				tt.lookup[label](inst.requests.Get(int(reqID)).Request)
				hit := httpCachePlainLookup(t, inst, reqID)
				if status := inst.xqd_http_cache_get_found_response(hit, 0, httpCacheTestHandleOut, httpCacheTestSecondOut); status != want {
					t.Errorf("%s request: get_found_response status %d, want %d", label, status, want)
				}
			}
		})
	}
}

func TestCacheSinkRefusesBytesReadBeforeAnAbandon(t *testing.T) {
	inst, cacheHandle := newHTTPCacheTransaction(t)
	insertBody := transactionInsert(t, inst, cacheHandle, httpCacheTestResponse(inst, http.StatusOK, http.Header{}))
	sink := inst.bodies.Get(int(insertBody)).sink.(*cacheBodySink)

	// The second read is abandoned from under the copy before it returns.
	reads := 0
	source := readerFunc(func(p []byte) (int, error) {
		reads++
		if reads == 2 {
			_ = sink.Abandon()
			return copy(p, "late"), nil
		}
		if reads > 2 {
			return 0, io.EOF
		}
		return copy(p, "a"), nil
	})
	appendSource(t, inst, insertBody, io.NopCloser(source))
	if _, failed := sink.obj.waitForWriteComplete(); !failed {
		t.Fatal("the abandoned fill did not fail")
	}
	time.Sleep(10 * time.Millisecond)
	sink.obj.WriteCond.L.Lock()
	stored := sink.obj.Body.String()
	sink.obj.WriteCond.L.Unlock()
	if stored != "a" {
		t.Errorf("object holds %q after the abandon, want %q", stored, "a")
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestHttpCacheCloseInvalidatesHandle(t *testing.T) {
	inst, cacheHandle := newHTTPCacheTransaction(t)
	if status := inst.xqd_http_cache_close(cacheHandle); status != XqdStatusOK {
		t.Fatalf("close status = %d", status)
	}
	if status := inst.xqd_http_cache_get_state(cacheHandle, httpCacheTestSecondOut); status != XqdErrInvalidHandle {
		t.Errorf("get_state after close = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := inst.xqd_http_cache_close(cacheHandle); status != XqdErrInvalidHandle {
		t.Errorf("second close = %d, want %d", status, XqdErrInvalidHandle)
	}

	// The abandoned obligation does not stay pending for the next lookup.
	next := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
	if status := inst.xqd_http_cache_get_state(next, httpCacheTestSecondOut); status != XqdStatusOK {
		t.Fatalf("get_state status = %d", status)
	}
	if state := inst.memory.Uint32(httpCacheTestSecondOut); state&CacheLookupStateMustInsertOrUpdate == 0 {
		t.Errorf("state after the leader closed = %#x, want the obligation", state)
	}
}

func TestStoredResponseCurrentAge(t *testing.T) {
	requestTime := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		age  string
		date string
		want time.Duration
	}{
		// corrected age value 5+2, apparent age 2, resident time 8
		{"age header", "5", "Wed, 23 Sep 2026 10:00:00 GMT", 15 * time.Second},
		{"only the first age value", "5, 30", "", 15 * time.Second},
		{"malformed age", " 5", "", 10 * time.Second},
		// apparent age 32 beats the corrected age value 2
		{"old date", "", "Wed, 23 Sep 2026 09:59:30 GMT", 40 * time.Second},
		{"unparseable date", "", "yesterday", 10 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}
			if tt.age != "" {
				header.Set("Age", tt.age)
			}
			if tt.date != "" {
				header.Set("Date", tt.date)
			}
			stored := &storedResponse{status: 200, header: header, requestTime: requestTime, responseTime: requestTime.Add(2 * time.Second)}
			if got := stored.currentAge(requestTime.Add(10 * time.Second)); got != tt.want {
				t.Errorf("current age %v, want %v", got, tt.want)
			}
		})
	}

	for age, want := range map[time.Duration]string{
		0:                          "0",
		-time.Second:               "0",
		10*time.Second + 1:         "11",
		10 * time.Second:           "10",
		200 * 365 * 24 * time.Hour: "2147483648",
	} {
		if got := ageHeaderValue(age); got != want {
			t.Errorf("ageHeaderValue(%v) = %q, want %q", age, got, want)
		}
	}
}

func TestParseCacheDirectives(t *testing.T) {
	tests := []struct {
		value string
		want  []cacheDirective
	}{
		{"max-age=60, public", []cacheDirective{{"max-age", "60", true}, {"public", "", false}}},
		{", ,Max-Age = 60 ,", []cacheDirective{{"max-age", "60", true}}},
		{`private="a\"b\\c", no-store`, []cacheDirective{{"private", `a"bc`, true}, {"no-store", "", false}}},
		{" public", nil},
		{"", nil},
		{",", nil},
		{"max-age=", nil},
		{`private="unterminated`, nil},
		{"public private", nil},
		{"max-age=6 0", nil},
	}
	for _, tt := range tests {
		got, ok := parseCacheDirectives(tt.value)
		if ok != (tt.want != nil) || len(got) != len(tt.want) {
			t.Errorf("parseCacheDirectives(%q) = %v, %v", tt.value, got, ok)
			continue
		}
		for n := range got {
			if got[n] != tt.want[n] {
				t.Errorf("parseCacheDirectives(%q)[%d] = %v, want %v", tt.value, n, got[n], tt.want[n])
			}
		}
	}

	for value, want := range map[string]int{"a, b,c": 3, ",a": 1, "a ,b": -1, "a b": -1, "": -1, `"a"`: -1} {
		names, ok := parseFieldNames(value)
		if (want < 0 && ok) || (want >= 0 && (!ok || len(names) != want)) {
			t.Errorf("parseFieldNames(%q) = %v, %v", value, names, ok)
		}
	}
}

func TestFreshenStoredHeaderUsesStoredDirectives(t *testing.T) {
	merged := freshenStoredHeader(
		http.Header{"Cache-Control": {`no-cache="X-A"`}, "Content-Length": {"5"}, "X-Old": {"1", "2"}},
		http.Header{"Cache-Control": {`no-cache="X-B"`}, "Content-Length": {"0"}, "X-A": {"1"}, "X-B": {"1"}, "X-Old": {"3"}},
	)
	if merged.Get("X-A") != "" || merged.Get("X-B") != "1" {
		t.Errorf("X-A %q, X-B %q: want the stored directive to decide", merged.Get("X-A"), merged.Get("X-B"))
	}
	if merged.Get("Content-Length") != "5" || merged.Get("Cache-Control") != `no-cache="X-B"` {
		t.Errorf("merged header %v", merged)
	}
	if got := merged.Values("X-Old"); len(got) != 1 || got[0] != "3" {
		t.Errorf("X-Old = %v, want the 304's values only", got)
	}
}

func TestHttpCacheCallsRejectCoreCacheHandles(t *testing.T) {
	inst := newHTTPCacheStoreTestInstance()
	_, _ = inst.memory.WriteAt([]byte("key"), httpCacheTestDataPtr)
	if status := inst.xqd_cache_transaction_lookup(httpCacheTestDataPtr, 3, 0, 0, httpCacheTestHandleOut); status != XqdStatusOK {
		t.Fatalf("core transaction_lookup status = %d", status)
	}
	coreHandle := int32(inst.memory.Uint32(httpCacheTestHandleOut))

	respID := httpCacheTestResponse(inst, http.StatusOK, http.Header{})
	if status := inst.xqd_http_cache_transaction_insert_and_stream_back(coreHandle, respID, 0, httpCacheTestOptions, httpCacheTestHandleOut, httpCacheTestSecondOut); status != XqdErrInvalidHandle {
		t.Errorf("insert_and_stream_back status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if inst.responses.Get(int(respID)) != nil {
		t.Error("the response survived an insert that production would have taken it for")
	}
	statuses := map[string]int32{
		"get_found_response":           inst.xqd_http_cache_get_found_response(coreHandle, 1, httpCacheTestHandleOut, httpCacheTestSecondOut),
		"get_state":                    inst.xqd_http_cache_get_state(coreHandle, httpCacheTestSecondOut),
		"prepare_response_for_storage": inst.xqd_http_cache_prepare_response_for_storage(coreHandle, httpCacheTestResponse(inst, http.StatusOK, http.Header{}), httpCacheTestActionOut, httpCacheTestSecondOut),
		"transaction_abandon":          inst.xqd_http_cache_transaction_abandon(coreHandle),
		"close":                        inst.xqd_http_cache_close(coreHandle),
	}
	for call, status := range statuses {
		if status != XqdErrInvalidHandle {
			t.Errorf("%s status = %d, want %d", call, status, XqdErrInvalidHandle)
		}
	}
	if inst.cacheHandles.Get(int(coreHandle)) == nil {
		t.Error("the HTTP close consumed a core cache handle")
	}
}

func TestCacheSinkClosesAFailedSourceOnce(t *testing.T) {
	inst, cacheHandle := newHTTPCacheTransaction(t)
	insertBody := transactionInsert(t, inst, cacheHandle, httpCacheTestResponse(inst, http.StatusOK, http.Header{}))
	source := &recordingCloser{body: io.MultiReader(strings.NewReader("par"), iotest.ErrReader(io.ErrUnexpectedEOF))}
	appendSource(t, inst, insertBody, source)

	deadline := time.Now().Add(5 * time.Second)
	for source.closed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	if got := source.closed.Load(); got != 1 {
		t.Errorf("source closed %d times, want once", got)
	}
}

func TestCacheSinkExpiresStalledFills(t *testing.T) {
	window := cacheFillWindow
	cacheFillWindow = 20 * time.Millisecond
	t.Cleanup(func() { cacheFillWindow = window })

	inst := newHTTPCacheStoreTestInstance()
	insert := func(key string, source io.ReadCloser) *CachedObject {
		t.Helper()
		_, _ = inst.memory.WriteAt([]byte(key), httpCacheTestDataPtr)
		if status := inst.xqd_cache_insert(httpCacheTestDataPtr, int32(len(key)), 0, httpCacheTestOptions, httpCacheTestHandleOut); status != XqdStatusOK {
			t.Fatalf("cache_insert status = %d", status)
		}
		insertBody := int32(inst.memory.Uint32(httpCacheTestHandleOut))
		appendSource(t, inst, insertBody, source)
		closeBody(t, inst, insertBody)
		return inst.cache.Lookup([]byte(key), nil).Object
	}
	stalled, stalledWriter := io.Pipe()
	stalledObject := insert("stalled", stalled)
	doneObject := insert("done", io.NopCloser(strings.NewReader("complete")))
	if _, failed := doneObject.waitForWriteComplete(); failed {
		t.Fatal("the finished fill failed")
	}

	abandoned := make(chan bool, 1)
	go func() {
		_, failed := stalledObject.waitForWriteComplete()
		abandoned <- failed
	}()
	select {
	case failed := <-abandoned:
		if !failed {
			t.Fatal("the stalled fill completed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stalled fill was not abandoned")
	}
	if _, err := stalledWriter.Write([]byte("late")); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("writing to the stalled source after the window: %v, want %v", err, io.ErrClosedPipe)
	}
	if inst.cache.Lookup([]byte("stalled"), nil).Object != nil {
		t.Error("the stalled object is still in the cache")
	}
	time.Sleep(2 * cacheFillWindow)
	if entry := inst.cache.Lookup([]byte("done"), nil); entry.Object != doneObject || doneObject.writeFailed() {
		t.Error("the finished fill did not survive the window")
	}
}

package fastlike

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
)

const (
	phraseOut         = 1024
	phraseNwrittenOut = 1020
	phraseIn          = 2048
)

func reasonPhrase(i *Instance, handle int32, maxlen int32) (int32, uint32, string) {
	i.memory.PutUint32(0xdead, phraseNwrittenOut)
	status := i.xqd_resp_reason_phrase_get(handle, phraseOut, maxlen, phraseNwrittenOut)
	n := i.memory.Uint32(phraseNwrittenOut)
	if status != XqdStatusOK {
		return status, n, ""
	}
	return status, n, string(i.memory.Data()[phraseOut : phraseOut+n])
}

func expectReasonPhrase(t *testing.T, i *Instance, handle int32, want string) {
	t.Helper()
	status, _, got := reasonPhrase(i, handle, maxReasonPhraseLen)
	if status != XqdStatusOK || got != want {
		t.Fatalf("reason_phrase_get = (%d, %q), want (%d, %q)", status, got, XqdStatusOK, want)
	}
}

func setStatusAndReasonPhrase(i *Instance, handle int32, status int32, phrase string) int32 {
	copy(i.memory.Data()[phraseIn:], phrase)
	return i.xqd_resp_set_status_and_reason_phrase(handle, status, phraseIn, int32(len(phrase)))
}

func newReasonPhraseTestInstance() *Instance {
	i := newPendingTestInstance()
	i.memory = &Memory{ByteMemory(make([]byte, 64*1024))}
	return i
}

func TestStatusSetResetsThePhrase(t *testing.T) {
	i := newReasonPhraseTestInstance()
	if status := i.xqd_resp_new(0); status != XqdStatusOK {
		t.Fatalf("resp_new status = %d", status)
	}
	handle := int32(i.memory.Uint32(0))
	expectReasonPhrase(t, i, handle, "OK")

	if status := setStatusAndReasonPhrase(i, handle, 200, "Totally Fine"); status != XqdStatusOK {
		t.Fatalf("set_status_and_reason_phrase status = %d", status)
	}
	// The last four are named differently by Go and by production.
	for code, want := range map[int32]string{
		404: "Not Found",
		599: "",
		203: "Non Authoritative Information",
		413: "Payload Too Large",
		414: "URI Too Long",
		416: "Range Not Satisfiable",
	} {
		if status := i.xqd_resp_status_set(handle, code); status != XqdStatusOK {
			t.Fatalf("status_set status = %d", status)
		}
		expectReasonPhrase(t, i, handle, want)
	}
}

func TestReasonPhraseGet(t *testing.T) {
	i := newReasonPhraseTestInstance()
	handle, resp := i.responses.New()

	for _, tc := range []struct {
		phrase     string
		maxlen     int32
		wantStatus int32
		wantN      uint32
	}{
		{"Not Found", 0, XqdErrBufferLength, 9},
		{"Not Found", 8, XqdErrBufferLength, 9},
		{"Not Found", 9, XqdStatusOK, 9},
		{"", 0, XqdStatusOK, 0},
	} {
		resp.reasonPhrase = tc.phrase
		status, n, got := reasonPhrase(i, int32(handle), tc.maxlen)
		if status != tc.wantStatus || n != tc.wantN || (status == XqdStatusOK && got != tc.phrase) {
			t.Errorf("%q with maxlen %d = (%d, %d, %q), want (%d, %d)", tc.phrase, tc.maxlen, status, n, got, tc.wantStatus, tc.wantN)
		}
	}

	resp.reasonPhrase = "OK"
	if status, _, _ := reasonPhrase(i, 42, 100); status != XqdErrInvalidHandle {
		t.Errorf("unknown handle status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_resp_reason_phrase_get(int32(handle), int32(i.memory.Len()-1), 100, phraseNwrittenOut); status != XqdErrInvalidArgument {
		t.Errorf("out of bounds buffer status = %d, want %d", status, XqdErrInvalidArgument)
	}
	// Production copies the phrase before it checks nwritten_out.
	if status := i.xqd_resp_reason_phrase_get(int32(handle), phraseOut, 100, phraseNwrittenOut+1); status != XqdErrBadAlignment {
		t.Errorf("misaligned nwritten status = %d, want %d", status, XqdErrBadAlignment)
	}
	if got := string(i.memory.Data()[phraseOut : phraseOut+2]); got != "OK" {
		t.Errorf("buffer holds %q after a misaligned nwritten, want the phrase", got)
	}
}

func TestSetStatusAndReasonPhrase(t *testing.T) {
	i := newReasonPhraseTestInstance()
	handle, resp := i.responses.New()

	long := strings.Repeat("a", maxReasonPhraseLen)
	for phrase, want := range map[string]string{
		"Slow Down":   "Slow Down",
		"caf\xe9":     "caf\xe9",
		"":            "",
		long + "more": long,
		// Only the part we keep has to be valid.
		long + "\r\n": long,
	} {
		if status := setStatusAndReasonPhrase(i, int32(handle), 429, phrase); status != XqdStatusOK {
			t.Fatalf("set_status_and_reason_phrase(%.20q) status = %d", phrase, status)
		}
		if resp.StatusCode != 429 {
			t.Fatalf("status = %d, want 429", resp.StatusCode)
		}
		expectReasonPhrase(t, i, int32(handle), want)
	}
}

func TestSetStatusAndReasonPhraseRejectsBadInput(t *testing.T) {
	i := newReasonPhraseTestInstance()
	handle, resp := i.responses.New()

	for name, status := range map[string]int32{
		"CRLF":          setStatusAndReasonPhrase(i, int32(handle), 500, "line1\r\nInjected: header"),
		"NUL":           setStatusAndReasonPhrase(i, int32(handle), 500, "bad\x00"),
		"status 99":     setStatusAndReasonPhrase(i, int32(handle), 99, "Fine"),
		"status 1000":   setStatusAndReasonPhrase(i, int32(handle), 1000, "Fine"),
		"out of bounds": i.xqd_resp_set_status_and_reason_phrase(int32(handle), 200, int32(i.memory.Len()-2), 4),
	} {
		if status != XqdErrInvalidArgument {
			t.Errorf("%s: status = %d, want %d", name, status, XqdErrInvalidArgument)
		}
	}
	if status := setStatusAndReasonPhrase(i, 42, 200, "Fine"); status != XqdErrInvalidHandle {
		t.Errorf("unknown handle status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if resp.StatusCode != 200 || resp.reasonPhrase != "OK" {
		t.Errorf("rejected calls changed the response to %d %q", resp.StatusCode, resp.reasonPhrase)
	}
}

func TestBackendReasonPhrase(t *testing.T) {
	long := strings.Repeat("x", maxReasonPhraseLen+10)
	for _, tc := range []struct {
		statusLine string
		want       string
	}{
		{"HTTP/1.1 200 Something\tdifferent ", "Something\tdifferent "},
		{"HTTP/1.1 200 ", "OK"},
		{"HTTP/1.1 200", "OK"},
		{"HTTP/1.1 413 ", "Payload Too Large"},
		{"HTTP/1.1 599 ", ""},
		{"HTTP/1.0 200 caf\xe9", "caf\xe9"},
		{"HTTP/1.1 200 " + long, long[:maxReasonPhraseLen]},
	} {
		t.Run(tc.statusLine[:min(len(tc.statusLine), 40)], func(t *testing.T) {
			i := newRawOriginInstance(t, 0, func(conn net.Conn) {
				_, _ = fmt.Fprintf(conn, "%s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", tc.statusLine)
			})
			respHandle, _ := sendToOrigin(t, i)
			expectReasonPhrase(t, i, respHandle, tc.want)
		})
	}

	http2 := &http.Response{StatusCode: 413, Status: "413 Request Entity Too Large", ProtoMajor: 2}
	if got := backendReasonPhrase(http2); got != "Payload Too Large" {
		t.Errorf("HTTP/2 phrase = %q, want %q", got, "Payload Too Large")
	}
}

func TestOriginPhraseOnlyGoesWithItsStatus(t *testing.T) {
	// Like http.TimeoutHandler, this wrapper answers with its own status.
	i := newStreamTestInstance(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = ProxyReasonPhrase(&http.Response{StatusCode: 200, Status: "200 Origin Phrase", ProtoMajor: 1, Request: r})
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	respHandle, _ := sendToOrigin(t, i)
	expectReasonPhrase(t, i, respHandle, "Service Unavailable")
}

func TestHttpCacheReplaysTheStoredPhrase(t *testing.T) {
	inst := newHTTPCacheStoreTestInstance()
	cacheHandle := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
	respID := httpCacheTestResponse(inst, 200, http.Header{"Etag": {`"v1"`}})
	inst.responses.Get(int(respID)).reasonPhrase = "Original"
	storeThroughStreamBack(t, inst, cacheHandle, respID, "body")

	for _, transform := range []uint32{0, 1} {
		lookup := httpCachePlainLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
		if resp, _ := foundResponse(t, inst, lookup, transform); resp.reasonPhrase != "Original" {
			t.Errorf("transform %d: phrase %q", transform, resp.reasonPhrase)
		}
	}

	lookup := httpCachePlainLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, http.Header{"If-None-Match": {`"v1"`}}))
	if resp, _ := foundResponse(t, inst, lookup, 1); resp.StatusCode != http.StatusNotModified || resp.reasonPhrase != "Not Modified" {
		t.Errorf("conditional hit: %d %q, want 304 \"Not Modified\"", resp.StatusCode, resp.reasonPhrase)
	}

	// A 304 from the backend keeps the stored phrase.
	revalidation := httpCacheTransactionLookup(t, inst, httpCacheTestRequest(inst, http.MethodGet, nil))
	notModified := httpCacheTestResponse(inst, http.StatusNotModified, http.Header{"Etag": {`"v1"`}})
	if status := inst.xqd_http_cache_prepare_response_for_storage(revalidation, notModified, httpCacheTestActionOut, httpCacheTestHandleOut); status != XqdStatusOK {
		t.Fatalf("prepare_response_for_storage status = %d", status)
	}
	prepared := inst.responses.Get(int(inst.memory.Uint32(httpCacheTestHandleOut)))
	if action := inst.memory.Uint32(httpCacheTestActionOut); action != HttpStorageActionUpdate || prepared.StatusCode != 200 || prepared.reasonPhrase != "Original" {
		t.Errorf("revalidation = action %d, %d %q, want action %d, 200 \"Original\"", action, prepared.StatusCode, prepared.reasonPhrase, HttpStorageActionUpdate)
	}
}

func TestHttpCachePreparedResponsePhrase(t *testing.T) {
	for cacheControl, want := range map[string]string{
		"max-age=60": "Backend Phrase",
		"no-store":   "",
	} {
		inst, cacheHandle := newHTTPCacheTransaction(t)
		respID := httpCacheTestResponse(inst, 200, http.Header{"Cache-Control": {cacheControl}})
		inst.responses.Get(int(respID)).reasonPhrase = "Backend Phrase"
		if status := inst.xqd_http_cache_prepare_response_for_storage(cacheHandle, respID, httpCacheTestActionOut, httpCacheTestHandleOut); status != XqdStatusOK {
			t.Fatalf("prepare_response_for_storage status = %d", status)
		}
		if got := inst.responses.Get(int(inst.memory.Uint32(httpCacheTestHandleOut))).reasonPhrase; got != want {
			t.Errorf("%s: prepared phrase = %q, want %q", cacheControl, got, want)
		}
	}
}

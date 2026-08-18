package fastlike

import (
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newPendingTestInstance() *Instance {
	return &Instance{
		requests:        &RequestHandles{},
		responses:       &ResponseHandles{},
		bodies:          &BodyHandles{},
		pendingRequests: &PendingRequestHandles{},
		memory:          &Memory{ByteMemory(make([]byte, 8192))},
		abilog:          log.New(io.Discard, "", 0),
	}
}

// writeStr copies s into guest memory at off and returns (addr, size).
func writeStr(t *testing.T, i *Instance, off int64, s string) (int32, int32) {
	t.Helper()
	if _, err := i.memory.WriteAt([]byte(s), off); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	return int32(off), int32(len(s))
}

func TestPendingReqHeaderInsertTargetRouting(t *testing.T) {
	cases := []struct {
		name       string
		target     int32
		wantInResp bool
		wantInErr  bool
	}{
		{"any", PendingResponseKindAny, true, true},
		{"response", PendingResponseKindResponse, true, false},
		{"error", PendingResponseKindError, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i := newPendingTestInstance()
			phid, pr := i.pendingRequests.New()

			na, ns := writeStr(t, i, 100, "X-Foo")
			va, vs := writeStr(t, i, 200, "bar")

			if st := i.xqd_pending_req_header_insert(int32(phid), na, ns, va, vs, tc.target); st != XqdStatusOK {
				t.Fatalf("insert status = %d", st)
			}

			respHas := pr.headersResp.insert.Get("X-Foo") == "bar"
			errHas := pr.headersErr.insert.Get("X-Foo") == "bar"
			if respHas != tc.wantInResp {
				t.Errorf("headersResp has X-Foo = %v, want %v", respHas, tc.wantInResp)
			}
			if errHas != tc.wantInErr {
				t.Errorf("headersErr has X-Foo = %v, want %v", errHas, tc.wantInErr)
			}
		})
	}
}

func TestPendingReqHeaderMutationsRejectNameCountLimitAtomically(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*Instance, int32, int32, int32, int32, int32, int32) int32
	}{
		{
			name: "insert",
			call: func(i *Instance, handle, nameAddr, nameSize, valueAddr, valueSize, target int32) int32 {
				return i.xqd_pending_req_header_insert(handle, nameAddr, nameSize, valueAddr, valueSize, target)
			},
		},
		{
			name: "append",
			call: func(i *Instance, handle, nameAddr, nameSize, valueAddr, valueSize, target int32) int32 {
				return i.xqd_pending_req_header_append(handle, nameAddr, nameSize, valueAddr, valueSize, target)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := newPendingTestInstance()
			phid, pr := i.pendingRequests.New()
			pr.headersResp.insert = headerMapAtNameLimit()
			nameAddr, nameSize := writeStr(t, i, 100, "X-Added")
			valueAddr, valueSize := writeStr(t, i, 200, "value")

			if status := tc.call(i, int32(phid), nameAddr, nameSize, valueAddr, valueSize, PendingResponseKindAny); status != XqdErrInvalidArgument {
				t.Fatalf("status = %d, want %d", status, XqdErrInvalidArgument)
			}
			if len(pr.headersResp.insert) != maxHTTPHeaderNameCount {
				t.Fatalf("response queue count = %d, want %d", len(pr.headersResp.insert), maxHTTPHeaderNameCount)
			}
			if !pr.headersErr.empty() {
				t.Fatalf("error queue changed after rejected mutation: %+v", pr.headersErr)
			}
		})
	}
}

func TestPendingReqHeaderInvalidTarget(t *testing.T) {
	i := newPendingTestInstance()
	phid, _ := i.pendingRequests.New()
	na, ns := writeStr(t, i, 100, "X-Foo")
	va, vs := writeStr(t, i, 200, "bar")

	if st := i.xqd_pending_req_header_insert(int32(phid), na, ns, va, vs, 99); st != XqdErrInvalidArgument {
		t.Errorf("insert bad target = %d, want %d", st, XqdErrInvalidArgument)
	}
	if st := i.xqd_pending_req_header_append(int32(phid), na, ns, va, vs, 99); st != XqdErrInvalidArgument {
		t.Errorf("append bad target = %d, want %d", st, XqdErrInvalidArgument)
	}
	if st := i.xqd_pending_req_header_remove(int32(phid), na, ns, 99); st != XqdErrInvalidArgument {
		t.Errorf("remove bad target = %d, want %d", st, XqdErrInvalidArgument)
	}
}

func TestPendingReqHeaderInvalidHandle(t *testing.T) {
	i := newPendingTestInstance()
	na, ns := writeStr(t, i, 100, "X-Foo")
	va, vs := writeStr(t, i, 200, "bar")

	if st := i.xqd_pending_req_header_insert(999, na, ns, va, vs, PendingResponseKindAny); st != XqdErrInvalidHandle {
		t.Errorf("insert bad handle = %d, want %d", st, XqdErrInvalidHandle)
	}
	if st := i.xqd_pending_req_header_remove(999, na, ns, PendingResponseKindAny); st != XqdErrInvalidHandle {
		t.Errorf("remove bad handle = %d, want %d", st, XqdErrInvalidHandle)
	}
}

func TestPendingReqHeaderRejectsInvalidSyntax(t *testing.T) {
	i := newPendingTestInstance()
	phid, pr := i.pendingRequests.New()
	badNameAddr, badNameSize := writeStr(t, i, 100, "Bad Name")
	valueAddr, valueSize := writeStr(t, i, 200, "value")
	if st := i.xqd_pending_req_header_insert(int32(phid), badNameAddr, badNameSize, valueAddr, valueSize, PendingResponseKindAny); st != XqdErrInvalidArgument {
		t.Fatalf("insert with invalid name = %d, want %d", st, XqdErrInvalidArgument)
	}

	nameAddr, nameSize := writeStr(t, i, 300, "X-Good")
	badValueAddr, badValueSize := writeStr(t, i, 400, "bad\nvalue")
	if st := i.xqd_pending_req_header_append(int32(phid), nameAddr, nameSize, badValueAddr, badValueSize, PendingResponseKindAny); st != XqdErrInvalidArgument {
		t.Fatalf("append with invalid value = %d, want %d", st, XqdErrInvalidArgument)
	}
	if !pr.headersResp.empty() || !pr.headersErr.empty() {
		t.Fatal("invalid pending-header mutations changed queued headers")
	}
}

func TestPendingReqWaitConsumesPendingHandle(t *testing.T) {
	i := newPendingTestInstance()
	phid, pr := i.pendingRequests.New()
	pr.Complete(&http.Response{
		Status:     "204 No Content",
		StatusCode: http.StatusNoContent,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil)

	if status := i.xqd_pending_req_wait(int32(phid), 100, 104); status != XqdStatusOK {
		t.Fatalf("pending_req_wait status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_pending_req_wait(int32(phid), 108, 112); status != XqdErrInvalidHandle {
		t.Fatalf("second pending_req_wait status = %d, want %d", status, XqdErrInvalidHandle)
	}
}

func TestPendingReqPollConsumesReadyHandle(t *testing.T) {
	i := newPendingTestInstance()
	phid, pr := i.pendingRequests.New()
	pr.Complete(&http.Response{
		Status:     "204 No Content",
		StatusCode: http.StatusNoContent,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil)

	if status := i.xqd_pending_req_poll(int32(phid), 100, 104, 108); status != XqdStatusOK {
		t.Fatalf("pending_req_poll status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_pending_req_poll(int32(phid), 112, 116, 120); status != XqdErrInvalidHandle {
		t.Fatalf("second pending_req_poll status = %d, want %d", status, XqdErrInvalidHandle)
	}
}

func TestPendingReqSelectSuppressesSelectedRequestError(t *testing.T) {
	i := newPendingTestInstance()
	phid, pr := i.pendingRequests.New()
	pr.Complete(nil, errors.New("backend failed"))
	i.memory.PutUint32(uint32(phid), 0)

	if status := i.xqd_pending_req_select(0, 1, 100, 104, 108); status != XqdStatusOK {
		t.Fatalf("pending_req_select status = %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint32(100); got != 0 {
		t.Fatalf("selected index = %d, want 0", got)
	}
	if got := i.memory.Uint32(104); got != HandleInvalid {
		t.Fatalf("response handle = %#x, want invalid %#x", got, uint32(HandleInvalid))
	}
	if got := i.memory.Uint32(108); got != HandleInvalid {
		t.Fatalf("body handle = %#x, want invalid %#x", got, uint32(HandleInvalid))
	}
}

func TestPendingReqSelectV2WritesOkDetailForNonCachingError(t *testing.T) {
	i := newPendingTestInstance()
	phid, pr := i.pendingRequests.New()
	pr.Complete(nil, errors.New("backend failed"))
	i.memory.PutUint32(uint32(phid), 0)
	i.memory.PutUint32(0xdeadbeef, 100)

	if status := i.xqd_pending_req_select_v2(0, 1, 100, 104, 108, 112); status != XqdStatusOK {
		t.Fatalf("pending_req_select_v2 status = %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint32(100); got != SendErrorDetailOk {
		t.Fatalf("error detail tag = %d, want %d", got, SendErrorDetailOk)
	}
}

func TestPendingReqSelectV2WritesCachingSendErrorDetail(t *testing.T) {
	i := newPendingTestInstance()
	phid, pr := i.pendingRequests.New()
	pr.Complete(nil, &cachingSendError{err: errors.New("backend failed")})
	i.memory.PutUint32(uint32(phid), 0)
	i.memory.PutUint32(0xdeadbeef, 100)

	if status := i.xqd_pending_req_select_v2(0, 1, 100, 104, 108, 112); status != XqdStatusOK {
		t.Fatalf("pending_req_select_v2 status = %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint32(100); got != SendErrorDetailInternalError {
		t.Fatalf("error detail tag = %d, want %d", got, SendErrorDetailInternalError)
	}
}

func TestPendingReqSelectRejectsRuntimeMaximum(t *testing.T) {
	i := newPendingTestInstance()
	if status := i.xqd_pending_req_select(0, maxPendingRequests, 100, 104, 108); status != XqdErrBufferLength {
		t.Fatalf("pending_req_select status = %d, want %d", status, XqdErrBufferLength)
	}
}

func TestPendingReqWaitAppliesQueuedHeaders(t *testing.T) {
	i := newPendingTestInstance()
	phid, pr := i.pendingRequests.New()

	// Queue an insert, an append, and a removal targeting the real response.
	na, ns := writeStr(t, i, 100, "X-Added")
	va, vs := writeStr(t, i, 200, "yes")
	if st := i.xqd_pending_req_header_insert(int32(phid), na, ns, va, vs, PendingResponseKindResponse); st != XqdStatusOK {
		t.Fatalf("insert: %d", st)
	}
	na2, ns2 := writeStr(t, i, 300, "X-Multi")
	va2, vs2 := writeStr(t, i, 400, "a")
	if st := i.xqd_pending_req_header_append(int32(phid), na2, ns2, va2, vs2, PendingResponseKindResponse); st != XqdStatusOK {
		t.Fatalf("append: %d", st)
	}
	na3, ns3 := writeStr(t, i, 500, "X-Drop")
	if st := i.xqd_pending_req_header_remove(int32(phid), na3, ns3, PendingResponseKindResponse); st != XqdStatusOK {
		t.Fatalf("remove: %d", st)
	}

	// Resolve the request with a response carrying a header we asked to drop.
	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Header:     http.Header{"X-Drop": {"please-remove"}, "X-Keep": {"untouched"}},
		Body:       io.NopCloser(http.NoBody),
	}
	pr.Complete(resp, nil)

	// Reap via wait; out-params at 0 (resp handle) and 4 (body handle).
	if st := i.xqd_pending_req_wait(int32(phid), 0, 4); st != XqdStatusOK {
		t.Fatalf("wait status = %d", st)
	}
	whid := i.memory.Uint32(0)
	wh := i.responses.Get(int(whid))
	if wh == nil {
		t.Fatal("no response handle produced")
	}

	if got := wh.Header.Get("X-Added"); got != "yes" {
		t.Errorf("X-Added = %q, want yes", got)
	}
	if got := wh.Header.Values("X-Multi"); len(got) != 1 || got[0] != "a" {
		t.Errorf("X-Multi = %v, want [a]", got)
	}
	if got := wh.Header.Get("X-Drop"); got != "" {
		t.Errorf("X-Drop = %q, want empty (removed)", got)
	}
	if got := wh.Header.Get("X-Keep"); got != "untouched" {
		t.Errorf("X-Keep = %q, want untouched", got)
	}
}

func TestPendingReqPollAppliesQueuedHeaders(t *testing.T) {
	i := newPendingTestInstance()
	phid, pr := i.pendingRequests.New()

	na, ns := writeStr(t, i, 100, "X-Polled")
	va, vs := writeStr(t, i, 200, "ok")
	if st := i.xqd_pending_req_header_insert(int32(phid), na, ns, va, vs, PendingResponseKindAny); st != XqdStatusOK {
		t.Fatalf("insert: %d", st)
	}

	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(http.NoBody),
	}
	pr.Complete(resp, nil)

	// is_done at 0, resp handle at 4, body handle at 8.
	if st := i.xqd_pending_req_poll(int32(phid), 0, 4, 8); st != XqdStatusOK {
		t.Fatalf("poll status = %d", st)
	}
	if done := i.memory.Uint32(0); done != 1 {
		t.Fatalf("is_done = %d, want 1", done)
	}
	wh := i.responses.Get(int(i.memory.Uint32(4)))
	if wh == nil || wh.Header.Get("X-Polled") != "ok" {
		t.Errorf("X-Polled not applied on poll path: %+v", wh)
	}
}

func TestSendDownstreamPendingSuccess(t *testing.T) {
	i := newPendingTestInstance()
	rec := httptest.NewRecorder()
	i.ds_response = rec

	phid, pr := i.pendingRequests.New()

	na, ns := writeStr(t, i, 100, "X-Edge")
	va, vs := writeStr(t, i, 200, "hit")
	if st := i.xqd_pending_req_header_insert(int32(phid), na, ns, va, vs, PendingResponseKindResponse); st != XqdStatusOK {
		t.Fatalf("insert: %d", st)
	}

	resp := &http.Response{
		Status:     "201 Created",
		StatusCode: 201,
		Header:     http.Header{"Content-Type": {"text/plain"}},
		Trailer:    http.Header{"X-Checksum": {"complete"}},
		Body:       io.NopCloser(strings.NewReader("hello")),
	}
	pr.Complete(resp, nil)

	if st := i.xqd_resp_send_downstream_pending(int32(phid)); st != XqdStatusOK {
		t.Fatalf("send_downstream_pending status = %d", st)
	}

	res := rec.Result()
	if res.StatusCode != 201 {
		t.Errorf("status = %d, want 201", res.StatusCode)
	}
	if got := res.Header.Get("X-Edge"); got != "hit" {
		t.Errorf("X-Edge = %q, want hit", got)
	}
	body, _ := io.ReadAll(res.Body)
	if string(body) != "hello" {
		t.Errorf("body = %q, want hello", string(body))
	}
	if got := res.Trailer.Get("X-Checksum"); got != "complete" {
		t.Errorf("X-Checksum trailer = %q, want complete", got)
	}
}

func TestSendDownstreamPendingFailureSynthesizes502(t *testing.T) {
	i := newPendingTestInstance()
	rec := httptest.NewRecorder()
	i.ds_response = rec

	phid, pr := i.pendingRequests.New()

	// Queue an error-target header and a response-target header; only the
	// error one should survive a failure.
	na, ns := writeStr(t, i, 100, "X-Err")
	va, vs := writeStr(t, i, 200, "synth")
	if st := i.xqd_pending_req_header_insert(int32(phid), na, ns, va, vs, PendingResponseKindError); st != XqdStatusOK {
		t.Fatalf("insert err: %d", st)
	}
	na2, ns2 := writeStr(t, i, 300, "X-Ok")
	va2, vs2 := writeStr(t, i, 400, "nope")
	if st := i.xqd_pending_req_header_insert(int32(phid), na2, ns2, va2, vs2, PendingResponseKindResponse); st != XqdStatusOK {
		t.Fatalf("insert ok: %d", st)
	}

	pr.Complete(nil, io.ErrUnexpectedEOF)

	if st := i.xqd_resp_send_downstream_pending(int32(phid)); st != XqdStatusOK {
		t.Fatalf("send_downstream_pending status = %d", st)
	}

	res := rec.Result()
	if res.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", res.StatusCode)
	}
	if got := res.Header.Get("X-Err"); got != "synth" {
		t.Errorf("X-Err = %q, want synth", got)
	}
	if got := res.Header.Get("X-Ok"); got != "" {
		t.Errorf("X-Ok = %q, want empty on failure", got)
	}
}

func TestSendDownstreamPendingInvalidHandle(t *testing.T) {
	i := newPendingTestInstance()
	i.ds_response = httptest.NewRecorder()
	if st := i.xqd_resp_send_downstream_pending(999); st != XqdErrInvalidHandle {
		t.Errorf("status = %d, want %d", st, XqdErrInvalidHandle)
	}
}

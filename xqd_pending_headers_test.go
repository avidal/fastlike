package fastlike

import (
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

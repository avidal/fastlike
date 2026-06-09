package fastlike

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fastlike.dev/profile"
)

func newInstrumentedInstance(t *testing.T) *Instance {
	t.Helper()
	store := profile.NewProfileStore()
	store.SetBackendCap(4)
	i := &Instance{
		profile: &profile.Binding{Store: store, ModuleID: "test"},
	}
	r, _ := http.NewRequest("GET", "http://x/", nil)
	i.trace = store.NewRequestTrace("test", r)
	return i
}

func TestBackendRecorderOk(t *testing.T) {
	i := newInstrumentedInstance(t)
	u, _ := url.Parse("http://api.example.com/path?secret=1")
	rec, dropped := i.startBackendCall("api", "POST", u, 7)
	if dropped {
		t.Fatal("first call should not be dropped")
	}
	rec.completeBackendCall(http.StatusTeapot, false, nil)

	if got := len(i.trace.BackendCalls); got != 1 {
		t.Fatalf("expected 1 backend call, got %d", got)
	}
	call := i.trace.BackendCalls[0]
	if call.Outcome != profile.BackendOutcomeOk {
		t.Errorf("outcome: %d, want ok", call.Outcome)
	}
	if call.Status != http.StatusTeapot {
		t.Errorf("status: %d, want 418", call.Status)
	}
	if !strings.Contains(call.URLRedacted, "api.example.com") || strings.Contains(call.URLRedacted, "secret=") {
		t.Errorf("URL redaction wrong: %q", call.URLRedacted)
	}
	if call.PendingID != 7 {
		t.Errorf("pending id: %d", call.PendingID)
	}
}

func TestBackendRecorderCancelled(t *testing.T) {
	i := newInstrumentedInstance(t)
	u, _ := url.Parse("http://x/")
	rec, _ := i.startBackendCall("api", "GET", u, 0)
	rec.completeBackendCall(0, true, context.Canceled)
	if got := i.trace.BackendCalls[0].Outcome; got != profile.BackendOutcomeCancelled {
		t.Errorf("outcome: %d, want cancelled", got)
	}
}

func TestBackendRecorderNetworkError(t *testing.T) {
	i := newInstrumentedInstance(t)
	u, _ := url.Parse("http://x/")
	rec, _ := i.startBackendCall("api", "GET", u, 0)
	rec.completeBackendCall(0, false, errors.New("connect refused"))
	if got := i.trace.BackendCalls[0].Outcome; got != profile.BackendOutcomeNetworkError {
		t.Errorf("outcome: %d, want network-error", got)
	}
}

func TestBackendRecorderSyntheticFailure(t *testing.T) {
	i := newInstrumentedInstance(t)
	u, _ := url.Parse("http://x/")
	rec, _ := i.startBackendCall("api", "GET", u, 0)

	// Simulate the reliability wrapper flipping the flag through the ctx.
	ctx := rec.installHTTPTrace(context.Background(), false)
	flag, ok := ctx.Value(syntheticFailureCtxKey{}).(*bool)
	if !ok || flag == nil {
		t.Fatalf("synthetic flag missing from ctx")
	}
	*flag = true

	rec.completeBackendCall(http.StatusBadGateway, false, nil)
	if got := i.trace.BackendCalls[0].Outcome; got != profile.BackendOutcomeSyntheticFailure {
		t.Errorf("outcome: %d, want synthetic-failure", got)
	}
}

func TestBackendRecorderOrphan(t *testing.T) {
	i := newInstrumentedInstance(t)
	u, _ := url.Parse("http://x/")
	rec, _ := i.startBackendCall("api", "GET", u, 1)
	rec.markOrphaned()
	if got := i.trace.BackendCalls[0].Outcome; got != profile.BackendOutcomeOrphaned {
		t.Errorf("outcome: %d, want orphaned", got)
	}
}

func TestBackendRecorderIncomplete(t *testing.T) {
	i := newInstrumentedInstance(t)
	u, _ := url.Parse("http://x/")
	rec, _ := i.startBackendCall("api", "GET", u, 1)
	rec.markIncomplete()
	if got := i.trace.BackendCalls[0].Outcome; got != profile.BackendOutcomeIncomplete {
		t.Errorf("outcome: %d, want incomplete", got)
	}
}

func TestBackendRecorderCap(t *testing.T) {
	i := newInstrumentedInstance(t)
	u, _ := url.Parse("http://x/")
	for k := 0; k < 4; k++ {
		_, dropped := i.startBackendCall("api", "GET", u, uint32(k))
		if dropped {
			t.Fatalf("call %d dropped before cap", k)
		}
	}
	_, dropped := i.startBackendCall("api", "GET", u, 99)
	if !dropped {
		t.Fatal("5th call should have been dropped")
	}
	if i.trace.DroppedBackendCalls != 1 {
		t.Errorf("DroppedBackendCalls: %d, want 1", i.trace.DroppedBackendCalls)
	}
	if len(i.trace.BackendCalls) != 4 {
		t.Errorf("BackendCalls len: %d, want 4", len(i.trace.BackendCalls))
	}
}

func TestBackendRecorderNoTraceNoPhases(t *testing.T) {
	// profile==nil → recorder is a no-op.
	i := &Instance{}
	u, _ := url.Parse("http://x/")
	rec, dropped := i.startBackendCall("api", "GET", u, 0)
	if dropped {
		t.Fatal("no-op recorder should not report dropped")
	}
	// Should not panic.
	rec.completeBackendCall(200, false, nil)
	rec.markOrphaned()
	rec.markIncomplete()
	rec.disableLateWrites()
}

func TestBackendRecorderLateWriteSuppressed(t *testing.T) {
	i := newInstrumentedInstance(t)
	u, _ := url.Parse("http://x/")
	rec, _ := i.startBackendCall("api", "GET", u, 0)
	rec.markOrphaned()
	rec.disableLateWrites()
	// Goroutine fires completeBackendCall after the sweep; must not flip
	// outcome away from orphaned.
	rec.completeBackendCall(200, false, nil)
	if got := i.trace.BackendCalls[0].Outcome; got != profile.BackendOutcomeOrphaned {
		t.Errorf("late write overwrote outcome: %d", got)
	}
}

type recordingCloser struct {
	closed atomic.Int32
	body   io.Reader
}

func (r *recordingCloser) Read(p []byte) (int, error) { return r.body.Read(p) }
func (r *recordingCloser) Close() error {
	r.closed.Add(1)
	return nil
}

func TestPendingRequestCompleteHookFiresOnComplete(t *testing.T) {
	pr := &PendingRequest{done: make(chan struct{})}
	var called atomic.Int32
	pr.setCompleteHook(func(*http.Response, error) { called.Add(1) })

	pr.Complete(&http.Response{Body: &recordingCloser{body: strings.NewReader("")}}, nil)
	if called.Load() != 1 {
		t.Fatalf("hook not called after Complete; got %d", called.Load())
	}
}

func TestPendingRequestCompleteHookFiresImmediatelyWhenAlreadyDone(t *testing.T) {
	pr := &PendingRequest{done: make(chan struct{})}
	pr.Complete(&http.Response{Body: &recordingCloser{body: strings.NewReader("")}}, nil)

	var called atomic.Int32
	pr.setCompleteHook(func(*http.Response, error) { called.Add(1) })
	if called.Load() != 1 {
		t.Fatalf("hook should have fired synchronously; got %d", called.Load())
	}
}

func TestPendingRequestHookSingleCloseNoHandoff(t *testing.T) {
	// Pattern A: guest never converted the response to a handle. The
	// hook owns the close.
	pr := &PendingRequest{done: make(chan struct{})}
	body := &recordingCloser{body: strings.NewReader("hello")}
	pr.setCompleteHook(func(resp *http.Response, _ error) {
		if pr.bodyClosed.CompareAndSwap(false, true) {
			_ = resp.Body.Close()
		}
	})
	pr.Complete(&http.Response{Body: body}, nil)
	if got := body.closed.Load(); got != 1 {
		t.Fatalf("body close count = %d, want 1", got)
	}
}

func TestPendingRequestHookSkippedWhenHandedOff(t *testing.T) {
	// Pattern B: guest converted to handle; the wait/poll/select path
	// pre-flips bodyClosed before installing the hook. The hook then sees
	// true and skips.
	pr := &PendingRequest{done: make(chan struct{})}
	body := &recordingCloser{body: strings.NewReader("hi")}
	pr.bodyClosed.Store(true)
	pr.setCompleteHook(func(resp *http.Response, _ error) {
		if pr.bodyClosed.CompareAndSwap(false, true) {
			_ = resp.Body.Close()
		}
	})
	pr.Complete(&http.Response{Body: body}, nil)
	if got := body.closed.Load(); got != 0 {
		t.Fatalf("hook erroneously closed already-handed-off body (%d times)", got)
	}
}

func TestPendingRequestObserveWait(t *testing.T) {
	pr := &PendingRequest{
		done:     make(chan struct{}),
		recorder: &backendCallRecorder{},
	}
	start := time.Now().Add(-50 * time.Millisecond)
	if pr.waitObserved() {
		t.Fatal("waitObserved should be false initially")
	}
	pr.observeWait(start)
	if !pr.waitObserved() {
		t.Fatal("waitObserved should be true after first observe")
	}
	first := pr.waitObservedAtNano.Load()

	time.Sleep(time.Millisecond)
	pr.observeWait(start)
	if pr.waitObservedAtNano.Load() != first {
		t.Fatal("second observe should not change the timestamp")
	}
}

func TestInstallHTTPTraceNilReceiverDoesNotPanic(t *testing.T) {
	var rec *backendCallRecorder
	got := rec.installHTTPTrace(context.Background(), true)
	if got == nil {
		t.Fatal("nil receiver should return a usable context, not nil")
	}
}

func TestBackendRecorderHandlerPanicProducesNetworkError(t *testing.T) {
	// Simulates the goroutine's defer recover: a handler panic gets
	// converted to err via completeBackendCall(..., handlerErr) which
	// classifies the call as a network error (not Ok, not Incomplete).
	i := newInstrumentedInstance(t)
	u, _ := url.Parse("http://x/")
	rec, _ := i.startBackendCall("api", "GET", u, 0)

	// Synthesise what the goroutine's defer recover would do.
	panicErr := errors.New("backend handler panic: nil deref")
	rec.completeBackendCall(0, false, panicErr)

	if got := i.trace.BackendCalls[0].Outcome; got != profile.BackendOutcomeNetworkError {
		t.Errorf("outcome after panic: got %d, want network-error", got)
	}
	if got := i.trace.BackendCalls[0].Status; got != 0 {
		t.Errorf("status after panic: got %d, want 0", got)
	}
}

func TestPendingRequestObserveWaitNoRecorder(t *testing.T) {
	pr := &PendingRequest{done: make(chan struct{})}
	pr.observeWait(time.Now())
	if pr.waitObserved() {
		t.Fatal("observeWait without recorder must remain a no-op")
	}
}

func TestRedactURLStripsQueryAndUserinfo(t *testing.T) {
	cases := map[string]string{
		"http://user:pass@host/path?q=1":    "http://host/path",
		"https://api.example.com/v1/things": "https://api.example.com/v1/things",
		"http://localhost":                  "http://localhost",
	}
	for in, want := range cases {
		u, err := url.Parse(in)
		if err != nil {
			t.Fatalf("parse %q: %v", in, err)
		}
		if got := redactURL(u); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBodyCloseRaceLateCompleteVsSweep(t *testing.T) {
	// The one race that actually exists in real code is between an
	// incomplete-path send goroutine racing the sweep that installs the
	// hook. setCompleteHook either grabs and fires the pending response
	// itself, or installs the hook so Complete fires it later. Either way
	// the body must close exactly once.
	for run := 0; run < 200; run++ {
		pr := &PendingRequest{done: make(chan struct{})}
		body := &recordingCloser{body: strings.NewReader("")}

		var wg sync.WaitGroup
		wg.Add(2)
		// Send goroutine: completes the request.
		go func() {
			defer wg.Done()
			pr.Complete(&http.Response{Body: body}, nil)
		}()
		// Sweep: installs the body-close hook concurrently.
		go func() {
			defer wg.Done()
			installBodyCloseHook(pr)
		}()
		wg.Wait()

		if got := body.closed.Load(); got != 1 {
			t.Fatalf("run %d: orphan/incomplete body close count = %d, want exactly 1", run, got)
		}
	}
}

package fastlike

import (
	"context"
	"crypto/tls"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// backendCallRecorder is the per-PendingRequest handle the recorder uses to
// own a single BackendCall slot through its entire lifecycle. The slot is
// allocated synchronously by startBackendCall before any goroutine is
// launched, so the goroutine only mutates fields it owns and the finalizer
// reads everything under callMu without racing the goroutine.
type backendCallRecorder struct {
	trace *RequestTrace // nil when profiling is disabled or the cap was hit
	index int           // index into trace.BackendCalls; -1 when cap was hit
	// call is a direct pointer into the trace's BackendCalls backing
	// array. The slice is pre-allocated to backendCap at trace creation,
	// so the pointer is stable for the lifetime of the trace and async
	// completion goroutines never read trace.BackendCalls concurrently
	// with the wasm goroutine's append.
	call *BackendCall

	mu sync.Mutex // guards mutations after construction; finalizer reads under this

	// noLateWrites is set when the trace has been finalized. Goroutine
	// callbacks check this before mutating the BackendCall slot; resource
	// cleanup (body close) ignores it. The per-PendingRequest bodyClosed
	// atomic on PendingRequest itself owns the body-close arbitration; the
	// recorder does not duplicate that state.
	noLateWrites atomic.Bool

	// syntheticFlag is the pointer the reliability wrapper toggles. The
	// recorder reads it after the handler returns.
	syntheticFlag *bool

	// firstByteAt and connectStart are stamped by the httptrace.ClientTrace
	// callbacks the recorder installs; phases are derived at outcome time.
	startedAt    time.Time
	dnsStart     time.Time
	dnsDone      time.Time
	connectStart time.Time
	connectDone  time.Time
	tlsStart     time.Time
	tlsDone      time.Time
	firstByteAt  time.Time
}

// startBackendCall allocates a BackendCall slot on the active trace and
// returns a recorder bound to it. When profiling is disabled, or when the
// per-request cap has been reached, it returns a stub recorder that still
// satisfies the API but writes nothing. The dropped flag is true when the
// cap was hit so callers can attribute the drop to DroppedBackendCalls.
func (i *Instance) startBackendCall(name, method string, target *url.URL, pendingID uint32) (*backendCallRecorder, bool) {
	if i.trace == nil || i.profile == nil {
		return nopBackendRecorder(), false
	}
	t := i.trace
	if len(t.BackendCalls) >= cap(t.BackendCalls) {
		t.DroppedBackendCalls++
		return nopBackendRecorder(), true
	}
	t.BackendCalls = append(t.BackendCalls, BackendCall{
		PendingID:   pendingID,
		Name:        name,
		Method:      method,
		URLRedacted: redactURL(target),
		Started:     time.Since(t.WallStart).Nanoseconds(),
		Outcome:     BackendOutcomeIncomplete,
	})
	flag := false
	last := len(t.BackendCalls) - 1
	return &backendCallRecorder{
		trace:         t,
		index:         last,
		call:          &t.BackendCalls[last],
		syntheticFlag: &flag,
		startedAt:     time.Now(),
	}, false
}

// nopBackendRecorder returns a recorder that swallows every observation.
// Used when profiling is disabled or the per-request cap was hit.
func nopBackendRecorder() *backendCallRecorder {
	flag := false
	return &backendCallRecorder{index: -1, syntheticFlag: &flag}
}

// installHTTPTrace attaches DNS / connect / TLS / TTFB callbacks to ctx
// when the recorder is active and the backend has a transport fastlike can
// observe. The returned context flows through the handler chain via the
// outgoing *http.Request.
//
// The nil-receiver guard must split from the other early-exit conditions:
// the early-return branch references r.syntheticFlag, which would
// dereference nil under the old combined check.
func (r *backendCallRecorder) installHTTPTrace(ctx context.Context, transportPresent bool) context.Context {
	if r == nil {
		return ctx
	}
	if r.index < 0 || !transportPresent {
		return markSyntheticFailure(ctx, r.syntheticFlag)
	}
	// Each *Start / *Done pair is gated on IsZero so we record the first
	// attempt only. httptrace fires the pair repeatedly on multi-A DNS,
	// happy-eyeballs IPv4/IPv6 races, and TLS retries; without the gate
	// the *Done callbacks overwrite their *Start counterparts and the
	// derived phase deltas span the entire retry chain (seconds) rather
	// than the actual first-attempt time.
	ct := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			r.mu.Lock()
			if r.dnsStart.IsZero() {
				r.dnsStart = time.Now()
			}
			r.mu.Unlock()
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			r.mu.Lock()
			if r.dnsDone.IsZero() {
				r.dnsDone = time.Now()
			}
			r.mu.Unlock()
		},
		ConnectStart: func(_, _ string) {
			r.mu.Lock()
			if r.connectStart.IsZero() {
				r.connectStart = time.Now()
			}
			r.mu.Unlock()
		},
		ConnectDone: func(_, _ string, _ error) {
			r.mu.Lock()
			if r.connectDone.IsZero() {
				r.connectDone = time.Now()
			}
			r.mu.Unlock()
		},
		TLSHandshakeStart: func() {
			r.mu.Lock()
			if r.tlsStart.IsZero() {
				r.tlsStart = time.Now()
			}
			r.mu.Unlock()
		},
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			r.mu.Lock()
			if r.tlsDone.IsZero() {
				r.tlsDone = time.Now()
			}
			r.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			r.mu.Lock()
			if r.firstByteAt.IsZero() {
				r.firstByteAt = time.Now()
			}
			r.mu.Unlock()
		},
	}
	return httptrace.WithClientTrace(markSyntheticFailure(ctx, r.syntheticFlag), ct)
}

// completeBackendCall is called once the handler returns (or once a Wait /
// Poll / Select hostcall observes a pending response). status is the
// observed HTTP status; cancelled is true when the parent context was
// cancelled during the call; err is non-nil for network errors. When the
// trace has been finalized the call is a silent no-op.
//
// noLateWrites is checked *inside* the r.mu critical section. Checking
// outside the lock is racy: sweepPendingBackends takes r.mu, sets
// noLateWrites=true, and writes the orphan/incomplete outcome; without
// the inside-lock check, a goroutine that already passed an outside
// Load() would acquire r.mu after sweep released it and overwrite the
// orphan/incomplete outcome with a stale Ok.
func (r *backendCallRecorder) completeBackendCall(status int, cancelled bool, err error) {
	if r == nil || r.index < 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.noLateWrites.Load() {
		return
	}
	if r.call == nil {
		return
	}
	call := r.call
	call.TotalNanos = time.Since(r.startedAt).Nanoseconds()
	if call.TotalNanos < 0 {
		call.TotalNanos = 0
	}
	call.Status = status
	switch {
	case *r.syntheticFlag:
		call.Outcome = BackendOutcomeSyntheticFailure
	case cancelled:
		call.Outcome = BackendOutcomeCancelled
	case err != nil:
		call.Outcome = BackendOutcomeNetworkError
	default:
		call.Outcome = BackendOutcomeOk
	}
	r.fillPhasesLocked(call)
}

// markIncomplete sets Outcome to incomplete when the async-grace window
// expires before the goroutine finalized the call. Phases are snapshotted
// from whatever httptrace observed up to that point.
//
// noLateWrites is flipped *inside* the same r.mu critical section so any
// goroutine still racing to call completeBackendCall blocks on r.mu, then
// sees the flag and bails — sweep's classification wins.
func (r *backendCallRecorder) markIncomplete() {
	if r == nil || r.index < 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.noLateWrites.Store(true)
	if r.call == nil {
		return
	}
	call := r.call
	call.Outcome = BackendOutcomeIncomplete
	call.TotalNanos = time.Since(r.startedAt).Nanoseconds()
	r.fillPhasesLocked(call)
}

// markOrphaned tags the BackendCall as orphaned: the guest issued an async
// send and never polled / waited / selected on it before finalize. Sets
// noLateWrites under r.mu so a racing goroutine cannot overwrite the
// orphan classification (see markIncomplete for the same rationale).
func (r *backendCallRecorder) markOrphaned() {
	if r == nil || r.index < 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.noLateWrites.Store(true)
	if r.call == nil {
		return
	}
	call := r.call
	call.Outcome = BackendOutcomeOrphaned
	if call.TotalNanos == 0 {
		call.TotalNanos = time.Since(r.startedAt).Nanoseconds()
	}
	r.fillPhasesLocked(call)
}

// disableLateWrites locks the recorder against further trace mutations.
// Resource cleanup paths (body close) bypass the flag and stay active.
//
// Takes r.mu so it synchronises with any in-flight completeBackendCall
// that has already acquired the lock. After disableLateWrites returns,
// no completeBackendCall is mid-mutation, and subsequent calls will see
// noLateWrites=true on their in-lock check and bail. This is what makes
// it safe for sweepPendingBackends to publish the trace via
// completeTrace immediately after calling disableLateWrites — the UI
// can iterate BackendCalls without taking the recorder mutex.
func (r *backendCallRecorder) disableLateWrites() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.noLateWrites.Store(true)
	r.mu.Unlock()
}

func (r *backendCallRecorder) fillPhasesLocked(call *BackendCall) {
	if !r.dnsStart.IsZero() && !r.dnsDone.IsZero() {
		n := r.dnsDone.Sub(r.dnsStart).Nanoseconds()
		call.DNSNanos = &n
	}
	if !r.connectStart.IsZero() && !r.connectDone.IsZero() {
		n := r.connectDone.Sub(r.connectStart).Nanoseconds()
		call.ConnectNanos = &n
	}
	if !r.tlsStart.IsZero() && !r.tlsDone.IsZero() {
		n := r.tlsDone.Sub(r.tlsStart).Nanoseconds()
		call.TLSNanos = &n
	}
	if !r.firstByteAt.IsZero() {
		n := r.firstByteAt.Sub(r.startedAt).Nanoseconds()
		call.TTFBNanos = &n
	}
}

// redactURL strips userinfo and the raw query string. The plan reserves
// full URLs for deep mode; in trace mode we keep scheme/host/path so the
// waterfall is still meaningful but credentials and query parameters do
// not leak.
func redactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	var b strings.Builder
	if u.Scheme != "" {
		b.WriteString(u.Scheme)
		b.WriteString("://")
	}
	if u.Host != "" {
		b.WriteString(u.Host)
	}
	if u.Path != "" {
		b.WriteString(u.Path)
	} else if u.Opaque != "" {
		b.WriteString(u.Opaque)
	}
	return b.String()
}

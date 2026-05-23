package fastlike

import (
	"fmt"
	"net/http"
	"time"
)

// beginTrace arms the recorder for r and returns the response writer the
// caller must treat as w for every downstream write. When profiling is
// disabled it returns w unchanged. ServeHTTP shadows its w argument with
// the return value so loop-fail, trap, and resp_send_downstream all flow
// through the wrapper.
func (i *Instance) beginTrace(w http.ResponseWriter, r *http.Request) http.ResponseWriter {
	if i.profile == nil {
		return w
	}
	i.trace = i.profile.store.newRequestTrace(i.profile.moduleID, r)
	if i.profile.deepEnabled {
		i.trace.Deep = newDeepMetrics()
		// Snapshot the downstream request headers as the request
		// arrives. Guests are free to mutate r.Header later via
		// req_header_insert / req_header_append hostcalls; the trace
		// captures the original surface so the operator sees what
		// came in over the wire.
		i.trace.Deep.RequestHeaders = summarizeHeaders(r.Header)
		// Sample wasm heap at request start. The wasm has been
		// instantiated by Instance.setup() which ran before
		// beginTrace, so memory is live.
		i.deepSampleHeap(0)
	}
	tw := newTraceResponseWriter(w)
	i.traceWriter = tw
	return tw
}

// markOutcome stamps a non-normal outcome on the active trace. Subsequent
// calls do not downgrade a more specific outcome back to normal. Safe to
// call when profiling is disabled.
func (i *Instance) markOutcome(o TraceOutcome) {
	if i.trace == nil {
		return
	}
	if i.trace.Outcome == TraceOutcomeNormal {
		i.trace.Outcome = o
	}
}

// noteTrace appends an arbitrary note to the active trace. Used by panic
// recovery in safeWrap* (step 2) and by the async-grace path (step 3).
func (i *Instance) noteTrace(msg string) {
	if i.trace == nil {
		return
	}
	i.trace.Notes = append(i.trace.Notes, msg)
}

func (i *Instance) markHostcallPanic(name string, val any) {
	i.markOutcome(TraceOutcomePanic)
	i.noteTrace(fmt.Sprintf("hostcall %s panic: %v", name, val))
}

func (i *Instance) startHostcallSpan() time.Time {
	if i.trace == nil {
		return time.Time{}
	}
	return time.Now()
}

func (i *Instance) finishHostcallSpan(nameIdx uint16, start time.Time, rc int32, tags [4]int64) {
	if i.trace == nil || start.IsZero() {
		return
	}
	now := time.Now()
	duration := now.Sub(start).Nanoseconds()
	if duration < 0 {
		duration = 0
	}
	t := i.trace
	t.HostcallNanos += duration
	if len(t.Spans) >= cap(t.Spans) {
		t.Dropped++
		return
	}
	span := Span{
		NameIdx:  nameIdx,
		Start:    start.Sub(t.WallStart).Nanoseconds(),
		Duration: duration,
		RC:       rc,
		TagSlots: tags,
	}
	if span.Start < 0 {
		span.Start = 0
	}
	t.Spans = append(t.Spans, span)
	// Boundary sample for the heap curve. Dedup in deepSampleHeap
	// keeps the slab small once memory stabilises, so per-hostcall
	// overhead is one i.memory.Len() call plus one int compare on
	// steady state.
	i.deepSampleHeap(now.Sub(t.WallStart).Nanoseconds())
}

func hostcallTagSlots(a, b, c, d int64) [4]int64 {
	return [4]int64{a, b, c, d}
}

// finalizeTrace stamps end-of-request state into the trace and hands it
// back to the store. Runs once per request via the defer registered in
// ServeHTTP. The defer is registered after i.reset() so LIFO ordering
// puts finalize first, while ds_response, ds_context, and the in-flight
// trace are all still intact.
func (i *Instance) finalizeTrace() {
	if i.trace == nil {
		return
	}
	t := i.trace
	t.WallNanos = time.Since(t.WallStart).Nanoseconds()
	t.GuestActiveNanos = t.WallNanos - t.HostcallNanos
	if t.GuestActiveNanos < 0 {
		t.GuestActiveNanos = 0
	}
	if i.traceWriter != nil {
		t.Status = i.traceWriter.Status()
		t.HeaderFlushNanos = i.traceWriter.HeaderFlushed()
		t.HijackedNanos = i.traceWriter.Hijacked()
	}
	if t.Outcome == TraceOutcomeNormal && i.ds_context != nil && i.ds_context.Err() != nil {
		t.Outcome = TraceOutcomeCtxCanceled
	}
	i.sweepPendingBackends()
	if t.Deep != nil && i.traceWriter != nil {
		// Snapshot the downstream response headers right before the
		// trace is handed to the store. Reading via the wrapper's
		// Header() returns the same map net/http will have written
		// (the wrapper forwards Header() unchanged).
		t.Deep.ResponseHeaders = summarizeHeaders(i.traceWriter.Header())
	}
	// One final heap sample at end-of-request so the curve always has
	// a "final" data point even when memory was stable throughout.
	i.deepSampleHeap(t.WallNanos)
	t.Deep.finalize()
	i.profile.store.completeTrace(t)
	i.trace = nil
	i.traceWriter = nil
}

// sweepPendingBackends finalises every PendingRequest still in flight at
// the end of the request. The classification follows the policy in
// plans/guest-profiling.md "Async backend lifetime":
//
//   - waitObserved == false → guest never looked at it. Mark orphaned
//     immediately, install a body-close hook so the goroutine's eventual
//     response does not leak, and disable late writes to the trace.
//   - waitObserved == true and !IsReady → wait up to ProfileStore.AsyncGrace
//     for the goroutine to finish. If it finishes inside the window the
//     goroutine's completeBackendCall already recorded the outcome; if
//     the window expires, mark the call incomplete and install the hook.
//   - waitObserved == true and IsReady → goroutine already recorded the
//     outcome; nothing to do beyond flipping the no-late-writes flag.
//
// Body cleanup uses the PendingRequest.bodyClosed atomic. The
// convert-to-handle paths in xqd_pending_req_{poll,wait,select} set it
// before creating the response handle, so the hook never races with
// reset()'s response-handle close.
func (i *Instance) sweepPendingBackends() {
	if i.trace == nil || i.pendingRequests == nil {
		return
	}
	grace := time.Duration(0)
	if i.profile != nil && i.profile.store != nil {
		grace = i.profile.store.AsyncGrace()
	}
	for _, pr := range i.pendingRequests.handles {
		if pr == nil || pr.recorder == nil {
			continue
		}
		rec := pr.recorder
		switch {
		case !pr.waitObserved():
			rec.markOrphaned()
			i.noteTrace("backend call orphaned (fire-and-forget)")
		case pr.IsReady():
			// Goroutine already finished; outcome captured by
			// completeBackendCall inside the send goroutine.
		default:
			if grace == 0 {
				rec.markIncomplete()
			} else {
				select {
				case <-pr.done:
					// Completed within the grace window; outcome already recorded.
				case <-time.After(grace):
					rec.markIncomplete()
					i.noteTrace("backend call incomplete (grace expired)")
				}
			}
		}
		rec.disableLateWrites()
		installBodyCloseHook(pr)
	}
}

// installBodyCloseHook arranges for the goroutine's eventual response body
// to be closed exactly once even if no response handle ever owned it.
// Safe to call after Complete has already fired; the hook runs
// synchronously in that case.
func installBodyCloseHook(pr *PendingRequest) {
	if pr == nil {
		return
	}
	pr.setCompleteHook(func(resp *http.Response, _ error) {
		if resp == nil || resp.Body == nil {
			return
		}
		if pr.bodyClosed.CompareAndSwap(false, true) {
			_ = resp.Body.Close()
		}
	})
}

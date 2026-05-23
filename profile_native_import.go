package fastlike

import (
	"errors"
	"io"
)

// NativeSampleEvent is one raw sample as produced by a native profiler
// (perf, samply) before it has been attached to any RequestTrace. The
// absolute UnixNanos timestamp lets the merge step join the sample to a
// trace by time window; the PID lets it filter out samples that
// belonged to other processes sharing the input stream.
//
// This is intentionally a flat struct: parser stage produces it,
// merge stage consumes it, neither needs anything more. Multi-frame
// stacks would extend Function to a []string; for v1 the importer
// keeps only the top wasm frame, which is the field load-bearing for
// "what was the guest doing at this moment".
type NativeSampleEvent struct {
	PID       int
	UnixNanos int64
	Function  string
}

// NativeSampleImporter parses a profiler-tool output stream into
// NativeSampleEvent values. Implementations must return
// ErrNoNativeSamples when parsing succeeded but produced zero samples,
// so callers can branch on "nothing to merge" without conflating it
// with a parse failure. Any other error indicates the stream was
// malformed or unreadable.
type NativeSampleImporter interface {
	Import(io.Reader) ([]NativeSampleEvent, error)
}

// ErrNoNativeSamples is returned by NativeSampleImporter implementations
// when the input parsed cleanly but contained no samples. It is a
// sentinel value comparable with errors.Is.
var ErrNoNativeSamples = errors.New("no native samples in input")

// MergeNativeSamples distributes events across the completed traces in
// store. Each event attaches to at most one trace, chosen by:
//
//   - PID match: events.PID must equal expectedPID. The CLI sets
//     expectedPID to os.Getpid() so samples leaking from other processes
//     sharing the same input file are filtered out.
//   - Module match: when expectedModuleID is non-empty, only traces
//     whose ModuleID equals it receive samples. This protects against
//     samples captured before a hot reload landing on post-reload
//     traces with a different module fingerprint.
//   - Time window: the sample's UnixNanos must fall within
//     [trace.WallStart, trace.WallStart + trace.WallNanos]. A sample
//     before the trace started or after it finalized is dropped.
//
// Drops are silent: a sample that does not match any trace is not an
// error. The function returns the number of samples actually attached
// so the CLI can print a single summary line.
//
// Merge is store-wide; the caller is expected to pass the complete
// output of a profiling run rather than per-trace slices, because the
// profiler does not know which sample belongs to which request.
func MergeNativeSamples(store *ProfileStore, events []NativeSampleEvent, expectedPID int, expectedModuleID string) int {
	if store == nil || len(events) == 0 {
		return 0
	}
	traces := store.Recent(0)
	if len(traces) == 0 {
		return 0
	}

	attached := 0
	for _, ev := range events {
		if expectedPID != 0 && ev.PID != expectedPID {
			continue
		}
		t := findMatchingTrace(traces, ev.UnixNanos, expectedModuleID)
		if t == nil {
			continue
		}
		rel := ev.UnixNanos - t.WallStart.UnixNano()
		if rel < 0 {
			rel = 0
		}
		t.appendNativeSamples(NativeSample{
			RelativeNanos: rel,
			Function:      ev.Function,
		})
		attached++
	}
	return attached
}

// findMatchingTrace returns the first trace in traces whose time window
// contains unixNanos and whose ModuleID matches expectedModuleID when
// the expected id is non-empty. Returns nil when no trace qualifies.
//
// The linear scan is appropriate for the per-Fastlike LRU size (default
// 256). If retention grows, replacing the scan with a sorted index on
// WallStart is straightforward and isolated to this function.
func findMatchingTrace(traces []*RequestTrace, unixNanos int64, expectedModuleID string) *RequestTrace {
	for _, t := range traces {
		if expectedModuleID != "" && t.ModuleID != expectedModuleID {
			continue
		}
		startNanos := t.WallStart.UnixNano()
		endNanos := startNanos + t.WallNanos
		if unixNanos < startNanos || unixNanos > endNanos {
			continue
		}
		return t
	}
	return nil
}

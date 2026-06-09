package profile

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

func newCompletedTrace(t *testing.T, store *ProfileStore, moduleID string, start time.Time, wallNanos int64) *RequestTrace {
	t.Helper()
	r, _ := http.NewRequest("GET", "http://x/", nil)
	tr := store.NewRequestTrace(moduleID, r)
	tr.WallStart = start
	tr.WallNanos = wallNanos
	store.CompleteTrace(tr)
	return tr
}

func TestMergeNativeSamplesSuccessfulJoin(t *testing.T) {
	store := NewProfileStore()
	start := time.Unix(1700000000, 0)
	tr := newCompletedTrace(t, store, "modA", start, 5_000_000)

	events := []NativeSampleEvent{
		{PID: 4242, UnixNanos: start.Add(1 * time.Millisecond).UnixNano(), Function: "guest_main"},
		{PID: 4242, UnixNanos: start.Add(3 * time.Millisecond).UnixNano(), Function: "alloc"},
	}
	got := MergeNativeSamples(store, events, 4242, "modA")
	if got != 2 {
		t.Errorf("attached: got %d, want 2", got)
	}
	if len(tr.NativeSamples) != 2 {
		t.Fatalf("trace samples: got %d, want 2", len(tr.NativeSamples))
	}
	if tr.NativeSamples[0].RelativeNanos != int64(time.Millisecond) {
		t.Errorf("first sample relative: %d", tr.NativeSamples[0].RelativeNanos)
	}
	if tr.NativeSamples[1].Function != "alloc" {
		t.Errorf("second sample function: %q", tr.NativeSamples[1].Function)
	}
}

func TestMergeNativeSamplesPIDMismatchDrops(t *testing.T) {
	store := NewProfileStore()
	start := time.Unix(1700000000, 0)
	tr := newCompletedTrace(t, store, "modA", start, 5_000_000)

	events := []NativeSampleEvent{
		{PID: 99999, UnixNanos: start.Add(1 * time.Millisecond).UnixNano(), Function: "stranger"},
	}
	got := MergeNativeSamples(store, events, 4242, "modA")
	if got != 0 {
		t.Errorf("PID mismatch should attach 0, got %d", got)
	}
	if len(tr.NativeSamples) != 0 {
		t.Errorf("trace should have no samples after PID mismatch, has %d", len(tr.NativeSamples))
	}
}

func TestMergeNativeSamplesOutsideTimeWindowDrops(t *testing.T) {
	store := NewProfileStore()
	start := time.Unix(1700000000, 0)
	tr := newCompletedTrace(t, store, "modA", start, 5_000_000) // 5ms window

	events := []NativeSampleEvent{
		{PID: 4242, UnixNanos: start.Add(-1 * time.Millisecond).UnixNano(), Function: "before"},
		{PID: 4242, UnixNanos: start.Add(6 * time.Millisecond).UnixNano(), Function: "after"},
	}
	got := MergeNativeSamples(store, events, 4242, "modA")
	if got != 0 {
		t.Errorf("out-of-window samples should attach 0, got %d", got)
	}
	if len(tr.NativeSamples) != 0 {
		t.Errorf("trace should have no samples, has %d", len(tr.NativeSamples))
	}
}

func TestMergeNativeSamplesModuleMismatchDrops(t *testing.T) {
	store := NewProfileStore()
	start := time.Unix(1700000000, 0)
	tr := newCompletedTrace(t, store, "modA", start, 5_000_000)

	events := []NativeSampleEvent{
		{PID: 4242, UnixNanos: start.Add(1 * time.Millisecond).UnixNano(), Function: "guest_main"},
	}
	got := MergeNativeSamples(store, events, 4242, "modB")
	if got != 0 {
		t.Errorf("module mismatch should attach 0, got %d", got)
	}
	if len(tr.NativeSamples) != 0 {
		t.Errorf("trace should have no samples on module mismatch")
	}
}

func TestMergeNativeSamplesUnknownModuleAttachesToAny(t *testing.T) {
	// When expectedModuleID is empty, the merge does not gate on module.
	// Caller chooses this when the operator didn't pass a module fingerprint.
	store := NewProfileStore()
	start := time.Unix(1700000000, 0)
	tr := newCompletedTrace(t, store, "modA", start, 5_000_000)

	events := []NativeSampleEvent{
		{PID: 4242, UnixNanos: start.Add(2 * time.Millisecond).UnixNano(), Function: "guest_main"},
	}
	got := MergeNativeSamples(store, events, 4242, "")
	if got != 1 {
		t.Errorf("empty moduleID should ignore module gate; got %d, want 1", got)
	}
	if len(tr.NativeSamples) != 1 {
		t.Errorf("trace should have 1 sample, has %d", len(tr.NativeSamples))
	}
}

func TestMergeNativeSamplesPIDZeroIgnoresPIDGate(t *testing.T) {
	store := NewProfileStore()
	start := time.Unix(1700000000, 0)
	newCompletedTrace(t, store, "modA", start, 5_000_000)

	events := []NativeSampleEvent{
		{PID: 12345, UnixNanos: start.Add(1 * time.Millisecond).UnixNano(), Function: "any"},
	}
	// expectedPID = 0 → no PID filtering (useful for fixtures / tests).
	got := MergeNativeSamples(store, events, 0, "modA")
	if got != 1 {
		t.Errorf("PID=0 should disable PID gate; got %d, want 1", got)
	}
}

func TestMergeNativeSamplesEmptyStoreNoAttach(t *testing.T) {
	store := NewProfileStore()
	events := []NativeSampleEvent{
		{PID: 4242, UnixNanos: 1700000000_000000000, Function: "x"},
	}
	got := MergeNativeSamples(store, events, 4242, "")
	if got != 0 {
		t.Errorf("empty store: got %d, want 0", got)
	}
}

func TestMergeNativeSamplesNilStoreNoOp(t *testing.T) {
	events := []NativeSampleEvent{{PID: 1, UnixNanos: 1, Function: "x"}}
	if got := MergeNativeSamples(nil, events, 0, ""); got != 0 {
		t.Errorf("nil store should be no-op, got %d", got)
	}
}

func TestMergeNativeSamplesRaceFreeWithEncoders(t *testing.T) {
	// Spawn a merge in one goroutine and concurrent encoder calls in
	// another against the same retained trace. Without the per-trace
	// RWMutex this would trip -race; with the snapshot the readers see
	// a stable slice.
	store := NewProfileStore()
	start := time.Unix(1700000000, 0)
	tr := newCompletedTrace(t, store, "modA", start, 100_000_000)

	var events []NativeSampleEvent
	for k := 0; k < 200; k++ {
		events = append(events, NativeSampleEvent{
			PID:       4242,
			UnixNanos: start.Add(time.Duration(k) * time.Microsecond).UnixNano(),
			Function:  "guest_main",
		})
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		MergeNativeSamples(store, events, 4242, "modA")
	}()
	go func() {
		defer wg.Done()
		for k := 0; k < 200; k++ {
			_, _ = tr.MarshalJSON()
			_, _ = EncodeChromeTrace(tr)
		}
	}()
	wg.Wait()

	if got := len(tr.NativeSamples); got != len(events) {
		t.Errorf("attached count: got %d, want %d", got, len(events))
	}
}

func TestMergeNativeSamplesDistributesAcrossTraces(t *testing.T) {
	// Two traces back-to-back; samples falling in each window attach to
	// the right one. Exercises the linear scan in findMatchingTrace.
	store := NewProfileStore()
	start1 := time.Unix(1700000000, 0)
	tr1 := newCompletedTrace(t, store, "modA", start1, 5_000_000)
	start2 := start1.Add(10 * time.Millisecond)
	tr2 := newCompletedTrace(t, store, "modA", start2, 5_000_000)

	events := []NativeSampleEvent{
		{PID: 4242, UnixNanos: start1.Add(1 * time.Millisecond).UnixNano(), Function: "tr1_sample"},
		{PID: 4242, UnixNanos: start2.Add(2 * time.Millisecond).UnixNano(), Function: "tr2_sample"},
		{PID: 4242, UnixNanos: start1.Add(7 * time.Millisecond).UnixNano(), Function: "gap_dropped"},
	}
	got := MergeNativeSamples(store, events, 4242, "modA")
	if got != 2 {
		t.Errorf("attached: got %d, want 2 (one in gap)", got)
	}
	if len(tr1.NativeSamples) != 1 || tr1.NativeSamples[0].Function != "tr1_sample" {
		t.Errorf("tr1 samples: %+v", tr1.NativeSamples)
	}
	if len(tr2.NativeSamples) != 1 || tr2.NativeSamples[0].Function != "tr2_sample" {
		t.Errorf("tr2 samples: %+v", tr2.NativeSamples)
	}
}

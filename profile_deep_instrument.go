package fastlike

// These helpers are the deep-mode instrumentation seam. Every
// xqd_* function that wants to feed deep metrics calls one of them.
// They short-circuit to a no-op when trace.Deep is nil (mode is not
// ProfileModeDeep, profiling is off, or the trace was never armed),
// so adding instrumentation to a hostcall is one call site with no
// further gating logic in the caller.
//
// The helpers intentionally accept the rawest possible inputs (int
// counts, raw store names) so the privacy boundary stays here: a
// site that does not call a helper cannot leak data, and a site that
// does call a helper can only pass through what the helper exposes.

// deepBumpBodyRead adds n to BodyReadBytes when deep is active.
func (i *Instance) deepBumpBodyRead(n int64) {
	if i.trace == nil || i.trace.Deep == nil || n <= 0 {
		return
	}
	i.trace.Deep.BodyReadBytes += n
}

// deepBumpBodyWrite adds n to BodyWriteBytes when deep is active.
func (i *Instance) deepBumpBodyWrite(n int64) {
	if i.trace == nil || i.trace.Deep == nil || n <= 0 {
		return
	}
	i.trace.Deep.BodyWriteBytes += n
}

// deepBumpCacheLookup increments the lookup counter. Hit/miss/stale
// classification is deferred to a follow-up that walks the lookup
// result; for v1 we only assert "a lookup happened".
func (i *Instance) deepBumpCacheLookup() {
	if i.trace == nil || i.trace.Deep == nil {
		return
	}
	i.trace.Deep.CacheLookups++
}

// deepBumpCacheInsert increments the insert counter.
func (i *Instance) deepBumpCacheInsert() {
	if i.trace == nil || i.trace.Deep == nil {
		return
	}
	i.trace.Deep.CacheInserts++
}

// deepBumpCacheOutcome inspects the CacheState that a lookup returned
// and increments one of CacheHits / CacheMisses / CacheStale. The
// classification mirrors what fastlike's cache reports: Found+Usable
// without Stale is a hit, Found+Stale is a stale, otherwise a miss.
// No cache keys, surrogate keys, or response bodies reach this
// helper — outcome flags only.
func (i *Instance) deepBumpCacheOutcome(state CacheState) {
	if i.trace == nil || i.trace.Deep == nil {
		return
	}
	switch {
	case state.Found && state.Stale:
		i.trace.Deep.CacheStale++
	case state.Found && state.Usable:
		i.trace.Deep.CacheHits++
	default:
		i.trace.Deep.CacheMisses++
	}
}

// deepBumpStore records one access against the named store. kind is
// the store family ("kv" / "config" / "secret" / "dictionary"); name
// is the operator-supplied identifier. No key material reaches this
// helper — the call sites pass only the store name.
func (i *Instance) deepBumpStore(kind, name string) {
	if i.trace == nil || i.trace.Deep == nil {
		return
	}
	i.trace.Deep.bumpStoreAccess(kind, name)
}

// deepSampleHeap records one wasm linear memory observation against
// the active trace. Dedup against the last recorded sample's
// MemoryBytes is the load-bearing performance hack: hostcall
// boundaries fire many times per request, but wasm memory only grows
// monotonically and usually stabilises after a few initial pages.
// After dedup, a steady-state request adds zero samples per
// post-hostcall call.
//
// When the sample slab is full, HeapSamplesDropped increments and
// the new sample is dropped silently — the operator sees the drop
// counter and knows their guest is doing something unusual.
//
// nowOffsetNanos is the time the caller wants stamped on the sample,
// usually `time.Since(i.trace.WallStart).Nanoseconds()`. Computing
// it outside the helper keeps this function inlinable.
func (i *Instance) deepSampleHeap(nowOffsetNanos int64) {
	if i.trace == nil || i.trace.Deep == nil {
		return
	}
	if i.memory == nil {
		return
	}
	bytes := int64(i.memory.Len())
	d := i.trace.Deep
	// First sample always lands; subsequent samples only land when
	// the value changed.
	if len(d.HeapSamples) > 0 && bytes == d.lastHeapBytes {
		return
	}
	if len(d.HeapSamples) >= defaultHeapSampleCap {
		d.HeapSamplesDropped++
		return
	}
	d.HeapSamples = append(d.HeapSamples, HeapSample{
		RelativeNanos: nowOffsetNanos,
		MemoryBytes:   bytes,
	})
	d.lastHeapBytes = bytes
}

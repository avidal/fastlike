package fastlike

import "sort"

// DeepMetrics carries fields populated only when the Fastlike was built
// with ProfileModeDeep. The pointer is nil on every other mode; encoders
// and the UI must branch on the pointer before reading any of the
// fields below. Adding a deep-only counter is two changes: a field
// here, and one bump call inside an `if i.trace != nil &&
// i.trace.Deep != nil` block at the relevant xqd_* site.
//
// Privacy contract: NOTHING in this struct may include key material,
// header values, body bytes, or secret values. Names and counts only.
// The plan's Privacy section is the source of truth and the
// profile_deep_privacy_test.go fixture pins the contract.
type DeepMetrics struct {
	BodyReadBytes  int64
	BodyWriteBytes int64

	CacheLookups int
	CacheInserts int
	CacheHits    int
	CacheMisses  int
	CacheStale   int

	// RequestHeaders / ResponseHeaders are populated once per request
	// by beginTrace / finalizeTrace: a snapshot of the downstream
	// request headers at the moment fastlike accepted the request, and
	// a snapshot of the downstream response headers at the moment the
	// guest's final response has flushed. Header names appear
	// canonical-cased; names on the redact list (Cookie, Set-Cookie,
	// Authorization, etc.) appear as "<redacted>" with their byte
	// counts still aggregated. No header values ever reach this slice.
	RequestHeaders  []HeaderSummary
	ResponseHeaders []HeaderSummary

	// storeAccess is the live accumulation map, keyed by (kind, name).
	// finalizeTrace flattens it into StoreAccess so the JSON has a
	// stable order across runs and stays the public surface.
	storeAccess map[storeAccessKey]int

	// StoreAccess is the sorted list rebuilt at finalize. Sort order:
	// Kind ascending, then Name ascending. Empty slice when no access
	// happened — the encoder still omits the field via omitempty.
	StoreAccess []StoreAccess

	// HeapSamples is the wasm linear memory size curve over the
	// request, captured at request start, finalize, and after each
	// hostcall boundary. Consecutive samples whose MemoryBytes match
	// the previous entry are dropped at recording time (wasm memory
	// grows monotonically, so once it stabilises further samples add
	// no information).
	//
	// IMPORTANT: this is wasm guest linear memory bytes, NOT the Go
	// host's runtime heap. A reader looking at the curve to debug a
	// guest-side leak should treat each sample as the size of the
	// `memory` export at that moment.
	HeapSamples        []HeapSample
	HeapSamplesDropped int

	// lastHeapBytes is the dedup anchor: a new sample is only
	// appended when MemoryBytes differs from this value.
	lastHeapBytes int64
}

// HeapSample is one wasm linear memory observation. Both fields are
// measured against absolute baselines: RelativeNanos from the parent
// trace's WallStart, MemoryBytes from wasm linear memory size at the
// observation point.
type HeapSample struct {
	RelativeNanos int64
	MemoryBytes   int64
}

// defaultHeapSampleCap is the per-request ceiling on retained heap
// samples. Wasm memory grows in 64KB pages, so 1024 *distinct* samples
// implies ~64MB of growth — well outside what a normal request should
// produce. The cap is a safety valve against pathological guests, not
// a tunable.
const defaultHeapSampleCap = 1024

// HeapAggregates summarises a HeapSamples slice into three scalar
// values: Min and Max wasm memory size observed during the request,
// and the Final value (the last recorded sample). Returns the zero
// struct when samples is empty so encoders can branch on len.
type HeapAggregates struct {
	Min   int64
	Max   int64
	Final int64
}

// HeapAggregatesOf computes min / max / final across the given heap
// sample slice. Encoders that surface only aggregates (Chrome,
// Firefox) call this; the native JSON keeps the full slice.
func HeapAggregatesOf(samples []HeapSample) HeapAggregates {
	if len(samples) == 0 {
		return HeapAggregates{}
	}
	out := HeapAggregates{
		Min:   samples[0].MemoryBytes,
		Max:   samples[0].MemoryBytes,
		Final: samples[len(samples)-1].MemoryBytes,
	}
	for _, s := range samples[1:] {
		if s.MemoryBytes < out.Min {
			out.Min = s.MemoryBytes
		}
		if s.MemoryBytes > out.Max {
			out.Max = s.MemoryBytes
		}
	}
	return out
}

// StoreAccess is one row of the deep-mode store access counter. Kind is
// "kv" / "config" / "secret" / "dictionary"; Name is the operator-
// supplied store identifier (already configuration the user wrote down
// and visible in fastlike's logs); Count is the number of accesses
// during this request.
type StoreAccess struct {
	Kind  string
	Name  string
	Count int
}

type storeAccessKey struct {
	Kind string
	Name string
}

// newDeepMetrics allocates the per-trace deep accumulator. Caller is
// responsible for placing the result on RequestTrace.Deep only when
// the configured mode is ProfileModeDeep.
func newDeepMetrics() *DeepMetrics {
	return &DeepMetrics{
		storeAccess: make(map[storeAccessKey]int),
	}
}

// bumpStoreAccess records one access to the named store of the given
// kind. Safe to call on a nil receiver; the no-op path means
// instrumentation sites can omit the nil check and the compiler
// inlines it away on hot paths when the receiver is statically known.
func (d *DeepMetrics) bumpStoreAccess(kind, name string) {
	if d == nil {
		return
	}
	if d.storeAccess == nil {
		d.storeAccess = make(map[storeAccessKey]int)
	}
	d.storeAccess[storeAccessKey{Kind: kind, Name: name}]++
}

// finalize flattens the accumulator map into StoreAccess. Called once
// per trace from Instance.finalizeTrace before completeTrace hands the
// trace to the store. After finalize the storeAccess map is cleared so
// the trace does not retain it.
func (d *DeepMetrics) finalize() {
	if d == nil || len(d.storeAccess) == 0 {
		return
	}
	out := make([]StoreAccess, 0, len(d.storeAccess))
	for k, count := range d.storeAccess {
		out = append(out, StoreAccess{Kind: k.Kind, Name: k.Name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	d.StoreAccess = out
	d.storeAccess = nil
}

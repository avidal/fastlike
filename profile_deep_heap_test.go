package fastlike

import (
	"encoding/json"
	"strings"
	"testing"

	"fastlike.dev/profile"
)

// instanceWithHeap returns an Instance whose i.memory is backed by
// ByteMemory of size sz, so heap samples have a deterministic value
// to read.
func instanceWithHeap(t *testing.T, deepEnabled bool, sz int) *Instance {
	t.Helper()
	store := profile.NewProfileStore()
	i := &Instance{
		profile: &profile.Binding{Store: store, ModuleID: "m", DeepEnabled: deepEnabled},
		memory:  &Memory{ByteMemory(make([]byte, sz))},
	}
	i.trace = store.NewRequestTrace("m", mustRequest(t))
	if deepEnabled {
		i.trace.Deep = profile.NewDeepMetrics()
	}
	return i
}

func TestDeepHeapAbsentWhenNotDeep(t *testing.T) {
	i := instanceWithHeap(t, false, 64*1024)
	i.deepSampleHeap(0)
	if i.trace.Deep != nil {
		t.Errorf("Deep should remain nil when deepEnabled=false")
	}
}

func TestDeepHeapFirstSampleAlwaysRecords(t *testing.T) {
	i := instanceWithHeap(t, true, 128*1024)
	i.deepSampleHeap(0)
	if got := len(i.trace.Deep.HeapSamples); got != 1 {
		t.Fatalf("first sample: len=%d, want 1", got)
	}
	if got := i.trace.Deep.HeapSamples[0].MemoryBytes; got != 128*1024 {
		t.Errorf("MemoryBytes: %d, want %d", got, 128*1024)
	}
}

func TestDeepHeapDedupCollapsesRepeats(t *testing.T) {
	i := instanceWithHeap(t, true, 64*1024)
	for k := 0; k < 100; k++ {
		i.deepSampleHeap(int64(k))
	}
	if got := len(i.trace.Deep.HeapSamples); got != 1 {
		t.Errorf("dedup failed: got %d samples for stable memory, want 1", got)
	}
}

func TestDeepHeapAppendsOnChange(t *testing.T) {
	i := instanceWithHeap(t, true, 64*1024)
	i.deepSampleHeap(100)
	// Simulate memory growth by replacing the slice.
	i.memory = &Memory{ByteMemory(make([]byte, 128*1024))}
	i.deepSampleHeap(200)
	i.memory = &Memory{ByteMemory(make([]byte, 192*1024))}
	i.deepSampleHeap(300)
	if got := len(i.trace.Deep.HeapSamples); got != 3 {
		t.Fatalf("len: %d, want 3", got)
	}
	bytes := []int64{64 * 1024, 128 * 1024, 192 * 1024}
	for k, want := range bytes {
		if i.trace.Deep.HeapSamples[k].MemoryBytes != want {
			t.Errorf("sample %d: got %d, want %d", k, i.trace.Deep.HeapSamples[k].MemoryBytes, want)
		}
	}
}

func TestDeepHeapCapDropsExcess(t *testing.T) {
	i := instanceWithHeap(t, true, 64*1024)
	// Fill the cap by feeding distinct sizes.
	for k := 0; k < profile.DefaultHeapSampleCap; k++ {
		i.memory = &Memory{ByteMemory(make([]byte, (k+1)*1024))}
		i.deepSampleHeap(int64(k))
	}
	if got := len(i.trace.Deep.HeapSamples); got != profile.DefaultHeapSampleCap {
		t.Fatalf("filled samples: %d, want %d", got, profile.DefaultHeapSampleCap)
	}
	// Overflow.
	for k := 0; k < 10; k++ {
		i.memory = &Memory{ByteMemory(make([]byte, (profile.DefaultHeapSampleCap+k+1)*1024))}
		i.deepSampleHeap(int64(10000 + k))
	}
	if got := len(i.trace.Deep.HeapSamples); got != profile.DefaultHeapSampleCap {
		t.Errorf("samples post-overflow: %d, want %d", got, profile.DefaultHeapSampleCap)
	}
	if got := i.trace.Deep.HeapSamplesDropped; got != 10 {
		t.Errorf("HeapSamplesDropped: %d, want 10", got)
	}
}

func TestHeapAggregatesOfEmpty(t *testing.T) {
	got := profile.HeapAggregatesOf(nil)
	if got != (profile.HeapAggregates{}) {
		t.Errorf("empty samples: %+v, want zero struct", got)
	}
}

func TestHeapAggregatesOfMonotonic(t *testing.T) {
	samples := []profile.HeapSample{
		{RelativeNanos: 0, MemoryBytes: 64 * 1024},
		{RelativeNanos: 100, MemoryBytes: 128 * 1024},
		{RelativeNanos: 200, MemoryBytes: 256 * 1024},
	}
	got := profile.HeapAggregatesOf(samples)
	if got.Min != 64*1024 || got.Max != 256*1024 || got.Final != 256*1024 {
		t.Errorf("aggregates: %+v", got)
	}
}

func TestDeepHeapJSONShape(t *testing.T) {
	i := instanceWithHeap(t, true, 64*1024)
	i.deepSampleHeap(0)
	i.memory = &Memory{ByteMemory(make([]byte, 128*1024))}
	i.deepSampleHeap(500)

	raw, err := i.trace.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Deep struct {
			HeapSamples []struct {
				RelativeNanos int64 `json:"relative_nanos"`
				MemoryBytes   int64 `json:"memory_bytes"`
			} `json:"heap_samples"`
		} `json:"deep"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Deep.HeapSamples) != 2 {
		t.Fatalf("decoded len: %d, want 2", len(decoded.Deep.HeapSamples))
	}
	if decoded.Deep.HeapSamples[0].MemoryBytes != 64*1024 {
		t.Errorf("first sample bytes: %d", decoded.Deep.HeapSamples[0].MemoryBytes)
	}
}

func TestDeepHeapJSONOmittedWhenAbsent(t *testing.T) {
	tr := &profile.RequestTrace{Deep: profile.NewDeepMetrics()}
	raw, err := tr.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "heap_samples") {
		t.Errorf("heap_samples should be omitted when empty; got %s", raw)
	}
}

func TestDeepHeapEncodersAggregatesOnly(t *testing.T) {
	// Confirm: Chrome OtherData and Firefox deep_metrics marker
	// carry min/max/final and sample-count aggregates, but neither
	// surfaces the per-sample curve. Native JSON keeps the curve.
	i := instanceWithHeap(t, true, 64*1024)
	i.deepSampleHeap(0)
	i.memory = &Memory{ByteMemory(make([]byte, 256*1024))}
	i.deepSampleHeap(1000)

	chromeRaw, err := profile.EncodeChromeTrace(i.trace)
	if err != nil {
		t.Fatalf("chrome: %v", err)
	}
	for _, want := range []string{
		"fastlike.deep.wasm_heap_min_bytes",
		"fastlike.deep.wasm_heap_max_bytes",
		"fastlike.deep.wasm_heap_final_bytes",
		"fastlike.deep.wasm_heap_samples",
	} {
		if !strings.Contains(string(chromeRaw), want) {
			t.Errorf("chrome missing %q", want)
		}
	}
	// Per-sample relative_nanos field belongs to the native JSON shape.
	// Chrome should not be carrying per-sample entries.
	if strings.Contains(string(chromeRaw), `"relative_nanos"`) {
		t.Errorf("chrome should not carry per-sample heap entries")
	}

	ffRaw, err := profile.EncodeFirefoxGecko(i.trace)
	if err != nil {
		t.Fatalf("firefox: %v", err)
	}
	for _, want := range []string{
		`"wasm_heap_min_bytes"`,
		`"wasm_heap_max_bytes"`,
		`"wasm_heap_final_bytes"`,
	} {
		if !strings.Contains(string(ffRaw), want) {
			t.Errorf("firefox missing %q", want)
		}
	}
}

func TestDeepHeapEncodersNeverClaimGoHeap(t *testing.T) {
	// Every place we surface heap data must be unambiguous about it
	// being wasm linear memory, not Go runtime heap. The key shape we
	// commit to is "wasm_heap" so a future contributor doesn't
	// accidentally add a "go_heap_bytes" field that misleads.
	i := instanceWithHeap(t, true, 64*1024)
	i.deepSampleHeap(0)

	for label, raw := range map[string][]byte{
		"native":  mustEncodeNative(t, i.trace),
		"chrome":  mustEncodeChrome(t, i.trace),
		"firefox": mustEncodeFirefox(t, i.trace),
	} {
		body := string(raw)
		if strings.Contains(body, "go_heap") || strings.Contains(body, "runtime_heap") {
			t.Errorf("%s output claims Go heap data: %s", label, body)
		}
	}
}

func mustEncodeNative(t *testing.T, tr *profile.RequestTrace) []byte {
	t.Helper()
	raw, err := tr.MarshalJSON()
	if err != nil {
		t.Fatalf("native: %v", err)
	}
	return raw
}

func mustEncodeChrome(t *testing.T, tr *profile.RequestTrace) []byte {
	t.Helper()
	raw, err := profile.EncodeChromeTrace(tr)
	if err != nil {
		t.Fatalf("chrome: %v", err)
	}
	return raw
}

func mustEncodeFirefox(t *testing.T, tr *profile.RequestTrace) []byte {
	t.Helper()
	raw, err := profile.EncodeFirefoxGecko(tr)
	if err != nil {
		t.Fatalf("firefox: %v", err)
	}
	return raw
}

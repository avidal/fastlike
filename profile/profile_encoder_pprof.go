package profile

import (
	"bytes"
	"fmt"

	"github.com/google/pprof/profile"
)

// EncodePprof renders a RequestTrace into the gzip-compressed
// profile.proto format consumable by `go tool pprof` and the
// pprof web UI.
//
// The mapping is intentionally synthetic: pprof is designed for
// statistical CPU profiling, while a RequestTrace is a per-request
// wall-time event log. To bridge the impedance mismatch, fastlike
// emits one Sample per hostcall span and one Sample per backend
// call. Each sample's value is the span's wall-time duration in
// nanoseconds, and each Sample carries a Location naming the hostcall
// or backend. The user reads this as "this is how long was spent in
// each subsystem during this request", which is the load-bearing
// question pprof's flame / cumulative views answer well.
//
// Native samples (when present) become per-sample entries with value
// 1 and a "native sample" sample type, so they appear as counts
// rather than durations — that matches what an external profiler
// actually captured (a sample at a moment in time, not a duration).
//
// The output is the gzip-compressed protobuf wire form pprof expects.
// Golden tests decode the bytes back through profile.Parse before
// comparing structure; the raw wire form is not stable across
// protobuf encoder versions and is the wrong layer to assert on.
//
// Privacy: the encoder only references URLRedacted, hostcall names,
// backend names, and outcomes — all fields the in-memory trace
// already vetted. No header values, body bytes, secrets, or
// userinfo can reach this output.
func EncodePprof(t *RequestTrace) ([]byte, error) {
	if t == nil {
		return nil, fmt.Errorf("encode pprof: nil trace")
	}

	p := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "wall", Unit: "nanoseconds"},
			{Type: "count", Unit: "count"},
		},
		PeriodType:        &profile.ValueType{Type: "wall", Unit: "nanoseconds"},
		Period:            1,
		TimeNanos:         t.WallStart.UnixNano(),
		DurationNanos:     t.WallNanos,
		DefaultSampleType: "wall",
	}

	// Function and location intern tables. pprof requires every Sample
	// to point at one or more Locations, and every Location to point at
	// at least one Function. We allocate one Function per unique label
	// (hostcall name, backend name, "native:<fn>", lifecycle marker
	// name) and one Location wrapping it.
	functions := map[string]*profile.Function{}
	locations := map[string]*profile.Location{}
	nextFuncID := uint64(1)
	nextLocID := uint64(1)

	loc := func(label string) *profile.Location {
		if l, ok := locations[label]; ok {
			return l
		}
		fn, ok := functions[label]
		if !ok {
			fn = &profile.Function{
				ID:         nextFuncID,
				Name:       label,
				SystemName: label,
			}
			nextFuncID++
			p.Function = append(p.Function, fn)
			functions[label] = fn
		}
		l := &profile.Location{
			ID:   nextLocID,
			Line: []profile.Line{{Function: fn}},
		}
		nextLocID++
		p.Location = append(p.Location, l)
		locations[label] = l
		return l
	}

	// Hostcall spans: value = (duration_nanos, 1)
	for _, s := range t.Spans {
		name := "hostcall:" + ResolveHostcallName(s.NameIdx)
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc(name)},
			Value:    []int64{s.Duration, 1},
			Label: map[string][]string{
				"rc":       {fmt.Sprintf("%d", s.RC)},
				"category": {"hostcall"},
			},
		})
	}

	// Backend calls: value = (total_nanos, 1)
	for _, c := range t.BackendCalls {
		name := "backend:" + c.Name
		labels := map[string][]string{
			"method":   {c.Method},
			"outcome":  {c.Outcome.String()},
			"url":      {c.URLRedacted},
			"category": {"backend"},
			"status":   {fmt.Sprintf("%d", c.Status)},
		}
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc(name)},
			Value:    []int64{c.TotalNanos, 1},
			Label:    labels,
		})
	}

	// Native samples: value = (0, 1). The duration column stays zero
	// because a native sample is a moment, not a span; the count
	// column carries the meaningful aggregate.
	for _, s := range t.SnapshotNativeSamples() {
		fn := s.Function
		if fn == "" {
			fn = "(unknown)"
		}
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc("native:" + fn)},
			Value:    []int64{0, 1},
			Label:    map[string][]string{"category": {"native"}},
		})
	}

	// Surface lifecycle markers (outcome + truncation) as zero-value
	// samples on their own labels so a pprof report still reflects
	// "this trace was truncated" / "this trace ended in trap" without
	// the operator needing to read the native JSON.
	p.Sample = append(p.Sample, &profile.Sample{
		Location: []*profile.Location{loc("outcome:" + t.Outcome.String())},
		Value:    []int64{0, 1},
		Label: map[string][]string{
			"category":  {"lifecycle"},
			"req_id":    {fmt.Sprintf("%d", t.ReqID)},
			"module_id": {t.ModuleID},
			"method":    {t.Method},
			"url":       {t.URL},
		},
	})
	if t.Dropped > 0 {
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc("lifecycle:dropped_spans")},
			Value:    []int64{0, int64(t.Dropped)},
			Label:    map[string][]string{"category": {"lifecycle"}},
		})
	}
	if t.DroppedBackendCalls > 0 {
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc("lifecycle:dropped_backend_calls")},
			Value:    []int64{0, int64(t.DroppedBackendCalls)},
			Label:    map[string][]string{"category": {"lifecycle"}},
		})
	}

	// Deep-mode cache outcome counters appear as lifecycle samples
	// keyed by outcome, with the count column carrying the value. A
	// zero outcome is omitted so a pprof report doesn't claim
	// (misleadingly) that the request saw any cache activity it didn't.
	// Non-deep traces leave t.Deep nil so this entire block is skipped.
	if t.Deep != nil {
		for _, c := range []struct {
			name string
			n    int
		}{
			{"cache_hits", t.Deep.CacheHits},
			{"cache_misses", t.Deep.CacheMisses},
			{"cache_stale", t.Deep.CacheStale},
		} {
			if c.n == 0 {
				continue
			}
			p.Sample = append(p.Sample, &profile.Sample{
				Location: []*profile.Location{loc("deep:" + c.name)},
				Value:    []int64{0, int64(c.n)},
				Label:    map[string][]string{"category": {"deep"}},
			})
		}
	}

	var buf bytes.Buffer
	if err := p.Write(&buf); err != nil {
		return nil, fmt.Errorf("encode pprof: %w", err)
	}
	return buf.Bytes(), nil
}

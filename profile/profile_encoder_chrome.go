package profile

import (
	"encoding/json"
	"fmt"
)

// EncodeChromeTrace renders a RequestTrace into the Chrome Tracing
// JSON Object Format described at
// https://docs.google.com/document/d/1CvAClvFfyA5R-PhYUmn5OOQtYMH4h6I0nSsKchNAySU/
// (the "Trace Event Format" doc). The output is loadable directly in
// `about:tracing` and in Perfetto (https://ui.perfetto.dev/).
//
// The mapping is deliberately minimal:
//
//   - One process (pid=1) called "fastlike-request".
//   - Three tids: 1=hostcalls, 2=backends, 3=native.
//   - Each Span → ph="X" complete event on tid=1.
//   - Each BackendCall → ph="X" complete event on tid=2 with phase
//     fields as args; nil pointers are omitted from args.
//   - Each NativeSample → ph="i" instant event on tid=3.
//   - Header-flush timestamp → ph="i" instant on tid=1 named
//     "header_flush" (scope="g" so the marker shows on every track).
//   - Hijack timestamp → ph="i" instant on tid=1 named "hijack".
//   - Dropped + DroppedBackendCalls → ph="i" instants at t=0 named
//     "dropped_spans" / "dropped_backend_calls" so the viewer can't
//     silently render a truncated trace as a complete one.
//   - Trace outcome → ph="i" instant at t=0 named "outcome.<value>".
//
// The encoder is pure: no store or UI dependency, no I/O. The output
// is canonical JSON; goldens compare against the pretty-printed form.
//
// Privacy: URL fields use URLRedacted from the in-memory trace, which
// already strips userinfo and query strings outside deep mode. Hostcall
// tag slots are passed through as opaque integers (their semantics are
// hostcall-specific and documented in trace_schema.md, not unwrapped
// here). No header values, no body bytes, no secrets reach this JSON.
func EncodeChromeTrace(t *RequestTrace) ([]byte, error) {
	if t == nil {
		return nil, fmt.Errorf("encode chrome trace: nil trace")
	}

	nativeSamples := t.SnapshotNativeSamples()
	events := make([]chromeEvent, 0, len(t.Spans)+len(t.BackendCalls)+len(nativeSamples)+5)

	// Thread name metadata first so the viewer labels lanes correctly.
	events = append(
		events,
		chromeMeta(chromeTidHostcalls, "hostcalls"),
		chromeMeta(chromeTidBackends, "backends"),
		chromeMeta(chromeTidNative, "native"),
	)

	// Hostcall spans as complete events.
	for i, s := range t.Spans {
		events = append(events, chromeEvent{
			Name: ResolveHostcallName(s.NameIdx),
			Cat:  "hostcall",
			Ph:   "X",
			Ts:   nanosToMicrosFloat(s.Start),
			Dur:  nanosToMicrosFloat(s.Duration),
			Pid:  chromePid,
			Tid:  chromeTidHostcalls,
			Args: map[string]any{
				"index":     i,
				"rc":        s.RC,
				"tag_slots": s.TagSlots,
			},
		})
	}

	// Backend calls as complete events; nil phase pointers stay out of args.
	for i, c := range t.BackendCalls {
		args := map[string]any{
			"index":   i,
			"name":    c.Name,
			"method":  c.Method,
			"url":     c.URLRedacted,
			"status":  c.Status,
			"outcome": c.Outcome.String(),
		}
		if c.DNSNanos != nil {
			args["dns_ms"] = nanosToMillisFloat(*c.DNSNanos)
		}
		if c.ConnectNanos != nil {
			args["connect_ms"] = nanosToMillisFloat(*c.ConnectNanos)
		}
		if c.TLSNanos != nil {
			args["tls_ms"] = nanosToMillisFloat(*c.TLSNanos)
		}
		if c.TTFBNanos != nil {
			args["ttfb_ms"] = nanosToMillisFloat(*c.TTFBNanos)
		}
		events = append(events, chromeEvent{
			Name: c.Name,
			Cat:  "backend",
			Ph:   "X",
			Ts:   nanosToMicrosFloat(c.Started),
			Dur:  nanosToMicrosFloat(c.TotalNanos),
			Pid:  chromePid,
			Tid:  chromeTidBackends,
			Args: args,
		})
	}

	// Native samples as instant events.
	for i, s := range nativeSamples {
		events = append(events, chromeEvent{
			Name:  s.Function,
			Cat:   "native",
			Ph:    "i",
			Ts:    nanosToMicrosFloat(s.RelativeNanos),
			Pid:   chromePid,
			Tid:   chromeTidNative,
			Scope: "t",
			Args: map[string]any{
				"index": i,
			},
		})
	}

	// Header flush instant.
	if t.HeaderFlushNanos != nil {
		events = append(events, chromeEvent{
			Name:  "header_flush",
			Cat:   "lifecycle",
			Ph:    "i",
			Ts:    nanosToMicrosFloat(*t.HeaderFlushNanos),
			Pid:   chromePid,
			Tid:   chromeTidHostcalls,
			Scope: "g",
		})
	}

	// Hijack instant.
	if t.HijackedNanos != nil {
		events = append(events, chromeEvent{
			Name:  "hijack",
			Cat:   "lifecycle",
			Ph:    "i",
			Ts:    nanosToMicrosFloat(*t.HijackedNanos),
			Pid:   chromePid,
			Tid:   chromeTidHostcalls,
			Scope: "g",
		})
	}

	// Truncation counters as instants at t=0 so they always appear at
	// the start of the trace regardless of the actual sample times.
	if t.Dropped > 0 {
		events = append(events, chromeInstantAtZero("dropped_spans", chromeTidHostcalls, map[string]any{
			"count": t.Dropped,
		}))
	}
	if t.DroppedBackendCalls > 0 {
		events = append(events, chromeInstantAtZero("dropped_backend_calls", chromeTidBackends, map[string]any{
			"count": t.DroppedBackendCalls,
		}))
	}

	// Outcome instant at t=0 so it appears at the very start of the trace.
	events = append(events, chromeInstantAtZero("outcome."+t.Outcome.String(), chromeTidHostcalls, map[string]any{
		"req_id":    t.ReqID,
		"module_id": t.ModuleID,
		"method":    t.Method,
		"url":       t.URL,
		"status":    t.Status,
	}))

	other := map[string]any{
		"fastlike.req_id":     t.ReqID,
		"fastlike.module_id":  t.ModuleID,
		"fastlike.outcome":    t.Outcome.String(),
		"fastlike.wall_nanos": t.WallNanos,
	}
	if t.Deep != nil {
		// Deep counters land in OtherData rather than as instant events
		// so they show up in the Perfetto "Information & Stats" panel
		// without polluting the timeline tracks. Zero values are still
		// emitted so a deep capture distinguishes "0 lookups" from
		// "deep was not enabled" (which leaves these keys absent).
		other["fastlike.deep.body_read_bytes"] = t.Deep.BodyReadBytes
		other["fastlike.deep.body_write_bytes"] = t.Deep.BodyWriteBytes
		other["fastlike.deep.cache_lookups"] = t.Deep.CacheLookups
		other["fastlike.deep.cache_inserts"] = t.Deep.CacheInserts
		other["fastlike.deep.cache_hits"] = t.Deep.CacheHits
		other["fastlike.deep.cache_misses"] = t.Deep.CacheMisses
		other["fastlike.deep.cache_stale"] = t.Deep.CacheStale
		// Aggregate header summaries — names and per-header detail
		// live in the native JSON; encoders carry the counts and byte
		// totals so a Chrome viewer reading a deep capture can see
		// "this request had N request headers totalling M bytes"
		// without having to crack open the canonical JSON.
		reqHCount, reqHBytes := headerAggregateTotals(t.Deep.RequestHeaders)
		respHCount, respHBytes := headerAggregateTotals(t.Deep.ResponseHeaders)
		other["fastlike.deep.request_header_count"] = reqHCount
		other["fastlike.deep.request_header_bytes"] = reqHBytes
		other["fastlike.deep.response_header_count"] = respHCount
		other["fastlike.deep.response_header_bytes"] = respHBytes
		// Heap aggregates: encoders only carry min/max/final to keep
		// the OtherData panel compact. The full per-sample curve
		// stays in the native JSON. Field name is wasm-specific so
		// the consumer doesn't mistake it for Go host heap.
		hagg := HeapAggregatesOf(t.Deep.HeapSamples)
		other["fastlike.deep.wasm_heap_min_bytes"] = hagg.Min
		other["fastlike.deep.wasm_heap_max_bytes"] = hagg.Max
		other["fastlike.deep.wasm_heap_final_bytes"] = hagg.Final
		other["fastlike.deep.wasm_heap_samples"] = len(t.Deep.HeapSamples)
		other["fastlike.deep.wasm_heap_samples_dropped"] = t.Deep.HeapSamplesDropped
	}
	doc := chromeDocument{
		TraceEvents:     events,
		DisplayTimeUnit: "ms",
		OtherData:       other,
	}
	return json.Marshal(doc)
}

const (
	chromePid          = 1
	chromeTidHostcalls = 1
	chromeTidBackends  = 2
	chromeTidNative    = 3
)

// chromeEvent is one entry in the Chrome Tracing traceEvents array. The
// field order matches the spec exactly; extra fields are added through
// Args (a map so we can include or omit per-event keys).
type chromeEvent struct {
	Name  string         `json:"name"`
	Cat   string         `json:"cat,omitempty"`
	Ph    string         `json:"ph"`
	Ts    float64        `json:"ts"`
	Dur   float64        `json:"dur,omitempty"`
	Pid   int            `json:"pid"`
	Tid   int            `json:"tid"`
	Scope string         `json:"s,omitempty"`
	Args  map[string]any `json:"args,omitempty"`
}

// chromeDocument is the top-level Chrome Tracing object.
type chromeDocument struct {
	TraceEvents     []chromeEvent  `json:"traceEvents"`
	DisplayTimeUnit string         `json:"displayTimeUnit,omitempty"`
	OtherData       map[string]any `json:"otherData,omitempty"`
}

func chromeMeta(tid int, name string) chromeEvent {
	return chromeEvent{
		Name: "thread_name",
		Ph:   "M",
		Pid:  chromePid,
		Tid:  tid,
		Args: map[string]any{"name": name},
	}
}

func chromeInstantAtZero(name string, tid int, args map[string]any) chromeEvent {
	return chromeEvent{
		Name:  name,
		Cat:   "lifecycle",
		Ph:    "i",
		Ts:    0,
		Pid:   chromePid,
		Tid:   tid,
		Scope: "g",
		Args:  args,
	}
}

// nanosToMicrosFloat converts nanoseconds to fractional microseconds.
// Chrome Tracing uses microseconds as its base unit; sub-microsecond
// durations should still appear with their fractional value rather than
// rounding to zero.
func nanosToMicrosFloat(nanos int64) float64 {
	return float64(nanos) / 1_000.0
}

// nanosToMillisFloat is used for phase fields rendered in the args map;
// human readers think of DNS/Connect/TLS/TTFB in milliseconds.
func nanosToMillisFloat(nanos int64) float64 {
	return float64(nanos) / 1_000_000.0
}

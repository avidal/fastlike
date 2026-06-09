package profile

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func intPtr(v int64) *int64 { return &v }

func TestRequestTraceJSONFullShape(t *testing.T) {
	connectNanos := int64(2_500_000)
	ttfbNanos := int64(8_000_000)
	hijack := int64(123_456)

	trace := &RequestTrace{
		ReqID:            7,
		ModuleID:         "abc123",
		Method:           "GET",
		URL:              "http://x/path",
		Status:           200,
		WallStart:        time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
		WallNanos:        1_000_000,
		HeaderFlushNanos: intPtr(500_000),
		GuestActiveNanos: 400_000,
		HostcallNanos:    300_000,
		HijackedNanos:    &hijack,
		Spans: []Span{
			{
				NameIdx:  HostcallNameIndex("body_downstream_get"),
				Start:    1000,
				Duration: 2000,
				RC:       0,
				TagSlots: [4]int64{1, 2, 3, 4},
			},
		},
		BackendCalls: []BackendCall{
			{
				PendingID:    1,
				Name:         "api",
				Method:       "POST",
				URLRedacted:  "http://upstream/path",
				Started:      100,
				ConnectNanos: &connectNanos,
				TTFBNanos:    &ttfbNanos,
				TotalNanos:   9_000_000,
				Status:       200,
				Outcome:      BackendOutcomeOk,
			},
		},
		Dropped:             3,
		DroppedBackendCalls: 2,
		Outcome:             TraceOutcomeCtxCanceled,
		Notes:               []string{"note one"},
	}

	raw, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got := decoded["req_id"]; got != float64(7) {
		t.Errorf("req_id wrong: %v", got)
	}
	if got := decoded["dropped"]; got != float64(3) {
		t.Errorf("dropped not surfaced: %v", got)
	}
	if got := decoded["dropped_backend_calls"]; got != float64(2) {
		t.Errorf("dropped_backend_calls not surfaced: %v", got)
	}
	if got := decoded["outcome"]; got != "ctx-canceled" {
		t.Errorf("outcome string wrong: %v", got)
	}
	if got := decoded["hijacked_nanos"]; got != float64(123456) {
		t.Errorf("hijacked_nanos missing: %v", got)
	}

	spans := decoded["spans"].([]any)
	span0 := spans[0].(map[string]any)
	if got := span0["name"]; got != "body_downstream_get" {
		t.Errorf("span name not resolved: %v", got)
	}

	calls := decoded["backend_calls"].([]any)
	call0 := calls[0].(map[string]any)
	if got := call0["outcome"]; got != "ok" {
		t.Errorf("backend outcome string wrong: %v", got)
	}
	if got := call0["url_redacted"]; got != "http://upstream/path" {
		t.Errorf("redacted url missing: %v", got)
	}
	if _, present := call0["dns_nanos"]; present {
		t.Errorf("dns_nanos should be omitted when nil")
	}
	if got := call0["connect_nanos"]; got != float64(2_500_000) {
		t.Errorf("connect_nanos value wrong: %v", got)
	}
	if got := call0["ttfb_nanos"]; got != float64(8_000_000) {
		t.Errorf("ttfb_nanos value wrong: %v", got)
	}
	if _, present := call0["tls_nanos"]; present {
		t.Errorf("tls_nanos should be omitted when nil")
	}
}

func TestRequestTraceJSONOutcomeStrings(t *testing.T) {
	cases := map[TraceOutcome]string{
		TraceOutcomeNormal:      "normal",
		TraceOutcomeTrap:        "trap",
		TraceOutcomePanic:       "panic",
		TraceOutcomeLoopFail:    "loop-fail",
		TraceOutcomeCtxCanceled: "ctx-canceled",
	}
	for outcome, want := range cases {
		tr := &RequestTrace{Outcome: outcome}
		raw, err := json.Marshal(tr)
		if err != nil {
			t.Fatalf("marshal %v: %v", outcome, err)
		}
		if !bytes.Contains(raw, []byte(`"outcome":"`+want+`"`)) {
			t.Errorf("outcome=%v missing %q in %s", outcome, want, raw)
		}
	}
}

func TestBackendOutcomeJSONStrings(t *testing.T) {
	cases := map[BackendOutcome]string{
		BackendOutcomeOk:               "ok",
		BackendOutcomeNetworkError:     "network-error",
		BackendOutcomeSyntheticFailure: "synthetic-failure",
		BackendOutcomeCancelled:        "cancelled",
		BackendOutcomeIncomplete:       "incomplete",
		BackendOutcomeOrphaned:         "orphaned",
	}
	for outcome, want := range cases {
		tr := &RequestTrace{
			BackendCalls: []BackendCall{{Outcome: outcome}},
		}
		raw, err := json.Marshal(tr)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !bytes.Contains(raw, []byte(`"outcome":"`+want+`"`)) {
			t.Errorf("backend outcome=%v missing %q in %s", outcome, want, raw)
		}
	}
}

func TestRequestTraceJSONNilTrace(t *testing.T) {
	var tr *RequestTrace
	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal nil: %v", err)
	}
	if string(raw) != "null" {
		t.Errorf("nil trace: got %s, want null", raw)
	}
}

func TestRequestTraceJSONPhasesAllNilWhenAbsent(t *testing.T) {
	tr := &RequestTrace{
		BackendCalls: []BackendCall{{
			Name:    "api",
			Outcome: BackendOutcomeOk,
		}},
	}
	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, key := range []string{"dns_nanos", "connect_nanos", "tls_nanos", "ttfb_nanos"} {
		if strings.Contains(body, `"`+key+`"`) {
			t.Errorf("expected %q to be omitted when nil, got %s", key, body)
		}
	}
}

func TestRequestTraceJSONSpanNameUnknown(t *testing.T) {
	tr := &RequestTrace{
		Spans: []Span{{NameIdx: uint16(len(hostcallNames) + 10_000)}},
	}
	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	span0 := decoded["spans"].([]any)[0].(map[string]any)
	if span0["name"] != "<unknown>" {
		t.Errorf("out-of-range NameIdx should resolve to <unknown>; got %v", span0["name"])
	}
}

func TestRequestTraceJSONNativeSamplesOmittedWhenEmpty(t *testing.T) {
	tr := &RequestTrace{
		Outcome: TraceOutcomeNormal,
	}
	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	if strings.Contains(body, "native_samples") {
		t.Errorf("native_samples should be omitted when empty; got %s", body)
	}
}

func TestRequestTraceJSONNativeSamplesPresentWhenPopulated(t *testing.T) {
	tr := &RequestTrace{
		Outcome: TraceOutcomeNormal,
		NativeSamples: []NativeSample{
			{RelativeNanos: 1_000_000, Function: "guest_main"},
			{RelativeNanos: 2_500_000, Function: "alloc"},
		},
	}
	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	samples, ok := decoded["native_samples"].([]any)
	if !ok {
		t.Fatalf("native_samples missing or wrong type: %T", decoded["native_samples"])
	}
	if len(samples) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(samples))
	}
	first := samples[0].(map[string]any)
	if first["function"] != "guest_main" {
		t.Errorf("function: %v", first["function"])
	}
	if first["relative_nanos"] != float64(1_000_000) {
		t.Errorf("relative_nanos: %v", first["relative_nanos"])
	}
}

func TestRequestTraceJSONDeepOmittedWhenAbsent(t *testing.T) {
	tr := &RequestTrace{}
	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), `"deep"`) {
		t.Errorf("deep key should be omitted when DeepMetrics is nil; got %s", raw)
	}
}

func TestRequestTraceJSONDeepPresentWhenPopulated(t *testing.T) {
	d := NewDeepMetrics()
	d.BodyReadBytes = 1234
	d.BodyWriteBytes = 5678
	d.CacheLookups = 3
	d.CacheInserts = 1
	d.CacheHits = 2
	d.CacheMisses = 1
	d.CacheStale = 0
	d.BumpStoreAccess("kv", "users")
	d.BumpStoreAccess("kv", "users")
	d.BumpStoreAccess("config", "flags")
	d.Finalize()

	tr := &RequestTrace{Deep: d}
	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	deep, ok := decoded["deep"].(map[string]any)
	if !ok {
		t.Fatalf("deep missing or wrong type: %T", decoded["deep"])
	}
	if deep["body_read_bytes"] != float64(1234) {
		t.Errorf("body_read_bytes: %v", deep["body_read_bytes"])
	}
	if deep["cache_lookups"] != float64(3) {
		t.Errorf("cache_lookups: %v", deep["cache_lookups"])
	}
	if deep["cache_hits"] != float64(2) {
		t.Errorf("cache_hits: %v", deep["cache_hits"])
	}
	if deep["cache_misses"] != float64(1) {
		t.Errorf("cache_misses: %v", deep["cache_misses"])
	}
	if deep["cache_stale"] != float64(0) {
		t.Errorf("cache_stale: %v", deep["cache_stale"])
	}
	access, ok := deep["store_access"].([]any)
	if !ok || len(access) != 2 {
		t.Fatalf("store_access shape: %T %v", deep["store_access"], deep["store_access"])
	}
	// Sort order: config < kv (alphabetic on Kind).
	first := access[0].(map[string]any)
	if first["kind"] != "config" || first["name"] != "flags" || first["count"] != float64(1) {
		t.Errorf("first row wrong: %+v", first)
	}
	second := access[1].(map[string]any)
	if second["kind"] != "kv" || second["name"] != "users" || second["count"] != float64(2) {
		t.Errorf("second row wrong: %+v", second)
	}
}

func TestResolveHostcallNameSentinel(t *testing.T) {
	if got := ResolveHostcallName(0); got != "<unknown>" {
		t.Errorf("index 0: got %q, want <unknown>", got)
	}
	if got := ResolveHostcallName(uint16(len(hostcallNames) - 1)); got == "<unknown>" {
		t.Errorf("last valid index returned sentinel")
	}
	if got := ResolveHostcallName(uint16(len(hostcallNames) + 5)); got != "<unknown>" {
		t.Errorf("out-of-range: got %q, want <unknown>", got)
	}
}

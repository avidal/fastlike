package fastlike

import (
	"testing"
	"time"
)

// fixtureWallStart is the deterministic anchor every fixture uses so
// goldens stay stable across runs and machines.
var fixtureWallStart = time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)

// fixtureNormalTrace returns a representative successful trace with
// hostcall spans, one backend call with phase data, no native samples,
// no drops, no notes.
func fixtureNormalTrace(t *testing.T) *RequestTrace {
	t.Helper()
	headerFlush := int64(800_000)
	connect := int64(1_500_000)
	ttfb := int64(2_500_000)
	return &RequestTrace{
		ReqID:            7,
		ModuleID:         "abc123def456",
		Method:           "GET",
		URL:              "http://example.com/proxy",
		Status:           200,
		WallStart:        fixtureWallStart,
		WallNanos:        5_000_000,
		HeaderFlushNanos: &headerFlush,
		GuestActiveNanos: 1_500_000,
		HostcallNanos:    1_800_000,
		Spans: []Span{
			{NameIdx: hostcallNameIndex("body_downstream_get"), Start: 100_000, Duration: 200_000, RC: 0, TagSlots: [4]int64{1, 0, 0, 0}},
			{NameIdx: hostcallNameIndex("send"), Start: 1_000_000, Duration: 1_600_000, RC: 0, TagSlots: [4]int64{1, 1, 0, 0}},
		},
		BackendCalls: []BackendCall{
			{
				PendingID:    0,
				Name:         "api",
				Method:       "GET",
				URLRedacted:  "http://upstream/v1/things",
				Started:      1_050_000,
				ConnectNanos: &connect,
				TTFBNanos:    &ttfb,
				TotalNanos:   1_500_000,
				Status:       200,
				Outcome:      BackendOutcomeOk,
			},
		},
		Outcome: TraceOutcomeNormal,
	}
}

// fixtureDroppedCountersTrace covers the truncation accounting path:
// the slab capped at fewer spans than the guest produced, and the
// per-request backend cap was hit too. Encoders must surface both
// counters or the trace silently misrepresents itself.
func fixtureDroppedCountersTrace(t *testing.T) *RequestTrace {
	t.Helper()
	tr := fixtureNormalTrace(t)
	tr.ReqID = 8
	tr.Dropped = 12
	tr.DroppedBackendCalls = 3
	return tr
}

// fixtureNilPhasesTrace exercises the "backend handler has no transport
// fastlike could observe" case — TotalNanos is populated but the four
// phase pointers stay nil. The encoders must omit / null those fields
// without crashing.
func fixtureNilPhasesTrace(t *testing.T) *RequestTrace {
	t.Helper()
	return &RequestTrace{
		ReqID:     9,
		ModuleID:  "abc123def456",
		Method:    "POST",
		URL:       "http://example.com/api",
		Status:    200,
		WallStart: fixtureWallStart,
		WallNanos: 2_000_000,
		Spans: []Span{
			{NameIdx: hostcallNameIndex("send"), Start: 100_000, Duration: 1_800_000, RC: 0},
		},
		BackendCalls: []BackendCall{
			{
				PendingID:   0,
				Name:        "mock",
				Method:      "POST",
				URLRedacted: "http://mock/api",
				Started:     100_000,
				TotalNanos:  1_800_000,
				Status:      200,
				Outcome:     BackendOutcomeOk,
			},
		},
		Outcome: TraceOutcomeNormal,
	}
}

// fixtureNativeSamplesTrace carries three native samples joined into
// the trace, exercising the encoder paths that walk NativeSamples.
func fixtureNativeSamplesTrace(t *testing.T) *RequestTrace {
	t.Helper()
	tr := fixtureNormalTrace(t)
	tr.ReqID = 10
	tr.NativeSamples = []NativeSample{
		{RelativeNanos: 500_000, Function: "guest_main"},
		{RelativeNanos: 1_500_000, Function: "guest_handle_request"},
		{RelativeNanos: 3_200_000, Function: "alloc"},
	}
	return tr
}

// fixturePanicTrace covers the panic outcome path with a Notes entry.
func fixturePanicTrace(t *testing.T) *RequestTrace {
	t.Helper()
	tr := fixtureNormalTrace(t)
	tr.ReqID = 11
	tr.Status = 500
	tr.Outcome = TraceOutcomePanic
	tr.Notes = []string{"hostcall send panic: nil pointer dereference"}
	return tr
}

// fixtureCtxCanceledTrace covers ctx-canceled, with a partial backend
// call that finalized via the cancellation path.
func fixtureCtxCanceledTrace(t *testing.T) *RequestTrace {
	t.Helper()
	tr := fixtureNormalTrace(t)
	tr.ReqID = 12
	tr.Status = 500
	tr.Outcome = TraceOutcomeCtxCanceled
	tr.BackendCalls[0].Outcome = BackendOutcomeCancelled
	tr.BackendCalls[0].Status = 0
	return tr
}

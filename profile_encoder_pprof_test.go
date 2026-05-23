package fastlike

import (
	"bytes"
	"encoding/json"
	"sort"
	"testing"

	"github.com/google/pprof/profile"
)

func TestEncodePprofNil(t *testing.T) {
	if _, err := EncodePprof(nil); err == nil {
		t.Fatal("expected error for nil trace")
	}
}

// pprofSummary distills a parsed profile.Profile into a stable JSON
// shape so goldens can compare structure rather than the gzipped
// protobuf wire form (which is not byte-stable across encoder
// versions). The summary captures the dimensions that matter:
// sample types, total samples, per-sample value + ordered label
// pairs + location names. That's enough to catch any meaningful
// schema regression without depending on protobuf field ordering.
type pprofSummary struct {
	SampleTypes []string         `json:"sample_types"`
	Samples     []pprofSampleRow `json:"samples"`
}

type pprofSampleRow struct {
	Functions []string   `json:"functions"`
	Values    []int64    `json:"values"`
	Labels    [][]string `json:"labels"`
}

func summarizePprof(t *testing.T, raw []byte) []byte {
	t.Helper()
	p, err := profile.Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("pprof parse: %v", err)
	}
	out := pprofSummary{}
	for _, st := range p.SampleType {
		out.SampleTypes = append(out.SampleTypes, st.Type+"/"+st.Unit)
	}
	for _, s := range p.Sample {
		row := pprofSampleRow{Values: s.Value}
		for _, loc := range s.Location {
			for _, line := range loc.Line {
				if line.Function != nil {
					row.Functions = append(row.Functions, line.Function.Name)
				}
			}
		}
		// Labels: sort by key for stability, and sort values per key
		// for the same reason. The pprof package decodes labels as a
		// map so order is not guaranteed across runs even though our
		// encoder writes them deterministically.
		keys := make([]string, 0, len(s.Label))
		for k := range s.Label {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			vals := append([]string(nil), s.Label[k]...)
			sort.Strings(vals)
			pair := append([]string{k}, vals...)
			row.Labels = append(row.Labels, pair)
		}
		out.Samples = append(out.Samples, row)
	}
	// Sort samples themselves for stable ordering. The encoder appends
	// in a deterministic order but the comparison is more robust if
	// the summary doesn't rely on append order.
	sort.Slice(out.Samples, func(i, j int) bool {
		ai, aj := out.Samples[i], out.Samples[j]
		if len(ai.Functions) != 0 && len(aj.Functions) != 0 && ai.Functions[0] != aj.Functions[0] {
			return ai.Functions[0] < aj.Functions[0]
		}
		// Tie-break on first value column.
		if len(ai.Values) > 0 && len(aj.Values) > 0 && ai.Values[0] != aj.Values[0] {
			return ai.Values[0] < aj.Values[0]
		}
		return false
	})
	marshalled, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("summary marshal: %v", err)
	}
	return marshalled
}

func TestEncodePprofNormal(t *testing.T) {
	raw, err := EncodePprof(fixtureNormalTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "pprof_normal.json", summarizePprof(t, raw))
}

func TestEncodePprofDroppedCounters(t *testing.T) {
	raw, err := EncodePprof(fixtureDroppedCountersTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "pprof_dropped.json", summarizePprof(t, raw))
}

func TestEncodePprofNilPhases(t *testing.T) {
	raw, err := EncodePprof(fixtureNilPhasesTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "pprof_nil_phases.json", summarizePprof(t, raw))
}

func TestEncodePprofNativeSamples(t *testing.T) {
	raw, err := EncodePprof(fixtureNativeSamplesTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "pprof_native_samples.json", summarizePprof(t, raw))
}

func TestEncodePprofPanic(t *testing.T) {
	raw, err := EncodePprof(fixturePanicTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "pprof_panic.json", summarizePprof(t, raw))
}

func TestEncodePprofCtxCanceled(t *testing.T) {
	raw, err := EncodePprof(fixtureCtxCanceledTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertGoldenJSON(t, "pprof_ctx_canceled.json", summarizePprof(t, raw))
}

func TestEncodePprofRoundTripsThroughParser(t *testing.T) {
	// Sanity check: the bytes EncodePprof produces are valid pprof.
	raw, err := EncodePprof(fixtureNormalTrace(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	p, err := profile.Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := p.CheckValid(); err != nil {
		t.Errorf("CheckValid: %v", err)
	}
	if len(p.SampleType) != 2 {
		t.Errorf("sample types: %d, want 2", len(p.SampleType))
	}
}

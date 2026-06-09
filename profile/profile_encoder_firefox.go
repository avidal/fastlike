package profile

import (
	"encoding/json"
	"fmt"
)

// EncodeFirefoxGecko renders a RequestTrace into the Firefox profiler
// "Gecko" profile format. The output is loadable at
// https://profiler.firefox.com/ via "Load a profile from file".
//
// The schema fastlike emits is a v27 Gecko profile with a single
// thread carrying our spans and backend calls as markers and the
// native samples as time samples backed by a tiny frame/func/string
// table. This is a deliberately minimal mapping: Firefox's full
// profile shape supports stacks, categories, marker schemas, JS
// allocations, screenshots, and other dimensions fastlike has no
// signal for. The format we emit covers the timeline view (the only
// thing a Firefox profiler user wants from a wall-time hostcall
// trace) without inventing facts.
//
// Schema reference:
//
//	https://github.com/firefox-devtools/profiler/blob/main/docs-developer/gecko-profile-format.md
//
// Privacy: the encoder only consumes fields that already survived the
// privacy filter (URLRedacted, resolved hostcall names, backend
// names). Header values, body bytes, secrets, and URL query strings
// are physically inaccessible from the in-memory trace, so the format
// cannot leak them.
func EncodeFirefoxGecko(t *RequestTrace) ([]byte, error) {
	if t == nil {
		return nil, fmt.Errorf("encode firefox gecko: nil trace")
	}

	thread := geckoThread{
		Name:           "fastlike-request",
		ProcessName:    "fastlike",
		ProcessType:    "default",
		RegisterTime:   0,
		UnregisterTime: nil,
		PID:            1,
		TID:            1,
		Markers:        newGeckoMarkers(),
		Samples:        newGeckoSamples(),
		FrameTable:     newGeckoFrameTable(),
		StackTable:     newGeckoStackTable(),
		FuncTable:      newGeckoFuncTable(),
		StringTable:    []string{},
	}

	for _, s := range t.Spans {
		thread.Markers.add(
			thread.intern(ResolveHostcallName(s.NameIdx)),
			geckoMarkerPhaseInterval,
			nanosToMillisFloat(s.Start),
			nanosToMillisFloat(s.Start+s.Duration),
			geckoMarkerCategoryHostcall,
			map[string]any{
				"rc":        s.RC,
				"tag_slots": s.TagSlots,
			},
		)
	}

	for _, c := range t.BackendCalls {
		data := map[string]any{
			"backend": c.Name,
			"method":  c.Method,
			"url":     c.URLRedacted,
			"status":  c.Status,
			"outcome": c.Outcome.String(),
		}
		if c.DNSNanos != nil {
			data["dns_ms"] = nanosToMillisFloat(*c.DNSNanos)
		}
		if c.ConnectNanos != nil {
			data["connect_ms"] = nanosToMillisFloat(*c.ConnectNanos)
		}
		if c.TLSNanos != nil {
			data["tls_ms"] = nanosToMillisFloat(*c.TLSNanos)
		}
		if c.TTFBNanos != nil {
			data["ttfb_ms"] = nanosToMillisFloat(*c.TTFBNanos)
		}
		thread.Markers.add(
			thread.intern("backend:"+c.Name),
			geckoMarkerPhaseInterval,
			nanosToMillisFloat(c.Started),
			nanosToMillisFloat(c.Started+c.TotalNanos),
			geckoMarkerCategoryBackend,
			data,
		)
	}

	if t.HeaderFlushNanos != nil {
		ts := nanosToMillisFloat(*t.HeaderFlushNanos)
		thread.Markers.add(
			thread.intern("header_flush"),
			geckoMarkerPhaseInstant,
			ts,
			ts,
			geckoMarkerCategoryLifecycle,
			nil,
		)
	}
	if t.HijackedNanos != nil {
		ts := nanosToMillisFloat(*t.HijackedNanos)
		thread.Markers.add(
			thread.intern("hijack"),
			geckoMarkerPhaseInstant,
			ts,
			ts,
			geckoMarkerCategoryLifecycle,
			nil,
		)
	}
	if t.Dropped > 0 {
		thread.Markers.add(
			thread.intern("dropped_spans"),
			geckoMarkerPhaseInstant,
			0,
			0,
			geckoMarkerCategoryLifecycle,
			map[string]any{"count": t.Dropped},
		)
	}
	if t.DroppedBackendCalls > 0 {
		thread.Markers.add(
			thread.intern("dropped_backend_calls"),
			geckoMarkerPhaseInstant,
			0,
			0,
			geckoMarkerCategoryLifecycle,
			map[string]any{"count": t.DroppedBackendCalls},
		)
	}
	thread.Markers.add(
		thread.intern("outcome."+t.Outcome.String()),
		geckoMarkerPhaseInstant,
		0,
		0,
		geckoMarkerCategoryLifecycle,
		nil,
	)

	// Deep-mode counters appear as a single instant marker at t=0 with
	// the counter values as the marker's data payload. Putting them on
	// one marker keeps the timeline clean (vs N markers cluttering t=0)
	// while still surfacing the values in the marker chart's details
	// pane. Absent entirely when DeepMetrics is nil.
	if t.Deep != nil {
		reqHCount, reqHBytes := headerAggregateTotals(t.Deep.RequestHeaders)
		respHCount, respHBytes := headerAggregateTotals(t.Deep.ResponseHeaders)
		hagg := HeapAggregatesOf(t.Deep.HeapSamples)
		thread.Markers.add(
			thread.intern("deep_metrics"),
			geckoMarkerPhaseInstant,
			0,
			0,
			geckoMarkerCategoryLifecycle,
			map[string]any{
				"body_read_bytes":           t.Deep.BodyReadBytes,
				"body_write_bytes":          t.Deep.BodyWriteBytes,
				"cache_lookups":             t.Deep.CacheLookups,
				"cache_inserts":             t.Deep.CacheInserts,
				"cache_hits":                t.Deep.CacheHits,
				"cache_misses":              t.Deep.CacheMisses,
				"cache_stale":               t.Deep.CacheStale,
				"request_header_count":      reqHCount,
				"request_header_bytes":      reqHBytes,
				"response_header_count":     respHCount,
				"response_header_bytes":     respHBytes,
				"wasm_heap_min_bytes":       hagg.Min,
				"wasm_heap_max_bytes":       hagg.Max,
				"wasm_heap_final_bytes":     hagg.Final,
				"wasm_heap_samples":         len(t.Deep.HeapSamples),
				"wasm_heap_samples_dropped": t.Deep.HeapSamplesDropped,
			},
		)
	}

	for _, s := range t.SnapshotNativeSamples() {
		funcName := s.Function
		if funcName == "" {
			funcName = "(unknown)"
		}
		stack := thread.appendNativeSample(funcName)
		thread.Samples.add(stack, nanosToMillisFloat(s.RelativeNanos))
	}

	profile := geckoProfile{
		Meta: geckoMeta{
			Version:     27,
			Interval:    1,
			StartTime:   0,
			ProcessType: 0,
			Categories: []geckoCategory{
				{Name: "Other", Color: "grey", Subcategories: []string{"Other"}},
				{Name: "Hostcall", Color: "blue", Subcategories: []string{"Hostcall"}},
				{Name: "Backend", Color: "green", Subcategories: []string{"Backend"}},
				{Name: "Lifecycle", Color: "yellow", Subcategories: []string{"Lifecycle"}},
				{Name: "Native", Color: "red", Subcategories: []string{"Native"}},
			},
			Stackwalk:    0,
			Platform:     "fastlike",
			OSName:       "fastlike",
			Product:      "fastlike",
			ABI:          "fastlike",
			Misc:         "fastlike profile",
			AppBuildID:   t.ModuleID,
			PhysicalCPUs: 0,
			LogicalCPUs:  0,
			SampleUnits: geckoSampleUnits{
				Time:           "ms",
				EventDelay:     "ms",
				ThreadCPUDelta: "ns",
			},
			MarkerSchema: []geckoMarkerSchemaEntry{},
		},
		Libs:         []geckoLib{},
		Pages:        []any{},
		Threads:      []geckoThread{thread},
		PausedRanges: []any{},
		ProcessName:  "fastlike",
	}

	return json.Marshal(profile)
}

// gecko* types model a minimal subset of the Firefox profiler v27
// Gecko format. Many fields are required by the spec but irrelevant
// for fastlike (libs, pages, etc.); we emit empty arrays for those so
// the profiler accepts the file without complaining.

type geckoProfile struct {
	Meta         geckoMeta     `json:"meta"`
	Libs         []geckoLib    `json:"libs"`
	Pages        []any         `json:"pages"`
	Threads      []geckoThread `json:"threads"`
	PausedRanges []any         `json:"pausedRanges"`
	ProcessName  string        `json:"processName,omitempty"`
}

type geckoMeta struct {
	Version      int                      `json:"version"`
	Interval     float64                  `json:"interval"`
	StartTime    float64                  `json:"startTime"`
	ProcessType  int                      `json:"processType"`
	Categories   []geckoCategory          `json:"categories"`
	Stackwalk    int                      `json:"stackwalk"`
	Platform     string                   `json:"platform"`
	OSName       string                   `json:"oscpu"`
	Product      string                   `json:"product"`
	ABI          string                   `json:"abi"`
	Misc         string                   `json:"misc"`
	AppBuildID   string                   `json:"appBuildID"`
	PhysicalCPUs int                      `json:"physicalCPUs"`
	LogicalCPUs  int                      `json:"logicalCPUs"`
	SampleUnits  geckoSampleUnits         `json:"sampleUnits"`
	MarkerSchema []geckoMarkerSchemaEntry `json:"markerSchema"`
}

type geckoSampleUnits struct {
	Time           string `json:"time"`
	EventDelay     string `json:"eventDelay"`
	ThreadCPUDelta string `json:"threadCPUDelta"`
}

type geckoCategory struct {
	Name          string   `json:"name"`
	Color         string   `json:"color"`
	Subcategories []string `json:"subcategories"`
}

type geckoMarkerSchemaEntry struct {
	Name string `json:"name"`
}

type geckoLib struct{}

type geckoThread struct {
	Name           string           `json:"name"`
	ProcessName    string           `json:"processName,omitempty"`
	ProcessType    string           `json:"processType,omitempty"`
	RegisterTime   float64          `json:"registerTime"`
	UnregisterTime *float64         `json:"unregisterTime"`
	PID            int              `json:"pid"`
	TID            int              `json:"tid"`
	Markers        *geckoMarkers    `json:"markers"`
	Samples        *geckoSamples    `json:"samples"`
	FrameTable     *geckoFrameTable `json:"frameTable"`
	StackTable     *geckoStackTable `json:"stackTable"`
	FuncTable      *geckoFuncTable  `json:"funcTable"`
	StringTable    []string         `json:"stringTable"`
	stringIndex    map[string]int   `json:"-"`
}

func (th *geckoThread) intern(s string) int {
	if th.stringIndex == nil {
		th.stringIndex = make(map[string]int)
	}
	if idx, ok := th.stringIndex[s]; ok {
		return idx
	}
	idx := len(th.StringTable)
	th.StringTable = append(th.StringTable, s)
	th.stringIndex[s] = idx
	return idx
}

// appendNativeSample makes sure the function and matching one-frame
// stack exist in the per-thread tables, then returns the stack index.
// Subsequent samples that hit the same function reuse the stack.
func (th *geckoThread) appendNativeSample(funcName string) int {
	nameIdx := th.intern(funcName)

	// funcTable: name index → func index.
	for i, fn := range th.FuncTable.Name {
		if fn == nameIdx {
			return th.findOrCreateOneFrameStack(i)
		}
	}
	funcIdx := th.FuncTable.add(nameIdx)
	return th.findOrCreateOneFrameStack(funcIdx)
}

func (th *geckoThread) findOrCreateOneFrameStack(funcIdx int) int {
	// frameTable lookup: same func, no inline depth.
	frameIdx := -1
	for i, f := range th.FrameTable.Func {
		if f == funcIdx && th.FrameTable.InlineDepth[i] == 0 {
			frameIdx = i
			break
		}
	}
	if frameIdx == -1 {
		frameIdx = th.FrameTable.add(funcIdx)
	}
	// stackTable: prefix=nil, frame=frameIdx.
	for i, f := range th.StackTable.Frame {
		if f == frameIdx && th.StackTable.Prefix[i] == -1 {
			return i
		}
	}
	return th.StackTable.add(-1, frameIdx)
}

// Per-table schema mirrors what Firefox profiler expects: a "schema"
// object naming the column positions and a "data" array of row arrays.

type geckoMarkers struct {
	Schema map[string]int `json:"schema"`
	Data   [][]any        `json:"data"`
	Length int            `json:"length"`
}

const (
	geckoMarkerPhaseInstant  = 0
	geckoMarkerPhaseInterval = 1

	geckoMarkerCategoryHostcall  = 1
	geckoMarkerCategoryBackend   = 2
	geckoMarkerCategoryLifecycle = 3
)

func newGeckoMarkers() *geckoMarkers {
	return &geckoMarkers{
		Schema: map[string]int{
			"name":      0,
			"startTime": 1,
			"endTime":   2,
			"phase":     3,
			"category":  4,
			"data":      5,
		},
	}
}

func (m *geckoMarkers) add(nameIdx, phase int, start, end float64, category int, data map[string]any) {
	m.Data = append(m.Data, []any{nameIdx, start, end, phase, category, data})
	m.Length++
}

type geckoSamples struct {
	Schema map[string]int `json:"schema"`
	Data   [][]any        `json:"data"`
	Length int            `json:"length"`
}

func newGeckoSamples() *geckoSamples {
	return &geckoSamples{
		Schema: map[string]int{
			"stack":          0,
			"time":           1,
			"responsiveness": 2,
		},
	}
}

func (s *geckoSamples) add(stack int, time float64) {
	s.Data = append(s.Data, []any{stack, time, 0})
	s.Length++
}

type geckoFrameTable struct {
	Schema      map[string]int `json:"schema"`
	Func        []int          `json:"-"`
	InlineDepth []int          `json:"-"`
	Data        [][]any        `json:"data"`
	Length      int            `json:"length"`
}

func newGeckoFrameTable() *geckoFrameTable {
	return &geckoFrameTable{
		Schema: map[string]int{
			"func":        0,
			"inlineDepth": 1,
		},
	}
}

func (f *geckoFrameTable) add(funcIdx int) int {
	idx := f.Length
	f.Func = append(f.Func, funcIdx)
	f.InlineDepth = append(f.InlineDepth, 0)
	f.Data = append(f.Data, []any{funcIdx, 0})
	f.Length++
	return idx
}

type geckoStackTable struct {
	Schema map[string]int `json:"schema"`
	Prefix []int          `json:"-"`
	Frame  []int          `json:"-"`
	Data   [][]any        `json:"data"`
	Length int            `json:"length"`
}

func newGeckoStackTable() *geckoStackTable {
	return &geckoStackTable{
		Schema: map[string]int{
			"prefix": 0,
			"frame":  1,
		},
	}
}

func (s *geckoStackTable) add(prefix, frame int) int {
	idx := s.Length
	s.Prefix = append(s.Prefix, prefix)
	s.Frame = append(s.Frame, frame)
	var prefixVal any
	if prefix < 0 {
		prefixVal = nil
	} else {
		prefixVal = prefix
	}
	s.Data = append(s.Data, []any{prefixVal, frame})
	s.Length++
	return idx
}

type geckoFuncTable struct {
	Schema map[string]int `json:"schema"`
	Name   []int          `json:"-"`
	Data   [][]any        `json:"data"`
	Length int            `json:"length"`
}

func newGeckoFuncTable() *geckoFuncTable {
	return &geckoFuncTable{
		Schema: map[string]int{
			"name": 0,
		},
	}
}

func (f *geckoFuncTable) add(nameIdx int) int {
	idx := f.Length
	f.Name = append(f.Name, nameIdx)
	f.Data = append(f.Data, []any{nameIdx})
	f.Length++
	return idx
}

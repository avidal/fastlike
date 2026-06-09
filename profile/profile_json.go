package profile

import (
	"encoding/json"
	"time"
)

// BackendOutcome.String returns the lowercase wire form used in the JSON
// encoder and the UI. Defined here so the JSON file owns the wire format.
func (o BackendOutcome) String() string {
	switch o {
	case BackendOutcomeOk:
		return "ok"
	case BackendOutcomeNetworkError:
		return "network-error"
	case BackendOutcomeSyntheticFailure:
		return "synthetic-failure"
	case BackendOutcomeCancelled:
		return "cancelled"
	case BackendOutcomeIncomplete:
		return "incomplete"
	case BackendOutcomeOrphaned:
		return "orphaned"
	}
	return "unknown"
}

// MarshalJSON encodes a RequestTrace into the native wire schema. Internal
// representations (uint16 hostcall name indices, fixed-size tag slots,
// enum outcomes) are resolved to their stable wire form so consumers do
// not have to know about the in-memory layout. The schema is the contract
// every encoder (Firefox Gecko, Chrome Tracing, pprof) and the UI build
// on; changes here are breaking and need to land alongside golden-file
// updates.
func (t *RequestTrace) MarshalJSON() ([]byte, error) {
	if t == nil {
		return []byte("null"), nil
	}
	spans := make([]spanJSON, len(t.Spans))
	for i, s := range t.Spans {
		spans[i] = spanJSON{
			Name:     ResolveHostcallName(s.NameIdx),
			Start:    s.Start,
			Duration: s.Duration,
			RC:       s.RC,
			TagSlots: s.TagSlots,
		}
	}
	// SnapshotNativeSamples returns a stable copy under the per-trace
	// RWMutex so a concurrent MergeNativeSamples cannot race the loop.
	nativeRaw := t.SnapshotNativeSamples()
	var samples []nativeSampleJSON
	if len(nativeRaw) > 0 {
		samples = make([]nativeSampleJSON, len(nativeRaw))
		for i, s := range nativeRaw {
			samples[i] = nativeSampleJSON(s)
		}
	}
	calls := make([]backendCallJSON, len(t.BackendCalls))
	for i, c := range t.BackendCalls {
		calls[i] = backendCallJSON{
			PendingID:       c.PendingID,
			Name:            c.Name,
			Method:          c.Method,
			URLRedacted:     c.URLRedacted,
			Started:         c.Started,
			DNSNanos:        c.DNSNanos,
			ConnectNanos:    c.ConnectNanos,
			TLSNanos:        c.TLSNanos,
			TTFBNanos:       c.TTFBNanos,
			TotalNanos:      c.TotalNanos,
			Status:          c.Status,
			ReqHeaderBytes:  c.ReqHeaderBytes,
			RespHeaderBytes: c.RespHeaderBytes,
			Outcome:         c.Outcome.String(),
		}
	}
	var deep *deepMetricsJSON
	if t.Deep != nil {
		access := make([]storeAccessJSON, len(t.Deep.StoreAccess))
		for i, sa := range t.Deep.StoreAccess {
			access[i] = storeAccessJSON(sa)
		}
		reqHeaders := make([]headerSummaryJSON, len(t.Deep.RequestHeaders))
		for i, h := range t.Deep.RequestHeaders {
			reqHeaders[i] = headerSummaryJSON(h)
		}
		respHeaders := make([]headerSummaryJSON, len(t.Deep.ResponseHeaders))
		for i, h := range t.Deep.ResponseHeaders {
			respHeaders[i] = headerSummaryJSON(h)
		}
		var heapSamples []heapSampleJSON
		if len(t.Deep.HeapSamples) > 0 {
			heapSamples = make([]heapSampleJSON, len(t.Deep.HeapSamples))
			for i, s := range t.Deep.HeapSamples {
				heapSamples[i] = heapSampleJSON(s)
			}
		}
		deep = &deepMetricsJSON{
			BodyReadBytes:      t.Deep.BodyReadBytes,
			BodyWriteBytes:     t.Deep.BodyWriteBytes,
			CacheLookups:       t.Deep.CacheLookups,
			CacheInserts:       t.Deep.CacheInserts,
			CacheHits:          t.Deep.CacheHits,
			CacheMisses:        t.Deep.CacheMisses,
			CacheStale:         t.Deep.CacheStale,
			RequestHeaders:     reqHeaders,
			ResponseHeaders:    respHeaders,
			HeapSamples:        heapSamples,
			HeapSamplesDropped: t.Deep.HeapSamplesDropped,
			StoreAccess:        access,
		}
	}
	return json.Marshal(traceJSON{
		ReqID:               t.ReqID,
		ModuleID:            t.ModuleID,
		Method:              t.Method,
		URL:                 t.URL,
		Status:              t.Status,
		WallStart:           t.WallStart,
		WallNanos:           t.WallNanos,
		HeaderFlushNanos:    t.HeaderFlushNanos,
		GuestActiveNanos:    t.GuestActiveNanos,
		HostcallNanos:       t.HostcallNanos,
		NativeCPUNanos:      t.NativeCPUNanos,
		HijackedNanos:       t.HijackedNanos,
		Spans:               spans,
		BackendCalls:        calls,
		NativeSamples:       samples,
		Dropped:             t.Dropped,
		DroppedBackendCalls: t.DroppedBackendCalls,
		Outcome:             t.Outcome.String(),
		Notes:               t.Notes,
		Deep:                deep,
	})
}

// ResolveHostcallName looks up the interned name. Out-of-range indices fall
// back to the sentinel so a forward-compatible reader never panics on a
// trace produced by a newer fastlike with new hostcalls in the table.
func ResolveHostcallName(idx uint16) string {
	if int(idx) < len(hostcallNames) {
		return hostcallNames[idx]
	}
	return hostcallNames[0]
}

type traceJSON struct {
	ReqID               uint64             `json:"req_id"`
	ModuleID            string             `json:"module_id"`
	Method              string             `json:"method"`
	URL                 string             `json:"url"`
	Status              int                `json:"status"`
	WallStart           time.Time          `json:"wall_start"`
	WallNanos           int64              `json:"wall_nanos"`
	HeaderFlushNanos    *int64             `json:"header_flush_nanos,omitempty"`
	GuestActiveNanos    int64              `json:"guest_active_nanos"`
	HostcallNanos       int64              `json:"hostcall_nanos"`
	NativeCPUNanos      *int64             `json:"native_cpu_nanos,omitempty"`
	HijackedNanos       *int64             `json:"hijacked_nanos,omitempty"`
	Spans               []spanJSON         `json:"spans"`
	BackendCalls        []backendCallJSON  `json:"backend_calls"`
	NativeSamples       []nativeSampleJSON `json:"native_samples,omitempty"`
	Dropped             int                `json:"dropped"`
	DroppedBackendCalls int                `json:"dropped_backend_calls"`
	Outcome             string             `json:"outcome"`
	Notes               []string           `json:"notes,omitempty"`
	Deep                *deepMetricsJSON   `json:"deep,omitempty"`
}

type nativeSampleJSON struct {
	RelativeNanos int64  `json:"relative_nanos"`
	Function      string `json:"function"`
}

type deepMetricsJSON struct {
	BodyReadBytes      int64               `json:"body_read_bytes"`
	BodyWriteBytes     int64               `json:"body_write_bytes"`
	CacheLookups       int                 `json:"cache_lookups"`
	CacheInserts       int                 `json:"cache_inserts"`
	CacheHits          int                 `json:"cache_hits"`
	CacheMisses        int                 `json:"cache_misses"`
	CacheStale         int                 `json:"cache_stale"`
	RequestHeaders     []headerSummaryJSON `json:"request_headers,omitempty"`
	ResponseHeaders    []headerSummaryJSON `json:"response_headers,omitempty"`
	HeapSamples        []heapSampleJSON    `json:"heap_samples,omitempty"`
	HeapSamplesDropped int                 `json:"heap_samples_dropped,omitempty"`
	StoreAccess        []storeAccessJSON   `json:"store_access,omitempty"`
}

type headerSummaryJSON struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Bytes int    `json:"bytes"`
}

type heapSampleJSON struct {
	RelativeNanos int64 `json:"relative_nanos"`
	MemoryBytes   int64 `json:"memory_bytes"`
}

type storeAccessJSON struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type spanJSON struct {
	Name     string   `json:"name"`
	Start    int64    `json:"start_nanos"`
	Duration int64    `json:"duration_nanos"`
	RC       int32    `json:"rc"`
	TagSlots [4]int64 `json:"tag_slots"`
}

type backendCallJSON struct {
	PendingID       uint32 `json:"pending_id"`
	Name            string `json:"name"`
	Method          string `json:"method"`
	URLRedacted     string `json:"url_redacted"`
	Started         int64  `json:"started_nanos"`
	DNSNanos        *int64 `json:"dns_nanos,omitempty"`
	ConnectNanos    *int64 `json:"connect_nanos,omitempty"`
	TLSNanos        *int64 `json:"tls_nanos,omitempty"`
	TTFBNanos       *int64 `json:"ttfb_nanos,omitempty"`
	TotalNanos      int64  `json:"total_nanos"`
	Status          int    `json:"status"`
	ReqHeaderBytes  int    `json:"req_header_bytes"`
	RespHeaderBytes int    `json:"resp_header_bytes"`
	Outcome         string `json:"outcome"`
}

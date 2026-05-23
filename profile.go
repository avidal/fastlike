package fastlike

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ProfileMode selects the breadth of profiling instrumentation enabled for a
// Fastlike value. Modes are additive: combined implies native implies trace;
// deep implies combined.
type ProfileMode string

const (
	ProfileModeOff      ProfileMode = "off"
	ProfileModeTrace    ProfileMode = "trace"
	ProfileModeNative   ProfileMode = "native"
	ProfileModeCombined ProfileMode = "combined"
	ProfileModeDeep     ProfileMode = "deep"
)

// includesTrace reports whether the mode includes the always-on hostcall trace.
func (m ProfileMode) includesTrace() bool {
	switch m {
	case ProfileModeTrace, ProfileModeNative, ProfileModeCombined, ProfileModeDeep:
		return true
	}
	return false
}

// TraceOutcome categorises how a request exited.
type TraceOutcome uint8

const (
	TraceOutcomeNormal TraceOutcome = iota
	TraceOutcomeTrap
	TraceOutcomePanic
	TraceOutcomeLoopFail
	TraceOutcomeCtxCanceled
)

// String returns the lowercase wire form used in JSON encoders and the UI.
func (o TraceOutcome) String() string {
	switch o {
	case TraceOutcomeNormal:
		return "normal"
	case TraceOutcomeTrap:
		return "trap"
	case TraceOutcomePanic:
		return "panic"
	case TraceOutcomeLoopFail:
		return "loop-fail"
	case TraceOutcomeCtxCanceled:
		return "ctx-canceled"
	}
	return "unknown"
}

// BackendOutcome categorises how a backend call resolved.
type BackendOutcome uint8

const (
	BackendOutcomeOk BackendOutcome = iota
	BackendOutcomeNetworkError
	BackendOutcomeSyntheticFailure
	BackendOutcomeCancelled
	BackendOutcomeIncomplete
	BackendOutcomeOrphaned
)

// RequestTrace is the per-request capture produced by the profile recorder.
// All time fields are nanoseconds; absolute times are nanos since WallStart.
type RequestTrace struct {
	ReqID               uint64
	ModuleID            string
	Method              string
	URL                 string
	Status              int
	WallStart           time.Time
	WallNanos           int64
	HeaderFlushNanos    *int64
	GuestActiveNanos    int64
	HostcallNanos       int64
	NativeCPUNanos      *int64
	HijackedNanos       *int64
	Spans               []Span
	BackendCalls        []BackendCall
	NativeSamples       []NativeSample
	Dropped             int
	DroppedBackendCalls int
	Outcome             TraceOutcome
	Notes               []string

	// Deep is the deep-mode metrics struct, populated only when the
	// parent Fastlike was built with ProfileModeDeep. nil on every
	// other mode. Encoders and the UI MUST branch on this pointer
	// before reading any DeepMetrics field.
	Deep *DeepMetrics

	// nativeSamplesMu guards post-completion mutations to NativeSamples.
	// Trace fields are otherwise written once by the request goroutine
	// before completeTrace publishes the trace, but NativeSamples can
	// be appended later by MergeNativeSamples while the UI is iterating
	// the slice. Encoders and the UI must read NativeSamples through
	// SnapshotNativeSamples (or take the RLock themselves) to stay
	// race-free with concurrent merges.
	nativeSamplesMu sync.RWMutex
}

// SnapshotNativeSamples returns a defensive copy of t.NativeSamples
// captured under the per-trace RWMutex. Encoders and the UI use this
// rather than ranging t.NativeSamples directly so a concurrent
// MergeNativeSamples cannot race the iteration.
func (t *RequestTrace) SnapshotNativeSamples() []NativeSample {
	t.nativeSamplesMu.RLock()
	defer t.nativeSamplesMu.RUnlock()
	if len(t.NativeSamples) == 0 {
		return nil
	}
	out := make([]NativeSample, len(t.NativeSamples))
	copy(out, t.NativeSamples)
	return out
}

// appendNativeSamplesLocked appends each sample to t.NativeSamples
// under the per-trace write lock. Used by MergeNativeSamples; not
// part of the public API.
func (t *RequestTrace) appendNativeSamples(samples ...NativeSample) {
	if len(samples) == 0 {
		return
	}
	t.nativeSamplesMu.Lock()
	t.NativeSamples = append(t.NativeSamples, samples...)
	t.nativeSamplesMu.Unlock()
}

// NativeSample is one sampled wasm function as captured by an external
// profiler (perf, samply) and joined back to the trace by PID + time
// window. The sample's RelativeNanos is measured from the parent
// trace's WallStart, so the same X axis the canvas viewer uses for
// hostcall and backend tracks applies directly. Function is the
// resolved wasm function name from the jitdump symbols (resolved by
// the external profiler before we see it); when the profiler couldn't
// resolve the frame, the field falls back to the raw hex address.
//
// Samples are advisory: any number can attach to a trace, including
// zero. The JSON encoder omits the entire native_samples key when the
// slice is empty so non-native runs do not pollute the schema.
type NativeSample struct {
	RelativeNanos int64
	Function      string
}

// Span is one hostcall invocation. NameIdx indexes hostcallNames.
type Span struct {
	NameIdx   uint16
	Start     int64
	Duration  int64
	RC        int32
	TagSlots  [4]int64
	TagStrIdx [2]uint16
}

// BackendCall is one round trip to a backend handler.
type BackendCall struct {
	PendingID       uint32
	Name            string
	Method          string
	URLRedacted     string
	Started         int64
	DNSNanos        *int64
	ConnectNanos    *int64
	TLSNanos        *int64
	TTFBNanos       *int64
	TotalNanos      int64
	Status          int
	ReqHeaderBytes  int
	RespHeaderBytes int
	Outcome         BackendOutcome
}

// ProfileStore owns the per-Fastlike retention, in-flight set, and configuration
// for the viewer. A nil *ProfileStore means profiling is disabled.
type ProfileStore struct {
	mu        sync.RWMutex
	retain    int
	completed []*RequestTrace
	inFlight  map[uint64]*RequestTrace
	nextReqID atomic.Uint64

	uiAddr      string
	uiAuthToken string
	uiInsecure  bool
	dir         string
	asyncGrace  time.Duration
	backendCap  int
}

// NewProfileStore returns a store with default retention, async grace, and
// backend cap. FastlikeOption constructors mutate these defaults.
func NewProfileStore() *ProfileStore {
	return &ProfileStore{
		retain:     defaultProfileRetain,
		inFlight:   make(map[uint64]*RequestTrace),
		asyncGrace: defaultProfileAsyncGrace,
		backendCap: defaultProfileBackendCap,
	}
}

const (
	defaultProfileRetain     = 256
	defaultProfileAsyncGrace = 100 * time.Millisecond
	defaultProfileBackendCap = 512
	defaultProfileSpanCap    = 8000
)

// AsyncGrace returns the configured grace period for in-flight async backends.
func (s *ProfileStore) AsyncGrace() time.Duration { return s.asyncGrace }

// BackendCap returns the configured per-request backend-call cap.
func (s *ProfileStore) BackendCap() int { return s.backendCap }

// UIAddr returns the configured UI bind address. Empty means no listener.
func (s *ProfileStore) UIAddr() string { return s.uiAddr }

// Dir returns the configured archive directory, or empty if disk archival is off.
func (s *ProfileStore) Dir() string { return s.dir }

// Retain returns the configured LRU size for completed traces.
func (s *ProfileStore) Retain() int { return s.retain }

// Recent returns up to n most recent completed traces, most recent first.
// Pass n <= 0 to fetch every retained trace.
func (s *ProfileStore) Recent(n int) []*RequestTrace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := len(s.completed)
	if n <= 0 || n > total {
		n = total
	}
	out := make([]*RequestTrace, n)
	for i := 0; i < n; i++ {
		out[i] = s.completed[total-1-i]
	}
	return out
}

// Get returns the completed trace with the given request id, or nil.
func (s *ProfileStore) Get(reqID uint64) *RequestTrace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.completed {
		if t.ReqID == reqID {
			return t
		}
	}
	return nil
}

// InFlight returns a snapshot of currently-running traces.
func (s *ProfileStore) InFlight() []*RequestTrace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*RequestTrace, 0, len(s.inFlight))
	for _, t := range s.inFlight {
		out = append(out, t)
	}
	return out
}

// newRequestTrace allocates a trace, assigns a request id, and registers it
// as in-flight. The caller owns the returned pointer until completeTrace
// hands it back to the store.
func (s *ProfileStore) newRequestTrace(moduleID string, r *http.Request) *RequestTrace {
	if s == nil {
		return nil
	}
	backendCap := s.backendCap
	if backendCap <= 0 {
		backendCap = defaultProfileBackendCap
	}
	t := &RequestTrace{
		ReqID:        s.nextReqID.Add(1),
		ModuleID:     moduleID,
		Method:       r.Method,
		URL:          r.URL.String(),
		WallStart:    time.Now(),
		Spans:        make([]Span, 0, defaultProfileSpanCap),
		BackendCalls: make([]BackendCall, 0, backendCap),
	}
	s.mu.Lock()
	s.inFlight[t.ReqID] = t
	s.mu.Unlock()
	return t
}

// completeTrace moves a trace from in-flight to completed, evicting the
// oldest entry when retention is exceeded.
func (s *ProfileStore) completeTrace(t *RequestTrace) {
	if s == nil || t == nil {
		return
	}
	s.mu.Lock()
	delete(s.inFlight, t.ReqID)
	s.completed = append(s.completed, t)
	if s.retain > 0 && len(s.completed) > s.retain {
		s.completed = s.completed[len(s.completed)-s.retain:]
	}
	s.mu.Unlock()
}

// profileCompileConfig is the compile-time profile configuration consumed by
// Instance.compile. Carried from FastlikeOptions into Fastlike, then passed
// down to compile() before NewModule is called. Engine-level wiring lives in
// step 6; step 1 only plumbs the struct so the surface is stable.
type profileCompileConfig struct {
	mode ProfileMode
}

// profileBinding is the per-instance pointer back to the parent Fastlike's
// profile store, captured at instance construction time. nil means profiling
// is disabled for that instance.
type profileBinding struct {
	store       *ProfileStore
	moduleID    string
	deepEnabled bool // true when the configured mode is ProfileModeDeep
}

// moduleIDOf returns a short stable identifier for wasmbytes, formed from the
// first eight bytes of its SHA-256 digest. Used to tag traces with the module
// version they ran against so reloads do not silently merge before/after data.
func moduleIDOf(wasmbytes []byte) string {
	sum := sha256.Sum256(wasmbytes)
	return hex.EncodeToString(sum[:8])
}

// hostcallNames is the package-level interned table of hostcall names. Spans
// store the index, not the string, so per-call recording allocates nothing.
// Order is fixed; new entries append, never insert, so existing indices remain
// stable across releases. Index 0 is reserved as the "unknown" sentinel.
var hostcallNames = []string{
	"<unknown>",
	"original_header_count",
	"header_value_get",
	"header_remove",
	"is_request_cacheable",
	"get_suggested_cache_key",
	"lookup",
	"transaction_lookup",
	"transaction_insert",
	"transaction_insert_and_stream_back",
	"transaction_update",
	"transaction_update_and_return_fresh",
	"transaction_record_not_cacheable",
	"transaction_abandon",
	"close",
	"get_suggested_backend_request",
	"get_suggested_cache_options",
	"prepare_response_for_storage",
	"get_found_response",
	"get_state",
	"get_length",
	"get_max_age_ns",
	"get_stale_while_revalidate_ns",
	"get_age_ns",
	"get_hits",
	"get_sensitive_data",
	"get_surrogate_keys",
	"get_vary_rule",
	"get_stale_if_error_ns",
	"transaction_choose_stale",
	"init",
	"parse",
	"body_downstream_get",
	"downstream_client_ip_addr",
	"new",
	"version_get",
	"version_set",
	"method_get",
	"method_set",
	"uri_get",
	"uri_set",
	"header_names_get",
	"header_insert",
	"header_append",
	"header_values_get",
	"header_values_set",
	"send",
	"send_v2",
	"send_v3",
	"send_async",
	"send_async_streaming",
	"send_async_v2",
	"pending_req_poll",
	"pending_req_poll_v2",
	"pending_req_wait",
	"pending_req_wait_v2",
	"pending_req_select",
	"pending_req_select_v2",
	"cache_override_set",
	"cache_override_v2_set",
	"original_header_names_get",
	"downstream_client_ddos_detected",
	"fastly_key_is_valid",
	"downstream_compliance_region",
	"on_behalf_of",
	"framing_headers_mode_set",
	"auto_decompress_response_set",
	"register_dynamic_backend",
	"inspect",
	"downstream_client_h2_fingerprint",
	"downstream_client_oh_fingerprint",
	"downstream_tls_ja3_md5",
	"downstream_tls_ja4",
	"downstream_tls_protocol",
	"downstream_tls_cipher_openssl_name",
	"downstream_tls_client_hello",
	"downstream_tls_client_servername",
	"downstream_tls_raw_client_certificate",
	"downstream_tls_client_cert_verify_result",
	"send_downstream",
	"status_get",
	"status_set",
	"http_keepalive_mode_set",
	"get_addr_dest_ip",
	"get_addr_dest_port",
	"send_informational_response",
	"write",
	"read",
	"append",
	"abandon",
	"known_length",
	"trailer_append",
	"trailer_names_get",
	"trailer_value_get",
	"trailer_values_get",
	"endpoint_get",
	"open",
	"get",
	"plaintext",
	"from_bytes",
	"transform_image_optimizer_request",
	"check_rate",
	"ratecounter_increment",
	"ratecounter_lookup_rate",
	"ratecounter_lookup_count",
	"penaltybox_add",
	"penaltybox_has",
	"open_v2",
	"lookup_v2",
	"lookup_wait",
	"lookup_wait_v2",
	"insert",
	"insert_wait",
	"delete",
	"delete_wait",
	"list",
	"list_wait",
	"lookup_async",
	"pending_lookup_wait",
	"insert_async",
	"pending_insert_wait",
	"delete_async",
	"pending_delete_wait",
	"exists",
	"is_healthy",
	"is_dynamic",
	"get_host",
	"get_override_host",
	"get_port",
	"get_connect_timeout_ms",
	"get_first_byte_timeout_ms",
	"get_between_bytes_timeout_ms",
	"is_ssl",
	"get_ssl_min_version",
	"get_ssl_max_version",
	"get_http_keepalive_time",
	"get_tcp_keepalive_enable",
	"get_tcp_keepalive_interval",
	"get_tcp_keepalive_probes",
	"get_tcp_keepalive_time",
	"is_ipv6_preferred",
	"get_max_connections",
	"get_max_use",
	"get_max_lifetime_ms",
	"get_vcpu_ms",
	"get_heap_mib",
	"get_sandbox_id",
	"get_trace_id",
	"get_service_version",
	"get_hostname",
	"transaction_lookup_async",
	"cache_busy_handle_wait",
	"transaction_cancel",
	"close_busy",
	"get_user_metadata",
	"get_body",
	"replace",
	"replace_insert",
	"replace_get_age_ns",
	"replace_get_body",
	"replace_get_hits",
	"replace_get_length",
	"replace_get_max_age_ns",
	"replace_get_stale_while_revalidate_ns",
	"replace_get_state",
	"replace_get_user_metadata",
	"purge_surrogate_key",
	"select",
	"is_ready",
	"next_request",
	"next_request_wait",
	"next_request_abandon",
	"downstream_original_header_names",
	"downstream_original_header_count",
	"downstream_client_request_id",
	"downstream_server_ip_addr",
	"downstream_bot_analyzed",
	"downstream_bot_detected",
	"downstream_bot_name",
	"downstream_bot_category",
	"downstream_bot_category_kind",
	"downstream_bot_verified",
	"downstream_resvpnproxy_is_anonymous",
	"downstream_resvpnproxy_is_anonymous_vpn",
	"downstream_resvpnproxy_is_hosting_provider",
	"downstream_resvpnproxy_is_proxy_over_vpn",
	"downstream_resvpnproxy_is_public_proxy",
	"downstream_resvpnproxy_is_relay_proxy",
	"downstream_resvpnproxy_is_residential_proxy",
	"downstream_resvpnproxy_is_smart_dns_proxy",
	"downstream_resvpnproxy_is_tor_exit_node",
	"downstream_resvpnproxy_is_vpn_datacenter",
	"downstream_resvpnproxy_vpn_service_name",
	"shield_info",
	"backend_for_shield",
	"xqd_req_original_header_count",
	"xqd_resp_header_value_get",
	"xqd_body_close_downstream",
	"xqd_req_body_downstream_get",
	"xqd_resp_send_downstream",
	"xqd_req_downstream_client_ip_addr",
	"xqd_req_new",
	"xqd_req_version_get",
	"xqd_req_version_set",
	"xqd_req_method_get",
	"xqd_req_method_set",
	"xqd_req_uri_get",
	"xqd_req_uri_set",
	"xqd_req_header_remove",
	"xqd_req_header_insert",
	"xqd_req_header_append",
	"xqd_req_header_names_get",
	"xqd_req_header_value_get",
	"xqd_req_header_values_get",
	"xqd_req_header_values_set",
	"xqd_req_send",
	"xqd_req_send_v2",
	"xqd_req_send_v3",
	"xqd_req_send_async",
	"xqd_req_send_async_streaming",
	"xqd_req_send_async_v2",
	"xqd_pending_req_poll",
	"xqd_pending_req_poll_v2",
	"xqd_pending_req_wait",
	"xqd_pending_req_wait_v2",
	"xqd_pending_req_select",
	"xqd_pending_req_select_v2",
	"xqd_req_cache_override_set",
	"xqd_req_cache_override_v2_set",
	"xqd_req_original_header_names_get",
	"xqd_req_close",
	"xqd_req_downstream_client_ddos_detected",
	"xqd_req_fastly_key_is_valid",
	"xqd_req_downstream_compliance_region",
	"xqd_req_on_behalf_of",
	"xqd_req_framing_headers_mode_set",
	"xqd_req_auto_decompress_response_set",
	"xqd_req_register_dynamic_backend",
	"xqd_resp_new",
	"xqd_resp_status_get",
	"xqd_resp_status_set",
	"xqd_resp_version_get",
	"xqd_resp_version_set",
	"xqd_resp_header_remove",
	"xqd_resp_header_insert",
	"xqd_resp_header_append",
	"xqd_resp_header_names_get",
	"xqd_resp_header_values_get",
	"xqd_resp_header_values_set",
	"xqd_resp_close",
	"xqd_resp_framing_headers_mode_set",
	"xqd_resp_http_keepalive_mode_set",
	"xqd_resp_get_addr_dest_ip",
	"xqd_resp_get_addr_dest_port",
	"xqd_body_new",
	"xqd_body_write",
	"xqd_body_read",
	"xqd_body_append",
	"xqd_body_abandon",
	"xqd_body_known_length",
	"xqd_body_trailer_append",
	"xqd_body_trailer_names_get",
	"xqd_body_trailer_value_get",
	"xqd_body_trailer_values_get",
	"xqd_log_endpoint_get",
	"xqd_log_write",
	"xqd_image_optimizer_transform_request",
	"xqd_acl_open",
	"xqd_acl_lookup",
	"xqd_erl_check_rate",
	"xqd_erl_ratecounter_increment",
	"xqd_erl_ratecounter_lookup_rate",
	"xqd_erl_ratecounter_lookup_count",
	"xqd_erl_penaltybox_add",
	"xqd_erl_penaltybox_has",
	"xqd_compute_runtime_get_vcpu_ms",
	"xqd_compute_runtime_get_heap_mib",
	"xqd_async_io_select",
	"xqd_async_io_is_ready",
	"xqd_http_downstream_next_request",
	"xqd_http_downstream_next_request_wait",
	"xqd_http_downstream_next_request_abandon",
	"xqd_http_downstream_original_header_names",
	"xqd_http_downstream_original_header_count",
	"xqd_req_downstream_tls_cipher_openssl_name",
	"xqd_req_downstream_tls_protocol",
	"xqd_req_downstream_tls_client_servername",
	"xqd_req_downstream_tls_client_hello",
	"xqd_req_downstream_tls_raw_client_certificate",
	"xqd_req_downstream_tls_client_cert_verify_result",
	"xqd_req_downstream_client_h2_fingerprint",
	"xqd_req_downstream_client_oh_fingerprint",
	"xqd_req_downstream_tls_ja3_md5",
	"xqd_req_downstream_tls_ja4",
	"xqd_http_downstream_bot_analyzed",
	"xqd_http_downstream_bot_detected",
	"xqd_http_downstream_bot_name",
	"xqd_http_downstream_bot_category",
	"xqd_http_downstream_bot_category_kind",
	"xqd_http_downstream_bot_verified",
	"xqd_shield_info",
	"xqd_backend_for_shield",
}

var hostcallNameLookup = func() map[string]uint16 {
	m := make(map[string]uint16, len(hostcallNames))
	for idx, name := range hostcallNames {
		m[name] = uint16(idx)
	}
	return m
}()

func hostcallNameIndex(name string) uint16 {
	if idx, ok := hostcallNameLookup[name]; ok {
		return idx
	}
	return 0
}

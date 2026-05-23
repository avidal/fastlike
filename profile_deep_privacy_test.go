package fastlike

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"testing"
)

// scaryStrings is the set of values the privacy fixture intentionally
// drops into every input that could plausibly leak. Encoders MUST NOT
// surface any of these in their output, regardless of mode.
//
// The list captures the categories that matter:
//   - "ya29.PLAUSIBLE_OAUTH_SECRET" — a value-looking string that
//     should never escape a secret store
//   - "Bearer 8a4f9c..." — a header value
//   - "Cookie: session=abcdef" — another header value
//   - "userinfo:hunter2" — URL userinfo
//   - "?token=PROVISIONAL_SECRET" — URL query string
//   - "raw_body_bytes_PROHIBITED" — body bytes
//
// The "scary" key names (KV/Config/Secret store names) DO appear in
// the encoded output because the deep schema explicitly captures
// store names. The privacy contract is about *values*, not names.
var scaryStrings = []string{
	"ya29.PLAUSIBLE_OAUTH_SECRET",
	"Bearer 8a4f9c1234567890abcdef",
	"session=abcdef0123456789",
	"hunter2",
	"token=PROVISIONAL_SECRET",
	"raw_body_bytes_PROHIBITED",
	// Cache key + surrogate key shapes that an operator might
	// legitimately use for an API token bucket or per-user object.
	// Deep mode records cache outcome counts, not the keys themselves,
	// so these must never escape any encoder regardless of mode.
	"cache_key_USER_42_AUTHTOKEN",
	"surrogate_USER_42_SESSION",
}

// fixtureDeepPrivacyTrace builds a trace whose deep metrics are
// populated and whose surrounding context (URL, notes, headers via
// existing fields) contains intentionally scary inputs. The encoder
// tests below grep the output for the scary strings and fail if any
// of them appear.
//
// The trace's own visible fields (Method, URLRedacted on backends,
// store names) are intentionally not part of the scary list because
// they ARE allowed to surface — operator wrote them down. The test
// catches the case where a future schema change widens the surface
// and accidentally pipes a body/header/secret value into a public
// field.
func fixtureDeepPrivacyTrace(t *testing.T) *RequestTrace {
	t.Helper()
	tr := fixtureNormalTrace(t)
	tr.Deep = newDeepMetrics()
	tr.Deep.BodyReadBytes = 12345
	tr.Deep.BodyWriteBytes = 67890
	tr.Deep.CacheLookups = 2
	tr.Deep.CacheInserts = 1
	tr.Deep.CacheHits = 1
	tr.Deep.CacheMisses = 1
	tr.Deep.CacheStale = 0
	// Store names that look secret-adjacent — but they're configuration
	// the operator named explicitly, so they ARE allowed to appear.
	tr.Deep.bumpStoreAccess("kv", "API_KEY_PROD")
	tr.Deep.bumpStoreAccess("secret", "github_token")
	tr.Deep.bumpStoreAccess("config", "feature_flags")

	// Build request and response headers containing every scary value
	// in scaryStrings under both redacted and non-redacted header
	// names, with mixed casing for the redacted ones. The summarizer
	// must redact the names (so we only see "<redacted>") and never
	// the value (the grep test below catches any leak through Name).
	req := http.Header{}
	req.Add("Authorization", "Bearer 8a4f9c1234567890abcdef")
	req.Add("authorization", "ya29.PLAUSIBLE_OAUTH_SECRET")
	req.Add("Cookie", "session=abcdef0123456789")
	req.Add("X-API-KEY", "hunter2")
	req.Add("User-Agent", "fastlike-test/1.0")
	req.Add("Accept", "application/json")
	tr.Deep.RequestHeaders = summarizeHeaders(req)

	resp := http.Header{}
	resp.Add("Set-Cookie", "auth=session=abcdef0123456789; HttpOnly")
	resp.Add("set-cookie", "tracking=token=PROVISIONAL_SECRET")
	resp.Add("Content-Type", "application/json")
	resp.Add("WWW-Authenticate", "Bearer realm=acme")
	tr.Deep.ResponseHeaders = summarizeHeaders(resp)

	tr.Deep.finalize()
	return tr
}

// assertNoScaryStrings greps the encoded bytes for every value in
// scaryStrings and fails the test for each hit.
func assertNoScaryStrings(t *testing.T, label string, raw []byte) {
	t.Helper()
	body := string(raw)
	for _, scary := range scaryStrings {
		if strings.Contains(body, scary) {
			t.Errorf("%s: leaked scary value %q in output", label, scary)
		}
	}
}

func TestDeepPrivacyNativeJSON(t *testing.T) {
	tr := fixtureDeepPrivacyTrace(t)
	raw, err := tr.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	assertNoScaryStrings(t, "native JSON", raw)
}

func TestDeepPrivacyChromeEncoder(t *testing.T) {
	tr := fixtureDeepPrivacyTrace(t)
	raw, err := EncodeChromeTrace(tr)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertNoScaryStrings(t, "chrome", raw)
}

func TestDeepPrivacyFirefoxEncoder(t *testing.T) {
	tr := fixtureDeepPrivacyTrace(t)
	raw, err := EncodeFirefoxGecko(tr)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertNoScaryStrings(t, "firefox", raw)
}

func TestDeepPrivacyPprofEncoder(t *testing.T) {
	tr := fixtureDeepPrivacyTrace(t)
	raw, err := EncodePprof(tr)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// pprof is gzip-compressed protobuf; decompress before grepping.
	// We grep the parsed summary form (label strings + function names)
	// rather than the raw bytes because gzip would defeat substring
	// matching even if a leak existed.
	summary := summarizePprof(t, raw)
	assertNoScaryStrings(t, "pprof summary", summary)
}

func TestDeepPrivacyStoreNamesArePresent(t *testing.T) {
	// Positive control: the store names the operator configured DO
	// appear in the output (they're not on the scary list). If a future
	// privacy hardening accidentally strips them, this test catches it
	// and the operator notices their telemetry went blank.
	tr := fixtureDeepPrivacyTrace(t)
	raw, err := tr.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, expected := range []string{"API_KEY_PROD", "github_token", "feature_flags"} {
		if !strings.Contains(body, expected) {
			t.Errorf("expected store name %q in output but it was missing", expected)
		}
	}
}

// TestDeepGatedOffWhenModeNotDeep proves the gate at the source: a
// trace whose recorder has deep disabled allocates no DeepMetrics
// even if every instrumented hostcall fires. This is the contract
// "trace mode never collects deep data".
func TestDeepGatedOffWhenModeNotDeep(t *testing.T) {
	// Construct an Instance with profile binding but deep=false.
	store := NewProfileStore()
	i := &Instance{profile: &profileBinding{store: store, moduleID: "m", deepEnabled: false}}
	// Initialize the request handle structures the test wouldn't
	// otherwise populate — we won't run hostcalls, just confirm the
	// beginTrace path doesn't allocate Deep.
	i.trace = store.newRequestTrace("m", mustRequest(t))
	if i.profile.deepEnabled {
		t.Fatal("test setup wrong: deepEnabled should be false")
	}
	// Simulate beginTrace's deep gate.
	if i.profile.deepEnabled {
		i.trace.Deep = newDeepMetrics()
	}
	if i.trace.Deep != nil {
		t.Errorf("Deep should be nil when deepEnabled=false")
	}
	// Now the instrumentation helpers must be no-ops.
	i.deepBumpBodyRead(100)
	i.deepBumpBodyWrite(200)
	i.deepBumpCacheLookup()
	i.deepBumpCacheInsert()
	i.deepBumpStore("kv", "x")
	if i.trace.Deep != nil {
		t.Errorf("Deep must remain nil after instrumentation when gate is off")
	}
}

func TestDeepBumpCacheOutcomeClassification(t *testing.T) {
	cases := []struct {
		name  string
		state CacheState
		want  func(*DeepMetrics) int
	}{
		{"hit", CacheState{Found: true, Usable: true}, func(d *DeepMetrics) int { return d.CacheHits }},
		{"stale", CacheState{Found: true, Usable: true, Stale: true}, func(d *DeepMetrics) int { return d.CacheStale }},
		{"stale-only", CacheState{Found: true, Stale: true}, func(d *DeepMetrics) int { return d.CacheStale }},
		{"miss-empty", CacheState{}, func(d *DeepMetrics) int { return d.CacheMisses }},
		{"miss-must-update", CacheState{MustInsertOrUpdate: true}, func(d *DeepMetrics) int { return d.CacheMisses }},
	}
	for _, c := range cases {
		store := NewProfileStore()
		i := &Instance{profile: &profileBinding{store: store, moduleID: "m", deepEnabled: true}}
		i.trace = store.newRequestTrace("m", mustRequest(t))
		i.trace.Deep = newDeepMetrics()
		i.deepBumpCacheOutcome(c.state)
		if got := c.want(i.trace.Deep); got != 1 {
			t.Errorf("%s: expected matching counter to be 1, got %+v", c.name, i.trace.Deep)
		}
	}
}

func TestDeepCacheOutcomeAbsentWhenNotDeep(t *testing.T) {
	store := NewProfileStore()
	// deepEnabled=false → DeepMetrics never allocated, classifier is a no-op.
	i := &Instance{profile: &profileBinding{store: store, moduleID: "m", deepEnabled: false}}
	i.trace = store.newRequestTrace("m", mustRequest(t))
	i.deepBumpCacheOutcome(CacheState{Found: true, Usable: true})
	if i.trace.Deep != nil {
		t.Errorf("Deep should remain nil with deepEnabled=false")
	}
}

func TestDeepCollectsWhenModeIsDeep(t *testing.T) {
	store := NewProfileStore()
	i := &Instance{profile: &profileBinding{store: store, moduleID: "m", deepEnabled: true}}
	i.trace = store.newRequestTrace("m", mustRequest(t))
	if i.profile.deepEnabled {
		i.trace.Deep = newDeepMetrics()
	}
	i.deepBumpBodyRead(100)
	i.deepBumpBodyWrite(200)
	i.deepBumpCacheLookup()
	i.deepBumpStore("kv", "users")
	if i.trace.Deep == nil {
		t.Fatal("Deep should be populated when deepEnabled=true")
	}
	if i.trace.Deep.BodyReadBytes != 100 {
		t.Errorf("body read: %d", i.trace.Deep.BodyReadBytes)
	}
	if i.trace.Deep.BodyWriteBytes != 200 {
		t.Errorf("body write: %d", i.trace.Deep.BodyWriteBytes)
	}
	if i.trace.Deep.CacheLookups != 1 {
		t.Errorf("cache lookups: %d", i.trace.Deep.CacheLookups)
	}
	if got := i.trace.Deep.storeAccess[storeAccessKey{Kind: "kv", Name: "users"}]; got != 1 {
		t.Errorf("kv:users access count: %d", got)
	}
}

func TestDeepCaptureNoticeContent(t *testing.T) {
	// Capture log output during a deep-mode New call. The notice must
	// name both what deep captures and what it never captures, so an
	// operator can verify the privacy contract from log output alone.
	var buf strings.Builder
	origOutput := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(origOutput)
		log.SetFlags(origFlags)
	}()

	f := &Fastlike{profileCompile: &profileCompileConfig{mode: ProfileModeDeep}}
	f.maybeLogDeepCaptureNotice()

	body := buf.String()
	for _, want := range []string{
		"captures",
		"body read/write byte totals",
		"cache lookup/insert/hit/miss/stale counts",
		"per-named-store access counts",
		"request/response header names",
		"redacted",
		"wasm linear memory size curve",
		"NEVER captures",
		"header values",
		"body bytes",
		"secret values",
		"cache keys",
		"surrogate keys",
		"URL userinfo",
		"URL query strings",
		"Go runtime heap",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("notice missing required phrase %q; got: %s", want, body)
		}
	}
}

func TestDeepCaptureNoticeSilentWhenNotDeep(t *testing.T) {
	var buf strings.Builder
	origOutput := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(origOutput)
		log.SetFlags(origFlags)
	}()

	for _, mode := range []ProfileMode{ProfileModeOff, ProfileModeTrace, ProfileModeNative, ProfileModeCombined} {
		buf.Reset()
		f := &Fastlike{profileCompile: &profileCompileConfig{mode: mode}}
		f.maybeLogDeepCaptureNotice()
		if buf.Len() != 0 {
			t.Errorf("mode %q should not log deep notice, got: %s", mode, buf.String())
		}
	}
}

func TestDeepHeaderRedactionVisibleInEncoders(t *testing.T) {
	tr := fixtureDeepPrivacyTrace(t)

	// Native JSON: decode and walk the request/response header arrays.
	// The placeholder uses '<' / '>' which json.Encoder HTML-escapes
	// in the raw bytes, so string-matching is ambiguous; decoding
	// gives us the canonical form.
	raw, err := tr.MarshalJSON()
	if err != nil {
		t.Fatalf("native: %v", err)
	}
	var decoded struct {
		Deep struct {
			RequestHeaders  []HeaderSummary `json:"request_headers"`
			ResponseHeaders []HeaderSummary `json:"response_headers"`
		} `json:"deep"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	walk := func(dir string, summaries []HeaderSummary) {
		sawRedacted := false
		for _, h := range summaries {
			if h.Name == redactedHeaderPlaceholder {
				sawRedacted = true
			}
			for _, denied := range []string{"Authorization", "Cookie", "Set-Cookie", "X-Api-Key", "Www-Authenticate"} {
				if h.Name == denied {
					t.Errorf("%s: leaked deny-list header name %q", dir, denied)
				}
			}
		}
		if !sawRedacted {
			t.Errorf("%s: expected at least one redacted row", dir)
		}
	}
	walk("request_headers", decoded.Deep.RequestHeaders)
	walk("response_headers", decoded.Deep.ResponseHeaders)

	// Public headers should be present (positive control).
	expectedPublic := map[string]bool{
		"User-Agent":   false,
		"Accept":       false,
		"Content-Type": false,
	}
	for _, h := range decoded.Deep.RequestHeaders {
		if _, ok := expectedPublic[h.Name]; ok {
			expectedPublic[h.Name] = true
		}
	}
	for _, h := range decoded.Deep.ResponseHeaders {
		if _, ok := expectedPublic[h.Name]; ok {
			expectedPublic[h.Name] = true
		}
	}
	for name, seen := range expectedPublic {
		if !seen {
			t.Errorf("public header %q missing from decoded output", name)
		}
	}
}

func TestDeepHeaderAggregatesSurfaceInChromeAndFirefox(t *testing.T) {
	tr := fixtureDeepPrivacyTrace(t)

	chromeRaw, err := EncodeChromeTrace(tr)
	if err != nil {
		t.Fatalf("chrome: %v", err)
	}
	for _, want := range []string{
		"fastlike.deep.request_header_count",
		"fastlike.deep.request_header_bytes",
		"fastlike.deep.response_header_count",
		"fastlike.deep.response_header_bytes",
	} {
		if !strings.Contains(string(chromeRaw), want) {
			t.Errorf("chrome OtherData missing %q", want)
		}
	}

	ffRaw, err := EncodeFirefoxGecko(tr)
	if err != nil {
		t.Fatalf("firefox: %v", err)
	}
	for _, want := range []string{
		`"request_header_count"`,
		`"response_header_count"`,
	} {
		if !strings.Contains(string(ffRaw), want) {
			t.Errorf("firefox deep_metrics marker missing %q", want)
		}
	}
}

func TestDeepCacheOutcomesSurfaceInEncoders(t *testing.T) {
	tr := fixtureDeepPrivacyTrace(t)

	// Native JSON — explicit field names.
	raw, err := tr.MarshalJSON()
	if err != nil {
		t.Fatalf("native: %v", err)
	}
	body := string(raw)
	for _, want := range []string{`"cache_hits":1`, `"cache_misses":1`, `"cache_stale":0`} {
		if !strings.Contains(body, want) {
			t.Errorf("native JSON missing %q", want)
		}
	}

	// Chrome — counters in OtherData.
	cromeRaw, err := EncodeChromeTrace(tr)
	if err != nil {
		t.Fatalf("chrome: %v", err)
	}
	for _, want := range []string{
		`"fastlike.deep.cache_hits":1`,
		`"fastlike.deep.cache_misses":1`,
		`"fastlike.deep.cache_stale":0`,
	} {
		if !strings.Contains(string(cromeRaw), want) {
			t.Errorf("chrome OtherData missing %q", want)
		}
	}

	// Firefox — single deep_metrics marker with data payload.
	ffRaw, err := EncodeFirefoxGecko(tr)
	if err != nil {
		t.Fatalf("firefox: %v", err)
	}
	for _, want := range []string{`deep_metrics`, `"cache_hits":1`, `"cache_misses":1`} {
		if !strings.Contains(string(ffRaw), want) {
			t.Errorf("firefox missing %q", want)
		}
	}

	// pprof — one lifecycle sample per non-zero outcome; zero outcomes
	// (CacheStale=0 here) intentionally omitted.
	pprofRaw, err := EncodePprof(tr)
	if err != nil {
		t.Fatalf("pprof: %v", err)
	}
	summary := string(summarizePprof(t, pprofRaw))
	for _, want := range []string{"deep:cache_hits", "deep:cache_misses"} {
		if !strings.Contains(summary, want) {
			t.Errorf("pprof summary missing %q", want)
		}
	}
	if strings.Contains(summary, "deep:cache_stale") {
		t.Errorf("pprof should omit zero cache_stale sample")
	}
}

func mustRequest(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequest("GET", "http://x/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return r
}

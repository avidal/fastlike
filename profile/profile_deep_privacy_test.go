package profile

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// scaryStrings is the set of values the privacy fixture intentionally
// drops into every input that could plausibly leak. Encoders MUST NOT
// surface any of these in their output, regardless of mode.
//
// The "scary" key names (KV/Config/Secret store names) DO appear in the
// encoded output because the deep schema explicitly captures store names.
// The privacy contract is about *values*, not names.
var scaryStrings = []string{
	"ya29.PLAUSIBLE_OAUTH_SECRET",
	"Bearer 8a4f9c1234567890abcdef",
	"session=abcdef0123456789",
	"hunter2",
	"token=PROVISIONAL_SECRET",
	"raw_body_bytes_PROHIBITED",
	"cache_key_USER_42_AUTHTOKEN",
	"surrogate_USER_42_SESSION",
}

// fixtureDeepPrivacyTrace builds a trace whose deep metrics are populated and
// whose surrounding context contains intentionally scary inputs. The encoder
// tests below grep the output for the scary strings and fail if any appear.
func fixtureDeepPrivacyTrace(t *testing.T) *RequestTrace {
	t.Helper()
	tr := fixtureNormalTrace(t)
	tr.Deep = NewDeepMetrics()
	tr.Deep.BodyReadBytes = 12345
	tr.Deep.BodyWriteBytes = 67890
	tr.Deep.CacheLookups = 2
	tr.Deep.CacheInserts = 1
	tr.Deep.CacheHits = 1
	tr.Deep.CacheMisses = 1
	tr.Deep.CacheStale = 0
	// Store names that look secret-adjacent — but they're configuration
	// the operator named explicitly, so they ARE allowed to appear.
	tr.Deep.BumpStoreAccess("kv", "API_KEY_PROD")
	tr.Deep.BumpStoreAccess("secret", "github_token")
	tr.Deep.BumpStoreAccess("config", "feature_flags")

	req := http.Header{}
	req.Add("Authorization", "Bearer 8a4f9c1234567890abcdef")
	req.Add("authorization", "ya29.PLAUSIBLE_OAUTH_SECRET")
	req.Add("Cookie", "session=abcdef0123456789")
	req.Add("X-API-KEY", "hunter2")
	req.Add("User-Agent", "fastlike-test/1.0")
	req.Add("Accept", "application/json")
	tr.Deep.RequestHeaders = SummarizeHeaders(req)

	resp := http.Header{}
	resp.Add("Set-Cookie", "auth=session=abcdef0123456789; HttpOnly")
	resp.Add("set-cookie", "tracking=token=PROVISIONAL_SECRET")
	resp.Add("Content-Type", "application/json")
	resp.Add("WWW-Authenticate", "Bearer realm=acme")
	tr.Deep.ResponseHeaders = SummarizeHeaders(resp)

	tr.Deep.Finalize()
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
	summary := summarizePprof(t, raw)
	assertNoScaryStrings(t, "pprof summary", summary)
}

func TestDeepPrivacyStoreNamesArePresent(t *testing.T) {
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

func TestDeepHeaderRedactionVisibleInEncoders(t *testing.T) {
	tr := fixtureDeepPrivacyTrace(t)

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

	ffRaw, err := EncodeFirefoxGecko(tr)
	if err != nil {
		t.Fatalf("firefox: %v", err)
	}
	for _, want := range []string{`deep_metrics`, `"cache_hits":1`, `"cache_misses":1`} {
		if !strings.Contains(string(ffRaw), want) {
			t.Errorf("firefox missing %q", want)
		}
	}

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

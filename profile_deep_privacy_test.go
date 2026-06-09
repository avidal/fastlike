package fastlike

import (
	"log"
	"net/http"
	"strings"
	"testing"

	"fastlike.dev/profile"
)

// TestDeepGatedOffWhenModeNotDeep proves the gate at the source: a trace
// whose recorder has deep disabled allocates no DeepMetrics even if every
// instrumented hostcall fires. This is the contract "trace mode never
// collects deep data".
func TestDeepGatedOffWhenModeNotDeep(t *testing.T) {
	store := profile.NewProfileStore()
	i := &Instance{profile: &profile.Binding{Store: store, ModuleID: "m", DeepEnabled: false}}
	i.trace = store.NewRequestTrace("m", mustRequest(t))
	if i.profile.DeepEnabled {
		t.Fatal("test setup wrong: deepEnabled should be false")
	}
	if i.profile.DeepEnabled {
		i.trace.Deep = profile.NewDeepMetrics()
	}
	if i.trace.Deep != nil {
		t.Errorf("Deep should be nil when deepEnabled=false")
	}
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
		want  func(*profile.DeepMetrics) int
	}{
		{"hit", CacheState{Found: true, Usable: true}, func(d *profile.DeepMetrics) int { return d.CacheHits }},
		{"stale", CacheState{Found: true, Usable: true, Stale: true}, func(d *profile.DeepMetrics) int { return d.CacheStale }},
		{"stale-only", CacheState{Found: true, Stale: true}, func(d *profile.DeepMetrics) int { return d.CacheStale }},
		{"miss-empty", CacheState{}, func(d *profile.DeepMetrics) int { return d.CacheMisses }},
		{"miss-must-update", CacheState{MustInsertOrUpdate: true}, func(d *profile.DeepMetrics) int { return d.CacheMisses }},
	}
	for _, c := range cases {
		store := profile.NewProfileStore()
		i := &Instance{profile: &profile.Binding{Store: store, ModuleID: "m", DeepEnabled: true}}
		i.trace = store.NewRequestTrace("m", mustRequest(t))
		i.trace.Deep = profile.NewDeepMetrics()
		i.deepBumpCacheOutcome(c.state)
		if got := c.want(i.trace.Deep); got != 1 {
			t.Errorf("%s: expected matching counter to be 1, got %+v", c.name, i.trace.Deep)
		}
	}
}

func TestDeepCacheOutcomeAbsentWhenNotDeep(t *testing.T) {
	store := profile.NewProfileStore()
	i := &Instance{profile: &profile.Binding{Store: store, ModuleID: "m", DeepEnabled: false}}
	i.trace = store.NewRequestTrace("m", mustRequest(t))
	i.deepBumpCacheOutcome(CacheState{Found: true, Usable: true})
	if i.trace.Deep != nil {
		t.Errorf("Deep should remain nil with deepEnabled=false")
	}
}

func TestDeepCollectsWhenModeIsDeep(t *testing.T) {
	store := profile.NewProfileStore()
	i := &Instance{profile: &profile.Binding{Store: store, ModuleID: "m", DeepEnabled: true}}
	i.trace = store.NewRequestTrace("m", mustRequest(t))
	if i.profile.DeepEnabled {
		i.trace.Deep = profile.NewDeepMetrics()
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
	if got := i.trace.Deep.StoreAccessCount("kv", "users"); got != 1 {
		t.Errorf("kv:users access count: %d", got)
	}
}

func TestDeepCaptureNoticeContent(t *testing.T) {
	// Capture log output during a deep-mode New call. The notice must name
	// both what deep captures and what it never captures, so an operator can
	// verify the privacy contract from log output alone.
	var buf strings.Builder
	origOutput := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(origOutput)
		log.SetFlags(origFlags)
	}()

	f := &Fastlike{profileCompile: &profile.CompileConfig{Mode: profile.ProfileModeDeep}}
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

	for _, mode := range []profile.ProfileMode{profile.ProfileModeOff, profile.ProfileModeTrace, profile.ProfileModeNative, profile.ProfileModeCombined} {
		buf.Reset()
		f := &Fastlike{profileCompile: &profile.CompileConfig{Mode: mode}}
		f.maybeLogDeepCaptureNotice()
		if buf.Len() != 0 {
			t.Errorf("mode %q should not log deep notice, got: %s", mode, buf.String())
		}
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

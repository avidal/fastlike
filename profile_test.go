package fastlike

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProfileStoreRetention(t *testing.T) {
	s := NewProfileStore()
	s.retain = 3

	for i := 0; i < 10; i++ {
		r, _ := http.NewRequest("GET", "http://x/", nil)
		tr := s.newRequestTrace("modA", r)
		s.completeTrace(tr)
	}

	if got, want := len(s.completed), 3; got != want {
		t.Fatalf("retention not enforced: have %d, want %d", got, want)
	}
	// The most recent three should be 8, 9, 10 in some order; Recent() reverses.
	recent := s.Recent(0)
	if recent[0].ReqID != 10 || recent[1].ReqID != 9 || recent[2].ReqID != 8 {
		t.Fatalf("Recent() order wrong: %d, %d, %d", recent[0].ReqID, recent[1].ReqID, recent[2].ReqID)
	}
}

func TestProfileStoreInFlightHandoff(t *testing.T) {
	s := NewProfileStore()
	r, _ := http.NewRequest("POST", "http://x/y", nil)
	tr := s.newRequestTrace("modZ", r)

	if got := s.InFlight(); len(got) != 1 || got[0].ReqID != tr.ReqID {
		t.Fatalf("trace missing from in-flight set: %+v", got)
	}
	if s.Get(tr.ReqID) != nil {
		t.Fatalf("Get should return nil for in-flight trace, not %+v", s.Get(tr.ReqID))
	}

	s.completeTrace(tr)

	if len(s.InFlight()) != 0 {
		t.Fatalf("trace still in in-flight set after completion")
	}
	if got := s.Get(tr.ReqID); got == nil || got.ReqID != tr.ReqID {
		t.Fatalf("Get() failed after completion: %+v", got)
	}
}

func TestProfileStoreNilSafe(t *testing.T) {
	var s *ProfileStore
	// All these must not panic; the nil store represents "profiling disabled".
	if got := s.newRequestTrace("m", httptest.NewRequest("GET", "/", nil)); got != nil {
		t.Fatalf("expected nil trace from nil store, got %+v", got)
	}
	s.completeTrace(&RequestTrace{}) // must not panic
}

func TestModuleIDStability(t *testing.T) {
	a := moduleIDOf([]byte("hello"))
	b := moduleIDOf([]byte("hello"))
	c := moduleIDOf([]byte("hellp"))
	if a != b {
		t.Fatalf("expected stable id for same input, got %s vs %s", a, b)
	}
	if a == c {
		t.Fatalf("expected different ids for different inputs, both %s", a)
	}
	if len(a) != 16 {
		t.Fatalf("expected 16-hex-char id, got %d: %q", len(a), a)
	}
}

func TestProfileModeIncludesTrace(t *testing.T) {
	cases := map[ProfileMode]bool{
		ProfileModeOff:      false,
		ProfileModeTrace:    true,
		ProfileModeNative:   true,
		ProfileModeCombined: true,
		ProfileModeDeep:     true,
	}
	for mode, want := range cases {
		if got := mode.includesTrace(); got != want {
			t.Errorf("mode %q includesTrace=%v, want %v", mode, got, want)
		}
	}
}

func TestFastlikeOptionDefaultStore(t *testing.T) {
	f := &Fastlike{profileCompile: &profileCompileConfig{}}
	WithProfileMode(ProfileModeTrace)(f)

	if f.profileStore == nil {
		t.Fatal("WithProfileMode should lazily allocate a ProfileStore")
	}
	if f.profileCompile.mode != ProfileModeTrace {
		t.Errorf("mode not set: %q", f.profileCompile.mode)
	}
	if f.profileStore.Retain() != defaultProfileRetain {
		t.Errorf("retain default wrong: %d", f.profileStore.Retain())
	}
	if f.profileStore.AsyncGrace() != defaultProfileAsyncGrace {
		t.Errorf("async grace default wrong: %s", f.profileStore.AsyncGrace())
	}
	if f.profileStore.BackendCap() != defaultProfileBackendCap {
		t.Errorf("backend cap default wrong: %d", f.profileStore.BackendCap())
	}
}

func TestFastlikeOptionOverrides(t *testing.T) {
	f := &Fastlike{profileCompile: &profileCompileConfig{}}
	WithProfileRetain(7)(f)
	WithProfileAsyncGrace(500 * time.Millisecond)(f)
	WithProfileBackendCap(64)(f)
	WithProfileUI("127.0.0.1:0")(f)
	WithProfileAuth("secret")(f)
	WithProfileInsecureUI()(f)
	WithProfileDir("/tmp/x")(f)

	s := f.profileStore
	if s.Retain() != 7 {
		t.Errorf("retain override: %d", s.Retain())
	}
	if s.AsyncGrace() != 500*time.Millisecond {
		t.Errorf("async grace override: %s", s.AsyncGrace())
	}
	if s.BackendCap() != 64 {
		t.Errorf("backend cap override: %d", s.BackendCap())
	}
	if s.UIAddr() != "127.0.0.1:0" {
		t.Errorf("ui addr: %q", s.UIAddr())
	}
	if s.uiAuthToken != "secret" {
		t.Errorf("auth token: %q", s.uiAuthToken)
	}
	if !s.uiInsecure {
		t.Errorf("insecure ui not set")
	}
	if s.Dir() != "/tmp/x" {
		t.Errorf("dir: %q", s.Dir())
	}
}

func TestFastlikeOptionRetainClamps(t *testing.T) {
	f := &Fastlike{profileCompile: &profileCompileConfig{}}
	WithProfileRetain(-3)(f)
	if got := f.profileStore.Retain(); got != defaultProfileRetain {
		t.Errorf("negative retain not clamped, got %d", got)
	}
	WithProfileBackendCap(0)(f)
	if got := f.profileStore.BackendCap(); got != defaultProfileBackendCap {
		t.Errorf("zero backend cap not clamped, got %d", got)
	}
	WithProfileAsyncGrace(-time.Second)(f)
	if got := f.profileStore.AsyncGrace(); got != 0 {
		t.Errorf("negative async grace not clamped to zero, got %s", got)
	}
}

func TestBindingForGatesOnMode(t *testing.T) {
	// mode=off must yield a nil binding even when a store exists, so
	// `-profile off` truly disables collection. Setting any mode that
	// includes trace flips the binding on.
	f := &Fastlike{profileCompile: &profileCompileConfig{}}
	WithProfileStore(NewProfileStore())(f)
	WithProfileMode(ProfileModeOff)(f)
	if b := f.bindingFor([]byte("wasm")); b != nil {
		t.Fatalf("mode=off must return nil binding, got %+v", b)
	}
	WithProfileMode(ProfileModeTrace)(f)
	if b := f.bindingFor([]byte("wasm")); b == nil {
		t.Fatalf("mode=trace must return a non-nil binding")
	}
}

func TestWithProfileStoreOverrides(t *testing.T) {
	f := &Fastlike{profileCompile: &profileCompileConfig{}}
	custom := NewProfileStore()
	custom.retain = 99
	WithProfileStore(custom)(f)
	if f.profileStore != custom {
		t.Fatal("WithProfileStore did not install custom store")
	}
}

func TestWithInstanceOptionsAccumulates(t *testing.T) {
	f := &Fastlike{}
	WithInstanceOptions(WithComplianceRegion("us"))(f)
	WithInstanceOptions(WithComplianceRegion("eu"))(f)
	if got, want := len(f.instanceOpts), 2; got != want {
		t.Fatalf("instanceOpts length: got %d, want %d", got, want)
	}
}

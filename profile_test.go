package fastlike

import (
	"testing"
	"time"

	"fastlike.dev/profile"
)

func TestFastlikeOptionDefaultStore(t *testing.T) {
	f := &Fastlike{profileCompile: &profile.CompileConfig{}}
	WithProfileMode(profile.ProfileModeTrace)(f)

	if f.profileStore == nil {
		t.Fatal("WithProfileMode should lazily allocate a ProfileStore")
	}
	if f.profileCompile.Mode != profile.ProfileModeTrace {
		t.Errorf("mode not set: %q", f.profileCompile.Mode)
	}
	def := profile.NewProfileStore()
	if f.profileStore.Retain() != def.Retain() {
		t.Errorf("retain default wrong: %d", f.profileStore.Retain())
	}
	if f.profileStore.AsyncGrace() != def.AsyncGrace() {
		t.Errorf("async grace default wrong: %s", f.profileStore.AsyncGrace())
	}
	if f.profileStore.BackendCap() != def.BackendCap() {
		t.Errorf("backend cap default wrong: %d", f.profileStore.BackendCap())
	}
}

func TestFastlikeOptionOverrides(t *testing.T) {
	f := &Fastlike{profileCompile: &profile.CompileConfig{}}
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
	if s.UIAuthToken() != "secret" {
		t.Errorf("auth token: %q", s.UIAuthToken())
	}
	if !s.UIInsecure() {
		t.Errorf("insecure ui not set")
	}
	if s.Dir() != "/tmp/x" {
		t.Errorf("dir: %q", s.Dir())
	}
}

func TestFastlikeOptionRetainClamps(t *testing.T) {
	def := profile.NewProfileStore()
	f := &Fastlike{profileCompile: &profile.CompileConfig{}}
	WithProfileRetain(-3)(f)
	if got := f.profileStore.Retain(); got != def.Retain() {
		t.Errorf("negative retain not clamped, got %d", got)
	}
	WithProfileBackendCap(0)(f)
	if got := f.profileStore.BackendCap(); got != def.BackendCap() {
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
	f := &Fastlike{profileCompile: &profile.CompileConfig{}}
	WithProfileStore(profile.NewProfileStore())(f)
	WithProfileMode(profile.ProfileModeOff)(f)
	if b := f.bindingFor([]byte("wasm")); b != nil {
		t.Fatalf("mode=off must return nil binding, got %+v", b)
	}
	WithProfileMode(profile.ProfileModeTrace)(f)
	if b := f.bindingFor([]byte("wasm")); b == nil {
		t.Fatalf("mode=trace must return a non-nil binding")
	}
}

func TestWithProfileStoreOverrides(t *testing.T) {
	f := &Fastlike{profileCompile: &profile.CompileConfig{}}
	custom := profile.NewProfileStore()
	custom.SetRetain(99)
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

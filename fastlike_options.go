package fastlike

import (
	"time"

	"fastlike.dev/profile"
)

// FastlikeOption is a functional option applied to a Fastlike value at
// construction time. It is a distinct named type from Option (which targets
// an Instance) so that misclassifying a profile-tier knob as a per-instance
// option is a compile error rather than a silent no-op. See the API surface
// section of plans/guest-profiling.md for the full rationale.
//
// All profile-related constructors return FastlikeOption. Per-instance knobs
// keep returning Option. The two slices are kept apart inside Fastlike and
// applied at their respective tiers.
type FastlikeOption func(*Fastlike)

// WithInstanceOptions threads per-instance Options through to every Instance
// the Fastlike creates. It is the bridge between the two tiers: callers pass
// Option values to New(...) by wrapping them with WithInstanceOptions, so the
// compiler still rejects an accidental WithProfileMode(...) appearing in the
// inner slice.
func WithInstanceOptions(opts ...Option) FastlikeOption {
	return func(f *Fastlike) {
		f.instanceOpts = append(f.instanceOpts, opts...)
	}
}

// WithProfileStore plugs an embedder-supplied ProfileStore into the Fastlike.
// If unset and any other profile option is in play, the Fastlike creates a
// default store via NewProfileStore().
func WithProfileStore(store *profile.ProfileStore) FastlikeOption {
	return func(f *Fastlike) {
		f.profileStore = store
	}
}

// WithProfileMode selects the breadth of profiling instrumentation. The mode
// is compile-time configuration: engine-level wiring for native/combined runs
// inside compile() in a later step. Trace mode (the default when any profile
// option is set) needs no engine support.
func WithProfileMode(mode profile.ProfileMode) FastlikeOption {
	return func(f *Fastlike) {
		f.ensureProfileStore()
		f.profileCompile.Mode = mode
	}
}

// WithProfileUI sets the bind address for the viewer. Whether a listener is
// actually started and what auth is enforced is governed by the security
// policy in plans/guest-profiling.md, not by the address alone.
func WithProfileUI(addr string) FastlikeOption {
	return func(f *Fastlike) {
		f.ensureProfileStore()
		f.profileStore.SetUIAddr(addr)
	}
}

// WithProfileAuth sets the bearer token enforced on every UI endpoint when
// the UI listener is bound.
func WithProfileAuth(token string) FastlikeOption {
	return func(f *Fastlike) {
		f.ensureProfileStore()
		f.profileStore.SetUIAuthToken(token)
	}
}

// WithProfileInsecureUI is the escape hatch that allows a non-loopback UI
// bind without bearer auth. Intended only for environments with externalized
// auth (mTLS, authenticating proxy).
func WithProfileInsecureUI() FastlikeOption {
	return func(f *Fastlike) {
		f.ensureProfileStore()
		f.profileStore.SetUIInsecure(true)
	}
}

// WithProfileDir enables disk archival of completed traces under PATH.
func WithProfileDir(path string) FastlikeOption {
	return func(f *Fastlike) {
		f.ensureProfileStore()
		f.profileStore.SetDir(path)
	}
}

// WithProfileRetain sets the per-Fastlike LRU size for completed traces.
// Values <= 0 are clamped to the default.
func WithProfileRetain(n int) FastlikeOption {
	return func(f *Fastlike) {
		f.ensureProfileStore()
		f.profileStore.SetRetain(n)
	}
}

// WithProfileAsyncGrace sets the maximum time finalizeTrace will wait for
// in-flight backend goroutines before snapshotting. Zero disables the grace
// period entirely; negative values are clamped to zero.
func WithProfileAsyncGrace(d time.Duration) FastlikeOption {
	return func(f *Fastlike) {
		f.ensureProfileStore()
		f.profileStore.SetAsyncGrace(d)
	}
}

// WithProfileBackendCap sets the per-request cap on recorded backend calls.
// Values <= 0 are clamped to the default.
func WithProfileBackendCap(n int) FastlikeOption {
	return func(f *Fastlike) {
		f.ensureProfileStore()
		f.profileStore.SetBackendCap(n)
	}
}

// ensureProfileStore lazily allocates the default ProfileStore the first
// time a profile option mutates it. WithProfileStore overrides any prior
// store, including a default one.
func (f *Fastlike) ensureProfileStore() {
	if f.profileStore == nil {
		f.profileStore = profile.NewProfileStore()
	}
}

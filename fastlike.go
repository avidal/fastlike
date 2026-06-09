package fastlike

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"

	"fastlike.dev/profile"
)

// Fastlike is the entrypoint to the package, used to construct new instances ready to serve
// incoming HTTP requests. It maintains a pool of compiled and linked wasm modules to amortize
// startup costs across multiple requests. In the case of a spike of incoming requests, new
// instances will be constructed on-demand and thrown away when the request is finished to avoid an
// ever-increasing memory cost.
type Fastlike struct {
	mu         sync.RWMutex
	instances  chan *Instance
	instancefn func(opts ...Option) *Instance

	// wasmfile is the path to the wasm file, stored for hot reload
	wasmfile string
	// instanceOpts are the per-instance options applied to every Instance the
	// Fastlike creates. Populated via WithInstanceOptions(...).
	instanceOpts []Option

	// profileStore owns retention, in-flight state, and UI configuration for
	// the per-Fastlike profile. nil disables profiling for every Instance
	// this Fastlike produces.
	profileStore *profile.ProfileStore

	// profileCompile is the compile-time profile configuration consumed by
	// Instance.compile. Always non-nil; mode defaults to ProfileModeOff.
	profileCompile *profile.CompileConfig

	// moduleID is a short stable identifier for the currently-loaded wasm
	// bytes. Recomputed on Reload so pooled instances built against the old
	// module can be retired by mismatch.
	moduleID string

	// stopChan is used to signal cleanup of background goroutines
	stopChan chan struct{}
}

// New returns a new Fastlike ready to create new instances from. Pass
// FastlikeOption values to configure profiling, retention, the UI listener,
// and other per-Fastlike behavior. Per-instance options are threaded through
// using WithInstanceOptions(...).
func New(wasmfile string, opts ...FastlikeOption) *Fastlike {
	f := &Fastlike{
		wasmfile:       wasmfile,
		profileCompile: &profile.CompileConfig{Mode: profile.ProfileModeOff},
		stopChan:       make(chan struct{}),
	}

	for _, opt := range opts {
		opt(f)
	}

	// Read the wasm file from disk
	wasmbytes, err := os.ReadFile(wasmfile)
	check(err)
	f.moduleID = profile.ModuleIDOf(wasmbytes)

	f.maybeEmitWasmSymbolSidecar(wasmbytes)
	f.maybeLogDeepCaptureNotice()

	// Calculate pool size: cap at 16 to avoid excessive memory usage
	// while still benefiting from instance reuse
	size := runtime.NumCPU()
	if size > 16 {
		size = 16
	} else if size < 0 {
		size = 0
	}

	// Create a buffered channel as the instance pool
	f.instances = make(chan *Instance, size)

	// instancefn creates new instances on demand when the pool is empty.
	// It captures the current wasm bytes, instanceOpts, profile binding,
	// and compile config so pooled instances built before a Reload keep
	// their original module attribution.
	f.instancefn = makeInstanceFn(f, wasmbytes)

	return f
}

// maybeLogDeepCaptureNotice prints the deep-mode capture-and-redaction
// summary at startup when ProfileModeDeep is active. The notice is
// load-bearing for operator awareness: an embedder enabling deep should
// know exactly what fastlike will record, even if they never read the
// docs. Single source of truth so CLI and embedders see identical text.
func (f *Fastlike) maybeLogDeepCaptureNotice() {
	if f.profileCompile == nil || f.profileCompile.Mode != profile.ProfileModeDeep {
		return
	}
	log.Print("[fastlike] -profile=deep captures: body read/write byte totals; cache lookup/insert/hit/miss/stale counts; per-named-store access counts (kv/config/secret/dictionary); request/response header names + sizes (deny-listed names like Cookie/Authorization redacted to <redacted>); wasm linear memory size curve sampled at request start, finalize, and hostcall boundaries.")
	log.Print("[fastlike] -profile=deep NEVER captures: header values, body bytes, secret values, KV values, cache keys, surrogate keys, URL userinfo, URL query strings, Go runtime heap (the memory curve is wasm guest memory only).")
}

// maybeEmitWasmSymbolSidecar writes the wasm-symbols-{pid}.json manifest
// when native sampling is configured. Skips silently otherwise. Errors
// from emission are logged but do not abort: the sidecar is an aid for
// external samplers, never a precondition for in-process tracing. The
// log line uses f.log so verbosity controls suppress it in quiet mode.
func (f *Fastlike) maybeEmitWasmSymbolSidecar(wasmbytes []byte) {
	if f.profileCompile == nil || !profile.NativeSamplingRequested(f.profileCompile.Mode) {
		return
	}
	dir := ""
	if f.profileStore != nil {
		dir = f.profileStore.Dir()
	}
	path, err := profile.WriteWasmSymbolSidecar(wasmbytes, dir, f.moduleID, f.profileCompile.Mode)
	if err != nil {
		log.Printf("[fastlike] wasm symbol sidecar emission failed: %v", err)
		return
	}
	log.Printf("[fastlike] wrote wasm symbol manifest %s", path)
	if _, supported := profile.NativeProfilerStrategy(f.profileCompile.Mode); !supported {
		log.Printf("[fastlike] native sampling requested but no supported strategy on this platform; engine ran without SetProfiler")
	}
}

// makeInstanceFn returns the closure that constructs new Instances for f.
// Extracted so Reload can rebuild the closure against fresh wasm bytes
// without duplicating the binding logic.
func makeInstanceFn(f *Fastlike, wasmbytes []byte) func(opts ...Option) *Instance {
	binding := f.bindingFor(wasmbytes)
	baseOpts := f.instanceOpts
	compileCfg := f.profileCompile
	return func(opts ...Option) *Instance {
		allOpts := append(append([]Option{}, baseOpts...), opts...)
		i := newInstanceWithProfile(wasmbytes, compileCfg, binding, allOpts...)
		return i
	}
}

// bindingFor returns the Binding to attach to instances built from
// wasmbytes. Returns nil when profiling is disabled — either no store
// is configured, or the configured mode is ProfileModeOff. The mode gate
// is what makes -profile off truly turn collection off; merely passing
// any other profile option allocates a store via ensureProfileStore, but
// mode=off keeps that store empty.
func (f *Fastlike) bindingFor(wasmbytes []byte) *profile.Binding {
	if f.profileStore == nil {
		return nil
	}
	if f.profileCompile == nil || !f.profileCompile.Mode.IncludesTrace() {
		return nil
	}
	return &profile.Binding{
		Store:       f.profileStore,
		ModuleID:    profile.ModuleIDOf(wasmbytes),
		DeepEnabled: f.profileCompile.Mode == profile.ProfileModeDeep,
	}
}

// ProfileStore returns the Fastlike's profile store, or nil if profiling is
// disabled. Exported so embedders and the viewer can read trace state.
func (f *Fastlike) ProfileStore() *profile.ProfileStore {
	return f.profileStore
}

// ModuleID returns the short identifier of the currently-loaded wasm module.
func (f *Fastlike) ModuleID() string {
	return f.moduleID
}

// ProfileMode returns the configured compile-time profile mode. Defaults to
// ProfileModeOff. Useful for callers (CLI, embedder bootstrap) that want to
// branch on whether trace collection is enabled before exposing a UI or
// allocating downstream resources.
func (f *Fastlike) ProfileMode() profile.ProfileMode {
	if f.profileCompile == nil {
		return profile.ProfileModeOff
	}
	return f.profileCompile.Mode
}

// ServeHTTP implements http.Handler for a Fastlike module. It's a convenience function over
// `Instantiate()` followed by `.ServeHTTP` on the returned instance.
func (f *Fastlike) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Get an instance from the pool (or create a new one if pool is empty)
	i := f.Instantiate()

	// Serve the request
	i.ServeHTTP(w, r)

	// Try to return the instance to the pool for reuse
	// If the pool is full, the instance is discarded (handled by default case)
	f.mu.RLock()
	instances := f.instances
	f.mu.RUnlock()

	// Protect against sending to a channel that might have been replaced during reload
	func() {
		defer func() {
			// Recover from panic if channel was replaced during reload.
			_ = recover()
		}()
		select {
		case instances <- i:
			// Successfully returned to pool
		default:
			// Pool is full, instance will be garbage collected
		}
	}()
}

// Warmup pre-creates n instances and adds them to the pool.
// This can be used to pre-compile the wasm module and reduce cold-start latency.
// If n exceeds the pool size, it is clamped to the maximum pool capacity.
func (f *Fastlike) Warmup(n int) {
	f.mu.RLock()
	instances := f.instances
	instancefn := f.instancefn
	f.mu.RUnlock()

	if n > cap(instances) {
		fmt.Printf("Warmup count %d is greater than max pool size %d. Clamping to max.\n", n, cap(instances))
		n = cap(instances)
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case instances <- instancefn():
			default:
			}
		}()
	}
	wg.Wait()
}

// Reload gracefully reloads the wasm module and replaces the instance pool.
// It reads the wasm file from disk, drains the current pool, and creates a new pool
// with fresh instances. This is safe to call while requests are being served.
// Returns an error if the wasm file cannot be read.
func (f *Fastlike) Reload() error {
	// Read the new wasm file
	wasmbytes, err := os.ReadFile(f.wasmfile)
	if err != nil {
		return fmt.Errorf("failed to read wasm file during reload: %w", err)
	}

	// Acquire write lock to replace the pool
	f.mu.Lock()
	defer f.mu.Unlock()

	// Close and drain the old instance pool
	// Old instances will be garbage collected once drained
	oldInstances := f.instances
	close(oldInstances)
	for range oldInstances {
		// Discard each old instance
	}

	// Recalculate pool size (same logic as New())
	poolSize := runtime.NumCPU()
	if poolSize > 16 {
		poolSize = 16
	} else if poolSize < 0 {
		poolSize = 0
	}

	// Create a new instance pool with the fresh wasm bytes. Recompute the
	// module id so new instances tag their traces with the post-reload
	// module while any pooled-and-still-running instance keeps its old
	// binding (see "Hot reload" in plans/guest-profiling.md).
	f.moduleID = profile.ModuleIDOf(wasmbytes)
	f.maybeEmitWasmSymbolSidecar(wasmbytes)
	f.instances = make(chan *Instance, poolSize)
	f.instancefn = makeInstanceFn(f, wasmbytes)

	return nil
}

// EnableReloadOnSIGHUP sets up a signal handler that reloads the wasm module
// when a SIGHUP signal is received. This is useful for hot-reloading during development
// or when using tools like watchexec to monitor file changes.
// The goroutine runs in the background until Close() is called or the program exits.
func (f *Fastlike) EnableReloadOnSIGHUP() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP)

	go func() {
		defer signal.Stop(sigChan)
		for {
			select {
			case <-sigChan:
				log.Printf("[fastlike] Received SIGHUP, reloading wasm module from %s", f.wasmfile)
				if err := f.Reload(); err != nil {
					log.Printf("[fastlike] Reload failed: %v", err)
				} else {
					log.Printf("[fastlike] Reload completed successfully")
				}
			case <-f.stopChan:
				return
			}
		}
	}()
}

// Close stops any background goroutines (such as the SIGHUP handler) and releases resources.
// It is safe to call Close multiple times.
func (f *Fastlike) Close() {
	select {
	case <-f.stopChan:
		// Already closed
	default:
		close(f.stopChan)
	}
}

// Instantiate returns an Instance ready to serve requests. It first tries to reuse
// an instance from the pool. If the pool is empty, it creates a new instance on-demand.
//
// IMPORTANT: This must be called for each request, as the XQD ABI is designed around
// a single request/response pair per instance. Never reuse an instance for multiple
// concurrent requests.
func (f *Fastlike) Instantiate(opts ...Option) *Instance {
	f.mu.RLock()
	instances := f.instances
	instancefn := f.instancefn
	f.mu.RUnlock()

	select {
	case i := <-instances:
		// Reuse an instance from the pool and apply any additional options
		for _, opt := range opts {
			opt(i)
		}
		return i
	default:
		// Pool is empty, create a new instance on-demand
		return instancefn(opts...)
	}
}

// check panics if err is non-nil. Used for fatal initialization errors.
func check(err error) {
	if err != nil {
		panic(err)
	}
}

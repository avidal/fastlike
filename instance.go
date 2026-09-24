package fastlike

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fastlike.dev/profile"

	"github.com/bytecodealliance/wasmtime-go/v46"
)

func isCleanExit(err error) bool {
	if wtErr, ok := err.(*wasmtime.Error); ok {
		if status, ok := wtErr.ExitStatus(); ok && status == 0 {
			return true
		}
	}
	return false
}

// Instance is an implementation of the XQD ABI along with a wasmtime.Instance configured to use it.
// Each instance handles exactly one HTTP request/response pair, as the XQD ABI is designed for
// single-request semantics. After serving a request, instances are reset and can be reused.
//
// API Design:
// Instance is exported to support the Fastlike.Instantiate() method, which allows advanced users
// to apply per-request configuration options. Most users should use Fastlike.ServeHTTP() directly,
// which internally manages instance lifecycle. Use Instantiate() when you need:
// - Per-request backend configuration
// - Custom logging or dictionary setup for specific requests
// - Fine-grained control over instance pooling and reuse
//
// The Instance type itself has no exported fields or methods (besides http.Handler), as all
// configuration is done via functional options passed to NewInstance() or Instantiate().
type Instance struct {
	wasmctx *wasmContext

	// Per-request wasm state (reset after each request)
	wasm   *wasmtime.Instance // The instantiated wasm module
	store  *wasmtime.Store    // Per-request store with its own linear memory
	memory *Memory            // Wrapper for reading/writing guest memory

	requests        *RequestHandles
	responses       *ResponseHandles
	bodies          *BodyHandles
	pendingRequests *PendingRequestHandles
	requestPromises *RequestPromiseHandles

	// KV Store handles for async operations
	kvStores  *KVStoreHandles
	kvLookups *KVStoreLookupHandles
	kvInserts *KVStoreInsertHandles
	kvDeletes *KVStoreDeleteHandles
	kvLists   *KVStoreListHandles

	// Async item handles for generic async I/O operations
	asyncItems *AsyncItemHandles

	// Downstream request/response state
	ds_request  *http.Request       // The incoming HTTP request from the client
	ds_response http.ResponseWriter // Where we write the final HTTP response
	ds_context  context.Context     // Request context, used for cancellation and timeouts

	// ds_originalHeaders holds the header names the client sent, casing and order included.
	ds_originalHeaders []string

	// downstreamRequestHandle is the handle ID for the downstream request
	// Created by body_downstream_get, used by functions like original_header_names_get
	downstreamRequestHandle int32

	// Backend configuration for subrequests.
	// backendsMu guards the map: dynamic backends are registered by the
	// guest, removed by reset(), and looked up by abandoned async send
	// goroutines that can outlive the request.
	backendsMu     sync.RWMutex
	backends       map[string]*Backend            // Named backends registered by the user
	defaultBackend func(name string) http.Handler // Fallback when backend not found (default: 502)

	// Logging configuration
	loggers       []logger                    // Named log endpoints
	defaultLogger func(name string) io.Writer // Fallback logger (default: stdout with prefix)

	// Key-value stores for configuration and data
	dictionaries    []dictionary        // Legacy string key-value lookup
	configStores    []configStore       // Modern alternative to dictionaries
	kvStoreRegistry map[string]*KVStore // Object storage with async operations
	secretStores    []secretStore       // Secure credential storage

	// Secret store handles
	secretStoreHandles *SecretStoreHandles
	secretHandles      *SecretHandles

	// Access Control Lists and Rate Limiting
	acls         map[string]*Acl    // Named ACLs for IP-based filtering
	aclHandles   *AclHandles        // Handle tracking for ACL operations
	rateCounters []rateCounterEntry // Rate counters for ERL
	penaltyBoxes []penaltyBoxEntry  // Penalty boxes for ERL

	// Cache state
	cache               *Cache               // In-memory cache implementation
	cacheHandles        *CacheHandles        // Handle tracking for cache lookups
	cacheBusyHandles    *CacheBusyHandles    // Handle tracking for async cache operations
	cacheReplaceHandles *CacheReplaceHandles // Handle tracking for cache replace operations

	// Shield configuration
	shields map[string]*Shield // Named shields for shielding module

	// Request processing functions
	geolookup        func(net.IP) Geo            // Geographic lookup from IP address
	uaparser         UserAgentParser             // User agent parsing
	deviceDetection  DeviceLookupFunc            // Device detection from user agent string
	botDetection     BotDetectionFunc            // Bot detection from request
	cachedBotInfo    *BotInfo                    // Per-request memoized bot detection result
	vpnProxy         VpnProxyFunc                // VPN/proxy intelligence from request
	cachedVpnProxy   *VpnProxyInfo               // Per-request memoized VPN proxy result
	imageOptimizer   ImageOptimizerTransformFunc // Image transformation hook
	secureFn         func(*http.Request) bool    // Determines if request is "secure" (default: checks TLS)
	complianceRegion string                      // GDPR/data locality region (e.g., "none", "us-eu", "us")

	// fakeValidFastlyKeys is the set of Fastly-Key header values that
	// fastly_key_is_valid treats as valid. Empty/nil means no key is valid,
	// preserving the historical always-false behavior.
	fakeValidFastlyKeys map[string]struct{}

	// Logging
	log    *log.Logger // General fastlike logging
	abilog *log.Logger // ABI call logging (verbose mode only)

	// CPU time tracking for compute runtime introspection
	// Note: This tracks active CPU time in microseconds, NOT wall clock time
	activeCpuTimeUs    atomic.Uint64 // Accumulated CPU time excluding I/O waits
	executionStartTime time.Time     // When execution started/resumed (zero when paused)

	// profile is the per-instance binding to the parent Fastlike's profile
	// store. nil disables profiling for this instance. Captured at instance
	// construction time so a Reload retiring the parent's binding does not
	// silently re-attribute an in-flight request's trace.
	profile *profile.Binding

	// trace is the currently-active RequestTrace, set by beginTrace and
	// nilled by finalizeTrace. nil when profile == nil or between requests.
	trace *profile.RequestTrace

	// traceWriter is the wrapper around ds_response that records status and
	// response bytes. Same lifetime as trace.
	traceWriter profile.ResponseObserver
}

// NewInstance returns an http.Handler that can handle a single request.
// Profiling is disabled on this path; embedders who want profiling should
// construct via Fastlike.New / Fastlike.Instantiate, which wires the
// per-Fastlike profile store through to each Instance.
func NewInstance(wasmbytes []byte, opts ...Option) *Instance {
	return newInstanceWithProfile(wasmbytes, nil, nil, opts...)
}

// newInstanceWithProfile is the internal constructor used by both NewInstance
// (no profiling) and Fastlike's instancefn (profiling bound). compileCfg may
// be nil; binding may be nil to disable profiling for this instance.
func newInstanceWithProfile(wasmbytes []byte, compileCfg *profile.CompileConfig, binding *profile.Binding, opts ...Option) *Instance {
	i := new(Instance)
	i.profile = binding
	i.compile(wasmbytes, compileCfg)

	i.requests = &RequestHandles{}
	i.bodies = &BodyHandles{}
	i.responses = &ResponseHandles{}
	i.pendingRequests = &PendingRequestHandles{}
	i.requestPromises = &RequestPromiseHandles{}
	i.kvStores = &KVStoreHandles{}
	i.kvLookups = &KVStoreLookupHandles{}
	i.kvInserts = &KVStoreInsertHandles{}
	i.kvDeletes = &KVStoreDeleteHandles{}
	i.kvLists = &KVStoreListHandles{}
	i.secretStoreHandles = &SecretStoreHandles{}
	i.secretHandles = &SecretHandles{}
	i.cache = NewCache()
	i.cacheHandles = &CacheHandles{}
	i.cacheBusyHandles = &CacheBusyHandles{}
	i.cacheReplaceHandles = &CacheReplaceHandles{}
	i.aclHandles = &AclHandles{}
	i.asyncItems = &AsyncItemHandles{}

	i.log = log.New(io.Discard, "[fastlike] ", log.Lshortfile)
	i.abilog = log.New(io.Discard, "[fastlike abi] ", log.Lshortfile)

	i.backends = map[string]*Backend{}
	i.loggers = []logger{}
	i.dictionaries = []dictionary{}
	i.configStores = []configStore{}
	i.kvStoreRegistry = map[string]*KVStore{}
	i.secretStores = []secretStore{}
	i.acls = map[string]*Acl{}
	i.shields = map[string]*Shield{}
	i.rateCounters = []rateCounterEntry{}
	i.penaltyBoxes = []penaltyBoxEntry{}

	// By default, any subrequests will return a 502
	i.defaultBackend = defaultBackend

	// By default, logs are written to stdout, prefixed with the name of the logger
	i.defaultLogger = defaultLogger

	// By default, all geo requests return the same data
	i.geolookup = defaultGeoLookup

	// By default, user agent parsing returns an empty useragent
	i.uaparser = func(_ string) UserAgent {
		return UserAgent{}
	}

	// By default, device detection returns no data
	i.deviceDetection = defaultDeviceDetection

	// By default, image optimizer returns an error
	i.imageOptimizer = defaultImageOptimizer

	// By default, requests are "secure" if they have TLS info
	i.secureFn = func(r *http.Request) bool {
		return r.TLS != nil
	}

	// By default, compliance region is "none"
	i.complianceRegion = "none"

	for _, o := range opts {
		o(i)
	}

	return i
}

// reset cleans up an instance after serving a request, preparing it for reuse.
// It closes all open handles, releases resources, and resets state to initial values.
func (i *Instance) reset() {
	// Close all HTTP request bodies
	for _, r := range i.requests.handles {
		if r == nil {
			continue
		}
		if r.Body != nil {
			_ = r.Body.Close()
		}
	}

	// Close all HTTP response bodies
	for _, w := range i.responses.handles {
		if w == nil {
			continue
		}
		if w.Body != nil {
			_ = w.Body.Close()
		}
	}

	// Close all body handles and release buffers
	for _, b := range i.bodies.handles {
		if b == nil {
			continue
		}
		if _, unfinished := b.closer.(bodyAbandoner); b.IsStreaming() || unfinished {
			// A writer that never finished must not have its partial body
			// published by teardown.
			_ = b.Abandon()
		} else if b.closer != nil {
			_ = b.closer.Close()
		}
		if b.buf != nil {
			b.buf = nil
		}
	}

	// Wake local next-request timers before dropping their handles so the
	// timeout goroutines do not outlive the request that created them.
	for _, promise := range i.requestPromises.handles {
		if promise != nil {
			promise.Complete(nil, context.Canceled)
		}
	}

	// Reset all handle trackers to empty state
	// The underlying memory is reused to avoid allocations
	*i.requests = RequestHandles{}
	*i.responses = ResponseHandles{}
	*i.bodies = BodyHandles{}
	*i.pendingRequests = PendingRequestHandles{}
	*i.requestPromises = RequestPromiseHandles{}
	*i.kvStores = KVStoreHandles{}
	*i.kvLookups = KVStoreLookupHandles{}
	*i.kvInserts = KVStoreInsertHandles{}
	*i.kvDeletes = KVStoreDeleteHandles{}
	*i.kvLists = KVStoreListHandles{}
	*i.secretStoreHandles = SecretStoreHandles{}
	*i.secretHandles = SecretHandles{}
	*i.cacheHandles = CacheHandles{}
	*i.cacheBusyHandles = CacheBusyHandles{}
	// Pending replaces and transactions must not outlive the request that started them.
	if i.cache != nil {
		i.cache.AbandonTransactions(i)
	}
	for _, replace := range i.cacheReplaceHandles.handles {
		if replace != nil {
			i.cache.ReplaceAbandon(replace.Replace)
		}
	}
	*i.cacheReplaceHandles = CacheReplaceHandles{}
	*i.aclHandles = AclHandles{}
	*i.asyncItems = AsyncItemHandles{}

	// Dynamic backends are request-scoped: a pooled instance must not carry
	// them into the next request, where re-registration under the same name
	// has to succeed like it does on a fresh production instance.
	// Idle connections are closed after releasing the lock: closing walks the
	// pool and issues a syscall per connection, and the map is read by
	// abandoned async send goroutines that must not block on it.
	var transports []*http.Transport
	i.backendsMu.Lock()
	for name, b := range i.backends {
		if b.IsDynamic {
			if b.Transport != nil {
				transports = append(transports, b.Transport)
			}
			delete(i.backends, name)
		}
	}
	i.backendsMu.Unlock()
	for _, t := range transports {
		t.CloseIdleConnections()
	}

	// Clear downstream request/response state
	i.ds_response = nil
	i.ds_request = nil
	i.ds_context = nil
	i.ds_originalHeaders = nil
	i.downstreamRequestHandle = 0
	i.cachedBotInfo = nil
	i.cachedVpnProxy = nil

	// Clear wasm state (will be re-initialized on next request)
	i.wasm = nil
	i.store = nil
	i.memory = nil

	// Reset CPU time tracking to zero
	i.activeCpuTimeUs.Store(0)
	i.executionStartTime = time.Time{}
}

// setup instantiates the guest for a new request and returns its entry point.
// Failures are returned so that the request gets a 500, like in production.
func (i *Instance) setup() (*wasmtime.Func, error) {
	// Ensure critical fields are initialized
	if i.wasmctx == nil || i.wasmctx.engine == nil || i.wasmctx.module == nil || i.wasmctx.linker == nil {
		panic("wasmctx not properly initialized")
	}

	// Create a fresh store for this request with the Instance attached as data
	// Host functions retrieve this Instance via caller.Data() to access per-request state
	i.store = wasmtime.NewStoreWithData(i.wasmctx.engine, i)
	i.store.Limiter(maxWasmMemoryBytes, maxWasmTableElements, 1, maxWasmTables, maxWasmMemories)

	// Configure WASI (WebAssembly System Interface) for this store
	wasicfg := wasmtime.NewWasiConfig()
	wasicfg.InheritStdout()               // Allow guest to write to stdout
	wasicfg.InheritStderr()               // Allow guest to write to stderr
	wasicfg.SetArgv([]string{"fastlike"}) // Set argv[0] to "fastlike"
	// Set Fastly environment variables for local development/testing
	wasicfg.SetEnv(
		[]string{"FASTLY_TRACE_ID", "FASTLY_SERVICE_VERSION", "FASTLY_HOSTNAME"},
		[]string{"00000000-0000-0000-0000-000000000000", "1", "localhost"},
	)
	i.store.SetWasi(wasicfg)

	// Lets a cancelled request interrupt the guest.
	i.store.SetEpochDeadline(1)

	// Initialize memory early with a placeholder so functions don't crash
	// This will be replaced with the real memory after instantiation
	i.memory = &Memory{nil}

	var err error
	i.wasm, err = i.wasmctx.linker.Instantiate(i.store, i.wasmctx.module)
	if err != nil {
		return nil, err
	}

	var mem *wasmtime.Memory
	if export := i.wasm.GetExport(i.store, "memory"); export != nil {
		mem = export.Memory()
	}
	if mem == nil {
		return nil, errors.New("the module does not export a memory named \"memory\"")
	}
	i.memory = &Memory{&wasmMemory{store: i.store, mem: mem}}

	entry := i.wasm.GetFunc(i.store, "_start")
	if entry == nil {
		return nil, errors.New("the module does not export a function named \"_start\"")
	}
	return entry, nil
}

// ServeHTTP serves the supplied request and response pair. This is not safe to call twice.
func (i *Instance) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Clean up even when setup fails.
	defer i.reset()

	// Claim the captured header names before any path can return early.
	i.ds_originalHeaders = claimOriginalHeaderNames(r)

	entry, err := i.setup()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Error instantiating wasm program.\n"))
		_, _ = w.Write([]byte(err.Error()))
		return
	}

	// beginTrace returns w wrapped in a traceResponseWriter when profiling
	// is enabled, or w unchanged when it is off. Shadowing w forces every
	// downstream write (loop-fail body, trap error body, ds_response handed
	// to the guest) through the wrapper.
	w = i.beginTrace(w, r)

	// Runs before reset, which clears the request state the trace reads.
	defer i.finalizeTrace()

	// Check for request loops using the cdn-loop header
	// We add "fastlike" to this header on each subrequest
	loops, ok := r.Header[http.CanonicalHeaderKey("cdn-loop")]
	if !ok {
		loops = []string{""}
	}

	// Enable verbose ABI logging if requested via header
	_, yeslog := r.Header[http.CanonicalHeaderKey("fastlike-verbose")]
	if yeslog {
		i.abilog.SetOutput(os.Stdout)
	}

	// Detect infinite request loops and fail fast
	if strings.Contains(strings.Join(loops, "\x00"), "fastlike") {
		i.markOutcome(profile.TraceOutcomeLoopFail)
		w.WriteHeader(http.StatusLoopDetected)
		_, _ = w.Write([]byte("Loop detected! This request has already come through your fastly program.\n"))
		_, _ = w.Write([]byte("You probably have a non-exhaustive backend handler?"))
		return
	}

	i.ds_request = r
	i.ds_response = w
	i.ds_context = r.Context()

	// Interrupt the guest when the request is cancelled.
	interrupted := make(chan struct{})
	stopInterrupt := context.AfterFunc(r.Context(), func() {
		i.wasmctx.engine.IncrementEpoch()
		close(interrupted)
	})

	// The guest program is responsible for:
	// 1. Getting a handle to the downstream request (via body_downstream_get)
	// 2. Processing the request (making subrequests, manipulating headers, etc.)
	// 3. Sending a response downstream (via resp_send_downstream)
	i.startExecution()
	_, err = entry.Call(i.store)
	i.stopExecution()

	// A late interrupt must not reach the next request served by this instance.
	if !stopInterrupt() {
		<-interrupted
	}

	// Handle wasm execution errors.
	// A clean exit (exit code 0) is normal for WASI programs — wasmtime
	// reports it as an error but it's not one. Only write the error
	// response for actual failures.
	if err != nil && !isCleanExit(err) {
		// A trap triggered by epoch interrupt during cancellation should be
		// classified as a cancellation, not as a guest-side trap. Genuine
		// guest traps reach finalize with ds_context still healthy.
		if i.ds_context != nil && i.ds_context.Err() != nil {
			i.markOutcome(profile.TraceOutcomeCtxCanceled)
		} else {
			i.markOutcome(profile.TraceOutcomeTrap)
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Error running wasm program.\n"))
		_, _ = w.Write([]byte("Below is a useless blob of wasm backtrace. There may be more in your server logs.\n"))
		_, _ = w.Write([]byte(err.Error()))
	}
}

// startExecution begins tracking CPU time for the guest execution.
// This should be called before entering guest code (e.g., before calling _start).
func (i *Instance) startExecution() {
	i.executionStartTime = time.Now()
}

// pauseExecution pauses CPU time tracking and accumulates the elapsed time.
// This should be called before blocking I/O operations (e.g., HTTP requests to backends).
// The caller MUST call resumeExecution() after the blocking operation completes.
func (i *Instance) pauseExecution() {
	// If not currently executing (already paused), nothing to do
	if i.executionStartTime.IsZero() {
		return
	}

	// Calculate elapsed CPU time since execution started/resumed
	elapsed := time.Since(i.executionStartTime)
	microseconds := elapsed.Microseconds()

	// Add to accumulated CPU time
	i.activeCpuTimeUs.Add(uint64(microseconds))

	// Mark as paused by zeroing the start time
	i.executionStartTime = time.Time{}
}

// resumeExecution resumes CPU time tracking after a blocking operation.
// This should be called after blocking operations complete (e.g., after HTTP response received).
func (i *Instance) resumeExecution() {
	// Record the new start time for execution
	i.executionStartTime = time.Now()
}

// stopExecution stops CPU time tracking and accumulates the final elapsed time.
// This should be called after guest code completes (e.g., after _start returns).
func (i *Instance) stopExecution() {
	i.pauseExecution()
}

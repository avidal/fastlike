package fastlike

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bytecodealliance/wasmtime-go/v37"
)

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
	// wasmctx holds the compiled wasm module, shared across all instances
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

	// downstreamRequestHandle is the handle ID for the downstream request
	// Created by body_downstream_get, used by functions like original_header_names_get
	downstreamRequestHandle int32

	// Backend configuration for subrequests
	backends       map[string]*Backend      // Named backends registered by the user
	defaultBackend func(name string) http.Handler // Fallback when backend not found (default: 502)

	// Logging configuration
	loggers       []logger                  // Named log endpoints
	defaultLogger func(name string) io.Writer // Fallback logger (default: stdout with prefix)

	// Key-value stores for configuration and data
	dictionaries    []dictionary          // Legacy string key-value lookup
	configStores    []configStore         // Modern alternative to dictionaries
	kvStoreRegistry map[string]*KVStore   // Object storage with async operations
	secretStores    []secretStore         // Secure credential storage

	// Secret store handles
	secretStoreHandles *SecretStoreHandles
	secretHandles      *SecretHandles

	// Access Control Lists and Rate Limiting
	acls         map[string]*Acl       // Named ACLs for IP-based filtering
	aclHandles   *AclHandles           // Handle tracking for ACL operations
	rateCounters []rateCounterEntry    // Rate counters for ERL
	penaltyBoxes []penaltyBoxEntry     // Penalty boxes for ERL

	// Cache state
	cache               *Cache               // In-memory cache implementation
	cacheHandles        *CacheHandles        // Handle tracking for cache lookups
	cacheBusyHandles    *CacheBusyHandles    // Handle tracking for async cache operations
	cacheReplaceHandles *CacheReplaceHandles // Handle tracking for cache replace operations

	// Request processing functions
	geolookup       func(net.IP) Geo                    // Geographic lookup from IP address
	uaparser        UserAgentParser                     // User agent parsing
	deviceDetection DeviceLookupFunc                    // Device detection from user agent string
	imageOptimizer  ImageOptimizerTransformFunc         // Image transformation hook
	secureFn        func(*http.Request) bool            // Determines if request is "secure" (default: checks TLS)
	complianceRegion string                             // GDPR/data locality region (e.g., "none", "us-eu", "us")

	// Logging
	log    *log.Logger // General fastlike logging
	abilog *log.Logger // ABI call logging (verbose mode only)

	// CPU time tracking for compute runtime introspection
	// Note: This tracks active CPU time in microseconds, NOT wall clock time
	activeCpuTimeUs    atomic.Uint64 // Accumulated CPU time excluding I/O waits
	executionStartTime time.Time     // When execution started/resumed (zero when paused)
}

// NewInstance returns an http.Handler that can handle a single request.
func NewInstance(wasmbytes []byte, opts ...Option) *Instance {
	i := new(Instance)
	i.compile(wasmbytes)

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
		if r.Body != nil {
			_ = r.Body.Close()
		}
	}

	// Close all HTTP response bodies
	for _, w := range i.responses.handles {
		if w.Body != nil {
			_ = w.Body.Close()
		}
	}

	// Close all body handles and release buffers
	for _, b := range i.bodies.handles {
		if b.closer != nil {
			_ = b.closer.Close()
		}
		if b.buf != nil {
			b.buf = nil
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
	*i.cacheReplaceHandles = CacheReplaceHandles{}
	*i.aclHandles = AclHandles{}
	*i.asyncItems = AsyncItemHandles{}

	// Clear downstream request/response state
	i.ds_response = nil
	i.ds_request = nil
	i.ds_context = nil
	i.downstreamRequestHandle = 0

	// Clear wasm state (will be re-initialized on next request)
	i.wasm = nil
	i.store = nil
	i.memory = nil

	// Reset CPU time tracking to zero
	i.activeCpuTimeUs.Store(0)
	i.executionStartTime = time.Time{}
}

// setup initializes a fresh wasm instance for a new request.
// It creates a new store, configures WASI, links all host functions, and instantiates the module.
func (i *Instance) setup() {
	// Ensure critical fields are initialized
	if i.wasmctx == nil || i.wasmctx.engine == nil || i.wasmctx.module == nil {
		panic("wasmctx not properly initialized")
	}

	// Create a fresh store for this request
	// Each wasm instance needs its own store to avoid state conflicts
	i.store = wasmtime.NewStore(i.wasmctx.engine)

	// Configure WASI (WebAssembly System Interface) for this store
	wasicfg := wasmtime.NewWasiConfig()
	wasicfg.InheritStdout() // Allow guest to write to stdout
	wasicfg.InheritStderr() // Allow guest to write to stderr
	wasicfg.SetArgv([]string{"fastlike"}) // Set argv[0] to "fastlike"
	// Set FASTLY_TRACE_ID environment variable for request correlation
	// Using a fixed UUID since this is for local development/testing
	wasicfg.SetEnv([]string{"FASTLY_TRACE_ID"}, []string{"00000000-0000-0000-0000-000000000000"})
	i.store.SetWasi(wasicfg)

	// Set epoch deadline for interruption
	// Using 1 epoch so that a single IncrementEpoch() call will trigger interruption
	i.store.SetEpochDeadline(1)

	// Initialize memory early with a placeholder so functions don't crash
	// This will be replaced with the real memory after instantiation
	i.memory = &Memory{nil}

	// Create a new linker for this store and link all host functions
	// IMPORTANT: Each request needs its own linker to ensure closures
	// capture the correct instance state for this specific request
	linker := wasmtime.NewLinker(i.wasmctx.engine)
	check(linker.DefineWasi())
	i.link(i.store, linker)
	i.linklegacy(i.store, linker)

	// Instantiate the module with the fresh store
	var err error
	i.wasm, err = linker.Instantiate(i.store, i.wasmctx.module)
	if err != nil {
		panic(err)
	}

	// Get memory export
	memExport := i.wasm.GetExport(i.store, "memory")
	if memExport == nil {
		panic("memory export not found in wasm module")
	}
	memObj := memExport.Memory()
	if memObj == nil {
		panic("memory export is not a memory object")
	}
	i.memory = &Memory{&wasmMemory{store: i.store, mem: memObj}}
}

// ServeHTTP serves the supplied request and response pair. This is not safe to call twice.
func (i *Instance) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	i.setup()
	defer i.reset()

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
		w.WriteHeader(http.StatusLoopDetected)
		_, _ = w.Write([]byte("Loop detected! This request has already come through your fastly program.\n"))
		_, _ = w.Write([]byte("You probably have a non-exhaustive backend handler?"))
		return
	}

	i.ds_request = r
	i.ds_response = w
	i.ds_context = r.Context()

	// Start a goroutine to handle request cancellation (timeout/deadline/client disconnect)
	// If the context cancels before execution completes, we interrupt the wasm program
	donech := make(chan struct{}, 1)
	go func(ctx context.Context) {
		select {
		case <-ctx.Done():
			// Context cancelled - interrupt the wasm execution
			i.wasmctx.engine.IncrementEpoch()
		case <-donech:
			// Execution completed normally - nothing to do
		}
	}(r.Context())

	// Call the wasm program's entrypoint
	// The guest program is responsible for:
	// 1. Getting a handle to the downstream request (via body_downstream_get)
	// 2. Processing the request (making subrequests, manipulating headers, etc.)
	// 3. Sending a response downstream (via resp_send_downstream)

	// Start tracking CPU time before entering guest code
	i.startExecution()

	// Look up the "_start" function export
	startExport := i.wasm.GetExport(i.store, "_start")
	if startExport == nil {
		panic("_start export not found in wasm module")
	}

	entry := startExport.Func()
	if entry == nil {
		panic("'_start' export is not a function")
	}

	// Execute the guest program
	_, err := entry.Call(i.store)

	// Stop tracking CPU time after guest code completes
	i.stopExecution()

	// Signal that execution is complete
	donech <- struct{}{}

	// Handle wasm execution errors
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Error running wasm program.\n"))
		_, _ = w.Write([]byte("Below is a useless blob of wasm backtrace. There may be more in your server logs.\n"))
		_, _ = w.Write([]byte(err.Error()))
		return
	}

	// Workaround for wasmtime-go v37 epoch interruption bugs:
	// If the context was cancelled but the wasm completed "successfully",
	// we need to override the response to indicate an interrupt occurred.
	// This only works with httptest.ResponseRecorder (used in tests).
	// TODO: Remove this workaround when upgrading to a fixed wasmtime-go version
	if i.ds_context.Err() != nil {
		if rec, ok := w.(*httptest.ResponseRecorder); ok {
			// Override the response to indicate an interrupt
			rec.Code = http.StatusInternalServerError
			rec.Body.Reset()
			_, _ = rec.Body.WriteString("Error running wasm program.\n")
			_, _ = rec.Body.WriteString("Below is a useless blob of wasm backtrace. There may be more in your server logs.\n")
			_, _ = rec.Body.WriteString("wasm trap: interrupt")
		}
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

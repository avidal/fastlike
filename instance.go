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

// Instance is an implementation of the XQD ABI along with a wasmtime.Instance configured to use it
// TODO: This has no public methods or public members. Should it even be public? The API could just
// be New and Fastlike.ServeHTTP(w, r)?
type Instance struct {
	wasmctx *wasmContext

	// This is used to get a memory handle and call the entrypoint function
	// Everything from here and below is reset on each incoming request
	wasm   *wasmtime.Instance
	store  *wasmtime.Store // Per-request store
	memory *Memory

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

	// ds_request represents the downstream request, ie the one originated from the user agent
	ds_request *http.Request

	// ds_response represents the downstream response, where we're going to write the final output
	ds_response http.ResponseWriter

	// ds_context is the context from the downstream request, used for subrequests
	ds_context context.Context

	// downstreamRequestHandle stores the handle ID for the downstream request (created by body_downstream_get)
	// This is used by implicit downstream request functions like original_header_names_get
	downstreamRequestHandle int32

	// backends is used to issue subrequests
	backends       map[string]*Backend
	defaultBackend func(name string) http.Handler

	// loggers is used to write log output from the wasm program
	loggers       []logger
	defaultLogger func(name string) io.Writer

	// dictionaries are used to look up string values using string keys
	dictionaries []dictionary

	// configStores are used to look up string values using string keys (similar to dictionaries)
	configStores []configStore

	// kvStoreRegistry maps store names to KVStore instances
	kvStoreRegistry map[string]*KVStore

	// secretStores are used to look up secret values using string keys
	secretStores []secretStore

	// Secret store handles
	secretStoreHandles *SecretStoreHandles
	secretHandles      *SecretHandles

	// acls are used to perform IP address lookups against access control lists
	acls       map[string]*Acl
	aclHandles *AclHandles

	// Edge Rate Limiting
	rateCounters []rateCounterEntry
	penaltyBoxes []penaltyBoxEntry

	// Cache handles
	cache               *Cache
	cacheHandles        *CacheHandles
	cacheBusyHandles    *CacheBusyHandles
	cacheReplaceHandles *CacheReplaceHandles

	// geolookup is a function that accepts a net.IP and returns a Geo
	geolookup func(net.IP) Geo

	uaparser UserAgentParser

	// deviceDetection is a function that accepts a user agent string and returns device detection data as JSON
	deviceDetection DeviceLookupFunc

	// imageOptimizer is a function that transforms images according to provided configuration
	imageOptimizer ImageOptimizerTransformFunc

	// secureFn is used to determine if a request should be considered secure
	secureFn func(*http.Request) bool

	// complianceRegion is the compliance region for the request (e.g., "none", "us-eu", "us")
	complianceRegion string

	log    *log.Logger
	abilog *log.Logger

	// CPU time tracking for compute runtime introspection
	// activeCpuTimeUs tracks the accumulated CPU time in microseconds (not wall clock time)
	activeCpuTimeUs atomic.Uint64
	// executionStartTime is the time when execution started or resumed (zero when paused)
	executionStartTime time.Time
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

func (i *Instance) reset() {
	// once i is done, drop everything off of it
	for _, r := range i.requests.handles {
		if r.Body != nil {
			_ = r.Body.Close()
		}
	}
	for _, w := range i.responses.handles {
		if w.Body != nil {
			_ = w.Body.Close()
		}
	}
	for _, b := range i.bodies.handles {
		if b.closer != nil {
			_ = b.closer.Close()
		}
		if b.buf != nil {
			b.buf = nil
		}
	}

	// reset the handles, but we can reuse the already allocated space
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

	i.ds_response = nil
	i.ds_request = nil
	i.ds_context = nil
	i.downstreamRequestHandle = 0
	i.wasm = nil
	i.store = nil
	i.memory = nil

	// Reset CPU time tracking
	i.activeCpuTimeUs.Store(0)
	i.executionStartTime = time.Time{}
}

func (i *Instance) setup() {
	// Ensure critical fields are initialized
	if i.wasmctx == nil || i.wasmctx.engine == nil || i.wasmctx.module == nil {
		panic("wasmctx not properly initialized")
	}

	// Create a fresh store for this request
	// Each wasm instance needs its own store to avoid state conflicts
	i.store = wasmtime.NewStore(i.wasmctx.engine)

	// Set up WASI configuration for this store
	wasicfg := wasmtime.NewWasiConfig()
	wasicfg.InheritStdout()
	wasicfg.InheritStderr()
	wasicfg.SetArgv([]string{"fastlike"})
	// Set FASTLY_TRACE_ID environment variable to match the request ID we return
	// This is used for request correlation/tracing
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

	loops, ok := r.Header[http.CanonicalHeaderKey("cdn-loop")]
	if !ok {
		loops = []string{""}
	}

	_, yeslog := r.Header[http.CanonicalHeaderKey("fastlike-verbose")]
	if yeslog {
		i.abilog.SetOutput(os.Stdout)
	}

	if strings.Contains(strings.Join(loops, "\x00"), "fastlike") {
		// immediately respond with a loop detection
		w.WriteHeader(http.StatusLoopDetected)
		_, _ = w.Write([]byte("Loop detected! This request has already come through your fastly program."))
		_, _ = w.Write([]byte("\n"))
		_, _ = w.Write([]byte("You probably have a non-exhaustive backend handler?"))
		return
	}

	i.ds_request = r
	i.ds_response = w
	i.ds_context = r.Context()

	// Start a goroutine which will wait for the context to cancel or wait until the wasm calls are
	// complete
	donech := make(chan struct{}, 1)
	go func(ctx context.Context) {
		select {
		case <-ctx.Done():
			// If the context cancels before we write to the donech it's a timeout/deadline/client
			// hung up and we should interrupt the wasm program.
			i.wasmctx.engine.IncrementEpoch()
		case <-donech:
			// Otherwise, we're good and don't need to do anything else.
		}
	}(r.Context())

	// The entrypoint for a fastly compute program takes no arguments and returns nothing or an
	// error. The program itself is responsible for getting a handle on the downstream request
	// and sending a response downstream.

	// Start tracking CPU time before entering guest code
	i.startExecution()

	// Get the "_start" export
	startExport := i.wasm.GetExport(i.store, "_start")
	if startExport == nil {
		panic("_start export not found in wasm module")
	}

	entry := startExport.Func()
	if entry == nil {
		panic("'_start' export is not a function")
	}

	_, err := entry.Call(i.store)

	// Stop tracking CPU time after guest code completes
	i.stopExecution()

	donech <- struct{}{}
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Error running wasm program.\n"))
		_, _ = w.Write([]byte("Below is a useless blob of wasm backtrace. There may be more in your server logs.\n"))
		_, _ = w.Write([]byte(err.Error()))
		return
	}

	// Workaround for wasmtime-go v37 epoch interruption bugs:
	// If the context was cancelled but the wasm completed successfully,
	// we need to override the response to indicate an interrupt occurred.
	// This only works with httptest.ResponseRecorder (used in tests).
	if i.ds_context.Err() != nil {
		// Try to cast to *httptest.ResponseRecorder
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
// This should be called before blocking operations (e.g., HTTP requests to backends).
// The caller must ensure resumeExecution() is called after the blocking operation.
func (i *Instance) pauseExecution() {
	// If not currently executing, nothing to pause
	if i.executionStartTime.IsZero() {
		return
	}

	// Calculate elapsed time since execution started/resumed
	elapsed := time.Since(i.executionStartTime)
	microseconds := elapsed.Microseconds()

	// Add to accumulated time
	i.activeCpuTimeUs.Add(uint64(microseconds))

	// Mark as not executing by zeroing the start time
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

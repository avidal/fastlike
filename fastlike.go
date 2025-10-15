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
	// instanceOpts are the original options passed to New(), applied to all instances
	instanceOpts []Option
}

// New returns a new Fastlike ready to create new instances from
func New(wasmfile string, instanceOpts ...Option) *Fastlike {
	f := &Fastlike{
		wasmfile:     wasmfile,
		instanceOpts: instanceOpts,
	}

	// Read the wasm file from disk
	wasmbytes, err := os.ReadFile(wasmfile)
	check(err)

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

	// instancefn creates new instances on demand when the pool is empty
	f.instancefn = func(opts ...Option) *Instance {
		// Merge the base options with any additional per-request options
		allOpts := append(instanceOpts, opts...)
		return NewInstance(wasmbytes, allOpts...)
	}

	return f
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

	select {
	case instances <- i:
		// Successfully returned to pool
	default:
		// Pool is full, instance will be garbage collected
	}
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

	// Create a new instance pool with the fresh wasm bytes
	f.instances = make(chan *Instance, poolSize)
	f.instancefn = func(opts ...Option) *Instance {
		// Merge the base options with any additional per-request options
		allOpts := append(f.instanceOpts, opts...)
		return NewInstance(wasmbytes, allOpts...)
	}

	return nil
}

// EnableReloadOnSIGHUP sets up a signal handler that reloads the wasm module
// when a SIGHUP signal is received. This is useful for hot-reloading during development
// or when using tools like watchexec to monitor file changes.
// The goroutine runs in the background until the program exits.
func (f *Fastlike) EnableReloadOnSIGHUP() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP)

	go func() {
		for range sigChan {
			log.Printf("[fastlike] Received SIGHUP, reloading wasm module from %s", f.wasmfile)
			if err := f.Reload(); err != nil {
				log.Printf("[fastlike] Reload failed: %v", err)
			} else {
				log.Printf("[fastlike] Reload completed successfully")
			}
		}
	}()
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

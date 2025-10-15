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

	// wasmfile and instanceOpts are stored for reload purposes
	wasmfile     string
	instanceOpts []Option
}

// New returns a new Fastlike ready to create new instances from
func New(wasmfile string, instanceOpts ...Option) *Fastlike {
	f := &Fastlike{
		wasmfile:     wasmfile,
		instanceOpts: instanceOpts,
	}

	// read in the file and store the bytes
	wasmbytes, err := os.ReadFile(wasmfile)
	check(err)

	size := runtime.NumCPU()

	if size > 16 {
		size = 16
	} else if size < 0 {
		size = 0
	}

	f.instances = make(chan *Instance, size)
	f.instancefn = func(opts ...Option) *Instance {
		// merge the original options with any supplied options
		opts = append(instanceOpts, opts...)
		return NewInstance(wasmbytes, opts...)
	}

	return f
}

// ServeHTTP implements http.Handler for a Fastlike module. It's a convenience function over
// `Instantiate()` followed by `.ServeHTTP` on the returned instance.
func (f *Fastlike) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	i := f.Instantiate()

	i.ServeHTTP(w, r)

	f.mu.RLock()
	instances := f.instances
	f.mu.RUnlock()

	select {
	case instances <- i:
	default:
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

	// Drain the old instances channel
	oldInstances := f.instances
	close(oldInstances)
	for range oldInstances {
		// Drain all instances (they will be garbage collected)
	}

	// Calculate pool size (same as in New)
	size := runtime.NumCPU()
	if size > 16 {
		size = 16
	} else if size < 0 {
		size = 0
	}

	// Create new instances channel and instancefn
	f.instances = make(chan *Instance, size)
	f.instancefn = func(opts ...Option) *Instance {
		// merge the original options with any supplied options
		opts = append(f.instanceOpts, opts...)
		return NewInstance(wasmbytes, opts...)
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

// Instantiate returns an Instance ready to serve requests. This may come from the instance pool if
// one is available, but otherwise will be constructed fresh.
// This *must* be called for each request, as the XQD runtime is designed around a single
// request/response pair for each instance.
func (f *Fastlike) Instantiate(opts ...Option) *Instance {
	f.mu.RLock()
	instances := f.instances
	instancefn := f.instancefn
	f.mu.RUnlock()

	select {
	case i := <-instances:
		for _, opt := range opts {
			opt(i)
		}
		return i
	default:
		return instancefn(opts...)
	}
}

// check panics if err is non-nil. Used for fatal initialization errors.
func check(err error) {
	if err != nil {
		panic(err)
	}
}

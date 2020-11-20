package fastlike

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"runtime"
	"sync"
)

// Fastlike is the entrypoint to the package, used to construct new instances ready to serve
// incoming HTTP requests. It maintains a pool of compiled and linked wasm modules to amortize
// startup costs across multiple requests. In the case of a spike of incoming requests, new
// instances will be constructed on-demand and thrown away when the request is finished to avoid an
// ever-increasing memory cost.
type Fastlike struct {
	instances chan *Instance

	// instancefn is called when a new instance must be created from scratch
	instancefn func(opts ...Option) *Instance
}

// New returns a new Fastlike ready to create new instances from
func New(wasmfile string, instanceOpts ...Option) *Fastlike {
	var f = &Fastlike{}

	// read in the file and store the bytes
	wasmbytes, err := ioutil.ReadFile(wasmfile)
	check(err)

	var size = runtime.NumCPU()

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
	var i = f.Instantiate()

	i.ServeHTTP(w, r)

	select {
	case f.instances <- i:
	default:
	}
}

func (f *Fastlike) Warmup(n int) {
	if n > cap(f.instances) {
		fmt.Printf("Warmup count %d is greater than max pool size %d. Clamping to max.\n", n, cap(f.instances))
		n = cap(f.instances)
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case f.instances <- f.instancefn():
			default:
			}
		}()
	}
	wg.Wait()
}

// Instantiate returns an Instance ready to serve requests. This may come from the instance pool if
// one is available, but otherwise will be constructed fresh.
// This *must* be called for each request, as the XQD runtime is designed around a single
// request/response pair for each instance.
func (f *Fastlike) Instantiate(opts ...Option) *Instance {
	select {
	case i := <-f.instances:
		for _, opt := range opts {
			opt(i)
		}
		return i
	default:
		return f.instancefn(opts...)
	}
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

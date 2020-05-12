// package fastlike is a Go implementation of the Fastly Compute@Edge XQD ABI, as described in the
// `fastly` crate.
// It is designed to be used as an `http.Handler`, and has a `ServeHTTP` method to accomodate.
// The ABI is designed around a single instance handling a single request and response pair. This
// package is thus designed accordingly, with each incoming HTTP request being passed to a fresh
// wasm instance.
// The public surface area of this package is intentionally small, as it is designed to operate on
// a single (request, response) pair and any fiddling with the internals can cause serious
// side-effects.
// For reference to the *guest* side of the ABI, see the following for the ABI methods:
// https://docs.rs/crate/fastly/0.3.2/source/src/abi.rs
// And for some of the constants (such as the ABI response status codes), see:
// https://docs.rs/crate/fastly-shared/0.3.2/source/src/lib.rs
package fastlike

import (
	"net/http"

	"github.com/bytecodealliance/wasmtime-go"
)

// Fastlike carries the wasm module, store, and linker and is capable of creating new instances
// ready to serve requests
type Fastlike struct {
	store  *wasmtime.Store
	wasi   *wasmtime.WasiInstance
	module *wasmtime.Module

	instanceOpts []InstanceOption
}

// New returns a new Fastlike ready to create new instances from
func New(wasmfile string, instanceOpts ...InstanceOption) *Fastlike {
	config := wasmtime.NewConfig()
	config.SetDebugInfo(true)
	config.SetWasmMultiValue(true)

	store := wasmtime.NewStore(wasmtime.NewEngineWithConfig(config))
	module, err := wasmtime.NewModuleFromFile(store, wasmfile)
	check(err)

	// These options ensure our wasm module can write to stdout/stderr
	wasicfg := wasmtime.NewWasiConfig()
	wasicfg.InheritStdout()
	wasicfg.InheritStderr()
	wasi, err := wasmtime.NewWasiInstance(store, wasicfg, "wasi_snapshot_preview1")
	check(err)

	return &Fastlike{
		store: store, wasi: wasi, module: module,
		instanceOpts: instanceOpts,
	}
}

// NewFromWasm returns a new Fastlike using the wasm bytes supplied
func NewFromWasm(wasm []byte, instanceOpts ...InstanceOption) *Fastlike {
	config := wasmtime.NewConfig()
	config.SetDebugInfo(true)
	config.SetWasmMultiValue(true)

	store := wasmtime.NewStore(wasmtime.NewEngineWithConfig(config))
	module, err := wasmtime.NewModule(store, wasm)
	check(err)

	// These options ensure our wasm module can write to stdout/stderr
	wasicfg := wasmtime.NewWasiConfig()
	wasicfg.InheritStdout()
	wasicfg.InheritStderr()
	wasi, err := wasmtime.NewWasiInstance(store, wasicfg, "wasi_snapshot_preview1")
	check(err)

	return &Fastlike{
		store: store, wasi: wasi, module: module,
		instanceOpts: instanceOpts,
	}
}

func (f *Fastlike) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var instance = f.Instantiate(f.instanceOpts...)
	instance.serve(w, r)
}

// Instantiate returns a new Instance ready to serve requests.
// This *must* be called for each request, as the XQD runtime is designed around a single
// request/response pair for each instance.
func (f *Fastlike) Instantiate(opts ...InstanceOption) *Instance {
	var i = &Instance{}
	var linker = i.linker(f.store, f.wasi)

	wasm, err := linker.Instantiate(f.module)
	check(err)
	i.wasm = wasm

	// setup our defaults here, then apply the instance options
	i.memory = &Memory{&wasmMemory{mem: wasm.GetExport("memory").Memory()}}
	i.subrequest = SubrequestHandlerIgnoreBackend(http.DefaultTransport.RoundTrip)
	i.requests = []*requestHandle{}
	i.responses = []*responseHandle{}
	i.bodies = []*bodyHandle{}

	for _, o := range opts {
		o(i)
	}

	return i
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

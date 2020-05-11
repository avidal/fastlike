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
	store      *wasmtime.Store
	wasi       *wasmtime.WasiInstance
	module     *wasmtime.Module
	subrequest SubrequestHandler
}

// New returns a new Fastlike ready to create new instances from
func New(wasmfile string) *Fastlike {
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
		subrequest: SubrequestHandlerIgnoreBackend(http.DefaultTransport.RoundTrip),
	}
}

// NewFromWasm returns a new Fastlike using the wasm bytes supplied
func NewFromWasm(wasm []byte) *Fastlike {
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
		subrequest: SubrequestHandlerIgnoreBackend(http.DefaultTransport.RoundTrip),
	}
}

// SubrequestHandler overrides the SubrequestHandler used by instantiated wasm programs.
func (f *Fastlike) SubrequestHandler(fn SubrequestHandler) {
	f.subrequest = fn
}

func (f *Fastlike) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var instance = f.Instantiate()
	instance.serve(w, r)
}

type SubrequestHandler func(backend string, r *http.Request) (*http.Response, error)

func SubrequestHandlerIgnoreBackend(fn func(*http.Request) (*http.Response, error)) SubrequestHandler {
	return func(backend string, r *http.Request) (*http.Response, error) {
		return fn(r)
	}
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
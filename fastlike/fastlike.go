// package fastlike is a Go implementation of the Fastly Compute@Edge XQD ABI, as described in the
// `fastly` crate.
// It is designed to be used as an `http.Handler`, and has a `ServeHTTP` method to accomodate.
// The ABI is designed around a single instance handling a single request and response pair. This
// package is thus designed accordingly, with each incoming HTTP request being passed to a fresh
// wasm instance.
// The public surface area of this package is intentionally small, as it is designed to operate on
// a single (request, response) pair and any fiddling with the internals can cause serious
// side-effects.
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
	}
}

func (f *Fastlike) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var instance = f.Instantiate()
	instance.serve(w, r)
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

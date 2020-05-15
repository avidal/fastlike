package fastlike

import (
	"net/http"
	"sync"

	"github.com/bytecodealliance/wasmtime-go"
)

// Fastlike carries the wasm module, store, and linker and is capable of creating new instances
// ready to serve requests
type Fastlike struct {
	*sync.Mutex
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
		Mutex: &sync.Mutex{},
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
		Mutex: &sync.Mutex{},
		store: store, wasi: wasi, module: module,
		instanceOpts: instanceOpts,
	}
}

func (f *Fastlike) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var instance = f.Instantiate(f.instanceOpts...)
	instance.ServeHTTP(w, r)
}

// Instantiate returns a new Instance ready to serve requests.
// This *must* be called for each request, as the XQD runtime is designed around a single
// request/response pair for each instance.
func (f *Fastlike) Instantiate(opts ...InstanceOption) *Instance {
	f.Lock()
	defer f.Unlock()

	var i = &Instance{}
	var linker = i.linker(f.store, f.wasi)

	wasm, err := linker.Instantiate(f.module)
	check(err)
	i.wasm = wasm

	// setup our defaults here, then apply the instance options
	i.memory = &Memory{&wasmMemory{mem: wasm.GetExport("memory").Memory()}}
	i.requests = RequestHandles{}
	i.responses = ResponseHandles{}
	i.bodies = BodyHandles{}

	// By default, any subrequests will return a 502
	i.backends = defaultBackendHandler()

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

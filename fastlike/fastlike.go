package fastlike

import (
	"fmt"
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

func (f *Fastlike) Instantiate() *Instance {
	linker := wasmtime.NewLinker(f.store)
	check(linker.DefineWasi(f.wasi))

	linker.DefineFunc("env", "xqd_req_body_downstream_get", xqd_req_body_downstream_get)
	linker.DefineFunc("env", "xqd_req_version_get", xqd_req_version_get)
	linker.DefineFunc("env", "xqd_req_version_set", xqd_req_version_set)
	linker.DefineFunc("env", "xqd_req_method_get", xqd_req_method_get)
	linker.DefineFunc("env", "xqd_req_uri_get", xqd_req_uri_get)
	linker.DefineFunc("env", "xqd_req_new", xqd_req_new)

	for _, n := range []string{"xqd_init", "xqd_resp_new", "xqd_body_new"} {
		linker.DefineFunc("env", n, wasm1)
	}

	for _, n := range []string{
		"xqd_resp_status_set",
		"xqd_resp_status_get",
		"xqd_resp_version_get",
		"xqd_resp_version_set",
		"xqd_req_version_set",
		"xqd_req_body_downstream_get",
		"xqd_body_append",
	} {
		linker.DefineFunc("env", n, wasm2)
	}

	for _, n := range []string{
		"xqd_resp_send_downstream",
		"xqd_req_method_set",
		"xqd_req_uri_set",
	} {
		linker.DefineFunc("env", n, wasm3)
	}

	for _, n := range []string{
		"xqd_req_cache_override_set",
		"xqd_req_uri_get",
		"xqd_req_method_get",
		"xqd_body_read",
	} {
		linker.DefineFunc("env", n, wasm4)
	}

	for _, n := range []string{
		"xqd_req_header_values_set",
		"xqd_resp_header_values_set",
		"xqd_body_write",
	} {
		linker.DefineFunc("env", n, wasm5)
	}

	for _, n := range []string{
		"xqd_req_header_names_get",
		"xqd_resp_header_names_get",
		"xqd_req_send",
	} {
		linker.DefineFunc("env", n, wasm6)
	}

	for _, n := range []string{
		"xqd_req_header_values_get",
		"xqd_resp_header_values_get",
	} {
		linker.DefineFunc("env", n, wasm8)
	}

	// Consider having `Instantiate` make a new ABI(<Instance>) and hold it?
	// How to avoid cyclical dependencies?
	// Maybe better to implement the ABI methods *on* Instance, then each ABI method can easily
	// access memory and request data, etc. Make it flat.
	// imports will look like `instance.xqd_req_send`
	// and the function will have a signature `func (i *Instance) xqd_req_send()`
	// and can access whatever else on `i`, such as the memory view and the request.
	// This effectively makes our Instance a module instance + the abi implementation
	// which sounds like a good thing?
	i, err := linker.Instantiate(f.module)
	check(err)
	return &Instance{i: i, mem: WasmMemory{}}
}

// Instance carries an instantiated module
type Instance struct {
	i   *wasmtime.Instance
	mem WasmMemory
}

// ServeHTTP implements net/http.ServeHTTP using a fastly compute program
func (i *Instance) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	i.Reset()
}

// Reset *must* be called on an Instance when it's handling a fresh request
// This ensures the memory is erased
func (i *Instance) Reset() {}

var memory WasmMemory

func (i *Instance) Run() {
	wmemory := i.i.GetExport("memory").Memory()
	fmt.Printf("memory size=%d\n", wmemory.Size())
	i.mem = WasmMemory{wmemory}
	memory = i.mem

	entry := i.i.GetExport("main2").Func()
	val, err := entry.Call()
	check(err)
	fmt.Printf("entry() = %+v\n", val)
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

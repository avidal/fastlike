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

	var i = &Instance{}

	for _, n := range []string{
		"xqd_resp_status_set",
		"xqd_resp_status_get",
		"xqd_resp_version_get",
		"xqd_resp_version_set",
		"xqd_body_append",
	} {
		check(linker.DefineFunc("env", n, i.wasm2(n)))
	}

	for _, n := range []string{
		"xqd_resp_send_downstream",
		"xqd_req_method_set",
		"xqd_req_uri_set",
	} {
		check(linker.DefineFunc("env", n, i.wasm3(n)))
	}

	for _, n := range []string{
		"xqd_req_cache_override_set",
		"xqd_body_read",
	} {
		check(linker.DefineFunc("env", n, i.wasm4(n)))
	}

	for _, n := range []string{
		"xqd_req_header_values_set",
		"xqd_resp_header_values_set",
		"xqd_body_write",
	} {
		check(linker.DefineFunc("env", n, i.wasm5(n)))
	}

	for _, n := range []string{
		"xqd_req_header_names_get",
		"xqd_resp_header_names_get",
		"xqd_req_send",
	} {
		check(linker.DefineFunc("env", n, i.wasm6(n)))
	}

	for _, n := range []string{
		"xqd_req_header_values_get",
		"xqd_resp_header_values_get",
	} {
		check(linker.DefineFunc("env", n, i.wasm8(n)))
	}

	linker.AllowShadowing(true)

	linker.DefineFunc("env", "xqd_req_new", i.xqd_req_new)
	linker.DefineFunc("env", "xqd_req_body_downstream_get", i.xqd_req_body_downstream_get)
	linker.DefineFunc("env", "xqd_req_version_get", i.xqd_req_version_get)
	linker.DefineFunc("env", "xqd_req_version_set", i.xqd_req_version_set)
	linker.DefineFunc("env", "xqd_req_method_get", i.xqd_req_method_get)
	linker.DefineFunc("env", "xqd_req_uri_get", i.xqd_req_uri_get)

	linker.DefineFunc("env", "xqd_resp_new", i.xqd_resp_new)
	linker.DefineFunc("env", "xqd_init", i.xqd_init)
	linker.DefineFunc("env", "xqd_body_new", i.xqd_body_new)
	linker.DefineFunc("env", "xqd_body_write", i.xqd_body_write)

	// Consider having `Instantiate` make a new ABI(<Instance>) and hold it?
	// How to avoid cyclical dependencies?
	// Maybe better to implement the ABI methods *on* Instance, then each ABI method can easily
	// access memory and request data, etc. Make it flat.
	// imports will look like `instance.xqd_req_send`
	// and the function will have a signature `func (i *Instance) xqd_req_send()`
	// and can access whatever else on `i`, such as the memory view and the request.
	// This effectively makes our Instance a module instance + the abi implementation
	// which sounds like a good thing?
	wi, err := linker.Instantiate(f.module)
	check(err)
	i.i = wi
	i.memory = &Memory{}
	return i
}

// Instance is an implementation of the XQD ABI along with a wasmtime.Instance configured to use it
type Instance struct {
	i      *wasmtime.Instance
	memory *Memory
}

// ServeHTTP implements net/http.ServeHTTP using a fastly compute program
func (i *Instance) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	i.Reset()
}

// Reset *must* be called on an Instance when it's handling a fresh request
// This ensures the memory is erased
func (i *Instance) Reset() {}

func (i *Instance) Run() {
	wmemory := i.i.GetExport("memory").Memory()
	fmt.Printf("memory size=%d\n", wmemory.Size())
	i.memory = &Memory{wmemory}

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

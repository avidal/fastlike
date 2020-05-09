package fastlike

import (
	"fmt"
	"net/http"
	"net/http/httptest"

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
		"xqd_resp_header_names_get",
		"xqd_req_send",
	} {
		check(linker.DefineFunc("env", n, i.wasm6(n)))
	}

	for _, n := range []string{
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
	linker.DefineFunc("env", "xqd_req_method_set", i.xqd_req_method_set)
	linker.DefineFunc("env", "xqd_req_uri_get", i.xqd_req_uri_get)
	linker.DefineFunc("env", "xqd_req_uri_set", i.xqd_req_uri_set)
	linker.DefineFunc("env", "xqd_req_header_names_get", i.xqd_req_header_names_get)
	linker.DefineFunc("env", "xqd_req_header_values_get", i.xqd_req_header_values_get)
	linker.DefineFunc("env", "xqd_req_send", i.xqd_req_send)

	linker.DefineFunc("env", "xqd_resp_new", i.xqd_resp_new)
	linker.DefineFunc("env", "xqd_init", i.xqd_init)
	linker.DefineFunc("env", "xqd_body_new", i.xqd_body_new)
	linker.DefineFunc("env", "xqd_body_write", i.xqd_body_write)

	linker.DefineFunc("env", "xqd_resp_status_set", i.xqd_resp_status_set)
	linker.DefineFunc("env", "xqd_resp_status_get", i.xqd_resp_status_get)
	linker.DefineFunc("env", "xqd_resp_version_set", i.xqd_resp_version_set)
	linker.DefineFunc("env", "xqd_resp_send_downstream", i.xqd_resp_send_downstream)

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
	i.memory = &Memory{wi.GetExport("memory").Memory()}
	i.requests = []*requestHandle{}
	i.responses = []*responseHandle{}
	i.bodies = []*bodyHandle{}
	return i
}

func (f *Fastlike) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var instance = f.Instantiate()
	instance.serve(w, r)
}

// Instance is an implementation of the XQD ABI along with a wasmtime.Instance configured to use it
type Instance struct {
	i      *wasmtime.Instance
	memory *Memory

	requests  []*requestHandle
	responses []*responseHandle
	bodies    []*bodyHandle

	// ds_request represents the downstream request, ie the one originated from the user agent
	ds_request *http.Request

	// ds_response represents the downstream response, where we're going to write the final output
	ds_response http.ResponseWriter
}

func (i *Instance) serve(w http.ResponseWriter, r *http.Request) {
	i.ds_request = r
	i.ds_response = w

	// The entrypoint for a fastly compute program takes no arguments and returns nothing or an
	// error. The program itself is responsible for getting a handle on the downstream request
	// and sending a response downstream.
	entry := i.i.GetExport("_start").Func()
	val, err := entry.Call()
	check(err)
	fmt.Printf("entry() = %+v\n", val)
}

func (i *Instance) Run() {
	var r, err = http.NewRequest("GET", "http://localhost:8080", nil)
	r.Header.Add("authorization", "bearer foobar")
	r.Header.Add("kaac", "whatever")

	// create a bunch of fabricated headers to push outside of 4096 bytes when written over the abi
	// boundary
	for i := 0; i < 2; i++ {
		r.Header.Add(fmt.Sprintf("synthetic-key-%03d", i), fmt.Sprintf("synthetic-value-%03d", i))
	}

	check(err)
	var w = httptest.NewRecorder()

	i.serve(w, r)

	fmt.Printf("Response:\n")
	fmt.Printf("%+v\n", w)
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

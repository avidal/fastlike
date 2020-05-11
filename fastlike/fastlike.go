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

func (f *Fastlike) Instantiate() *Instance {
	// While linkers are reusable across multiple instances, in practice it's not very helpful, as
	// we need to be able to bind host methods that are instance specific. It wouldn't be very
	// useful to make a generic "xqd_req_body_downstream_get" function available to module
	// instances if that function has no way to find the downstream request.
	// While we *could* have a list of waiting requests, we'd have no reasonable way to bind the
	// *responses* back to the originating requests in order to send them back downstream.
	// The linking step is cheap enough that it's not worth the implementation overhead to come up
	// with an alternative solution.
	linker := wasmtime.NewLinker(f.store)
	check(linker.DefineWasi(f.wasi))

	var i = &Instance{}

	for _, n := range []string{
		"xqd_resp_version_get",
		"xqd_body_append",
	} {
		check(linker.DefineFunc("env", n, i.wasm2(n)))
	}

	for _, n := range []string{
		"xqd_req_cache_override_set",
		"xqd_body_read",
	} {
		check(linker.DefineFunc("env", n, i.wasm4(n)))
	}

	for _, n := range []string{
		"xqd_resp_header_values_set",
	} {
		check(linker.DefineFunc("env", n, i.wasm5(n)))
	}

	for _, n := range []string{
		"xqd_resp_header_names_get",
	} {
		check(linker.DefineFunc("env", n, i.wasm6(n)))
	}

	for _, n := range []string{
		"xqd_resp_header_values_get",
	} {
		check(linker.DefineFunc("env", n, i.wasm8(n)))
	}

	linker.DefineFunc("env", "xqd_init", i.xqd_init)
	linker.DefineFunc("env", "xqd_req_body_downstream_get", i.xqd_req_body_downstream_get)
	linker.DefineFunc("env", "xqd_resp_send_downstream", i.xqd_resp_send_downstream)

	linker.DefineFunc("env", "xqd_req_new", i.xqd_req_new)
	linker.DefineFunc("env", "xqd_req_version_get", i.xqd_req_version_get)
	linker.DefineFunc("env", "xqd_req_version_set", i.xqd_req_version_set)
	linker.DefineFunc("env", "xqd_req_method_get", i.xqd_req_method_get)
	linker.DefineFunc("env", "xqd_req_method_set", i.xqd_req_method_set)
	linker.DefineFunc("env", "xqd_req_uri_get", i.xqd_req_uri_get)
	linker.DefineFunc("env", "xqd_req_uri_set", i.xqd_req_uri_set)
	linker.DefineFunc("env", "xqd_req_header_names_get", i.xqd_req_header_names_get)
	linker.DefineFunc("env", "xqd_req_header_values_get", i.xqd_req_header_values_get)
	linker.DefineFunc("env", "xqd_req_header_values_set", i.xqd_req_header_values_set)
	linker.DefineFunc("env", "xqd_req_send", i.xqd_req_send)

	linker.DefineFunc("env", "xqd_resp_new", i.xqd_resp_new)
	linker.DefineFunc("env", "xqd_resp_status_get", i.xqd_resp_status_get)
	linker.DefineFunc("env", "xqd_resp_status_set", i.xqd_resp_status_set)
	linker.DefineFunc("env", "xqd_resp_version_set", i.xqd_resp_version_set)

	linker.DefineFunc("env", "xqd_body_new", i.xqd_body_new)
	linker.DefineFunc("env", "xqd_body_write", i.xqd_body_write)

	wi, err := linker.Instantiate(f.module)
	check(err)
	i.i = wi
	i.memory = &Memory{&wasmMemory{mem: wi.GetExport("memory").Memory()}}
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

func check(err error) {
	if err != nil {
		panic(err)
	}
}

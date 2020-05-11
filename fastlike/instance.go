package fastlike

import (
	"fmt"
	"net/http"

	"github.com/bytecodealliance/wasmtime-go"
)

// Instance is an implementation of the XQD ABI along with a wasmtime.Instance configured to use it
// TODO: This has no public methods or public members. Should it even be public? The API could just
// be New and Fastlike.ServeHTTP(w, r)?
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

	// transport is used to issue subrequests
	transport http.RoundTripper
}

// serve serves the supplied request and response pair. This is not safe to call twice.
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

// Instantiate returns a new Instance ready to serve requests.
// This *must* be called for each request, as the XQD runtime is designed around a single
// request/response pair for each instance.
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

	// XQD Stubbing -{{{
	// TODO: All of these XQD methods are stubbed. As they are implemented, they'll be removed from
	// here and explicitly linked in the section below.
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
	// End XQD Stubbing -}}}

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
	i.transport = f.transport
	return i
}

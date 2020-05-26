package fastlike

import (
	"github.com/bytecodealliance/wasmtime-go"
)

type wasmContext struct {
	store  *wasmtime.Store
	wasi   *wasmtime.WasiInstance
	module *wasmtime.Module
	linker *wasmtime.Linker
}

func (i *Instance) compile(wasmbytes []byte) {
	config := wasmtime.NewConfig()

	check(config.CacheConfigLoadDefault())

	store := wasmtime.NewStore(wasmtime.NewEngineWithConfig(config))
	module, err := wasmtime.NewModule(store, wasmbytes)
	check(err)

	wasicfg := wasmtime.NewWasiConfig()
	wasicfg.InheritStdout()
	wasicfg.InheritStderr()

	wasi, err := wasmtime.NewWasiInstance(store, wasicfg, "wasi_snapshot_preview1")
	check(err)

	linker := wasmtime.NewLinker(store)
	check(linker.DefineWasi(wasi))

	// XQD Stubbing -{{{
	// TODO: All of these XQD methods are stubbed. As they are implemented, they'll be removed from
	// here and explicitly linked in the section below.
	linker.DefineFunc("env", "xqd_log_endpoint_get", i.wasm3("xqd_log_endpoint_get"))
	linker.DefineFunc("env", "xqd_log_write", i.wasm4("xqd_log_write"))

	linker.DefineFunc("env", "xqd_pending_req_poll", i.wasm4("xqd_pending_req_poll"))
	linker.DefineFunc("env", "xqd_pending_req_select", i.wasm5("xqd_pending_req_select"))
	linker.DefineFunc("env", "xqd_pending_req_wait", i.wasm3("xqd_pending_req_wait"))

	linker.DefineFunc("env", "xqd_req_downstream_tls_cipher_openssl_name", i.wasm3("xqd_req_downstream_tls_cipher_openssl_name"))
	linker.DefineFunc("env", "xqd_req_downstream_tls_protocol", i.wasm3("xqd_req_downstream_tls_protocol"))
	linker.DefineFunc("env", "xqd_req_downstream_tls_client_hello", i.wasm3("xqd_req_downstream_tls_client_hello"))

	linker.DefineFunc("env", "xqd_req_header_insert", i.wasm5("xqd_req_header_insert"))
	linker.DefineFunc("env", "xqd_req_send_async", i.wasm5("xqd_req_send_async"))

	linker.DefineFunc("env", "xqd_req_original_header_count", i.wasm1("xqd_req_original_header_count"))
	linker.DefineFunc("env", "xqd_req_header_remove", i.wasm3("xqd_req_header_remove"))

	linker.DefineFunc("env", "xqd_resp_header_append", i.wasm5("xqd_resp_header_append"))
	linker.DefineFunc("env", "xqd_resp_header_insert", i.wasm5("xqd_resp_header_insert"))
	linker.DefineFunc("env", "xqd_resp_header_value_get", i.wasm6("xqd_resp_header_value_get"))
	linker.DefineFunc("env", "xqd_resp_header_remove", i.wasm3("xqd_resp_header_remove"))

	linker.DefineFunc("env", "xqd_body_close_downstream", i.wasm1("xqd_body_close_downstream"))

	// End XQD Stubbing -}}}

	// xqd.go
	linker.DefineFunc("fastly", "init", i.xqd_init)
	linker.DefineFunc("fastly_uap", "parse", i.xqd_uap_parse)

	linker.DefineFunc("env", "xqd_req_body_downstream_get", i.xqd_req_body_downstream_get)
	linker.DefineFunc("env", "xqd_resp_send_downstream", i.xqd_resp_send_downstream)
	linker.DefineFunc("env", "xqd_req_downstream_client_ip_addr", i.xqd_req_downstream_client_ip_addr)

	// xqd_request.go
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
	linker.DefineFunc("env", "xqd_req_cache_override_set", i.xqd_req_cache_override_set)
	// The Go http implementation doesn't make it easy to get at the original headers in order, so
	// we just use the same sorted order
	linker.DefineFunc("env", "xqd_req_original_header_names_get", i.xqd_req_header_names_get)

	// xqd_response.go
	linker.DefineFunc("env", "xqd_resp_new", i.xqd_resp_new)
	linker.DefineFunc("env", "xqd_resp_status_get", i.xqd_resp_status_get)
	linker.DefineFunc("env", "xqd_resp_status_set", i.xqd_resp_status_set)
	linker.DefineFunc("env", "xqd_resp_version_get", i.xqd_resp_version_get)
	linker.DefineFunc("env", "xqd_resp_version_set", i.xqd_resp_version_set)
	linker.DefineFunc("env", "xqd_resp_header_names_get", i.xqd_resp_header_names_get)
	linker.DefineFunc("env", "xqd_resp_header_values_get", i.xqd_resp_header_values_get)
	linker.DefineFunc("env", "xqd_resp_header_values_set", i.xqd_resp_header_values_set)

	// xqd_body.go
	linker.DefineFunc("env", "xqd_body_new", i.xqd_body_new)
	linker.DefineFunc("env", "xqd_body_write", i.xqd_body_write)
	linker.DefineFunc("env", "xqd_body_read", i.xqd_body_read)
	linker.DefineFunc("env", "xqd_body_append", i.xqd_body_append)

	i.wasmctx = &wasmContext{
		store:  store,
		wasi:   wasi,
		module: module,
		linker: linker,
	}
}

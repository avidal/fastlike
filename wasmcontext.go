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
	config.SetInterruptable(true)

	store := wasmtime.NewStore(wasmtime.NewEngineWithConfig(config))
	module, err := wasmtime.NewModule(store.Engine, wasmbytes)
	check(err)

	wasicfg := wasmtime.NewWasiConfig()
	wasicfg.InheritStdout()
	wasicfg.InheritStderr()

	wasi, err := wasmtime.NewWasiInstance(store, wasicfg, "wasi_snapshot_preview1")
	check(err)

	linker := wasmtime.NewLinker(store)
	check(linker.DefineWasi(wasi))

	i.link(linker)
	i.linklegacy(linker)

	i.wasmctx = &wasmContext{
		store:  store,
		wasi:   wasi,
		module: module,
		linker: linker,
	}
}

func (i *Instance) link(linker *wasmtime.Linker) {
	// XQD Stubbing -{{{
	// TODO: All of these XQD methods are stubbed. As they are implemented, they'll be removed from
	// here and explicitly linked in the section below.
	linker.DefineFunc("fastly_log", "endpoint_get", i.wasm3("endpoint_get"))
	linker.DefineFunc("fastly_log", "write", i.wasm4("write"))

	linker.DefineFunc("fastly_http_req", "pending_req_poll", i.wasm4("pending_req_poll"))
	linker.DefineFunc("fastly_http_req", "pending_req_select", i.wasm5("pending_req_select"))
	linker.DefineFunc("fastly_http_req", "pending_req_wait", i.wasm3("pending_req_wait"))

	linker.DefineFunc("fastly_http_req", "downstream_tls_cipher_openssl_name", i.wasm3("downstream_tls_cipher_openssl_name"))
	linker.DefineFunc("fastly_http_req", "downstream_tls_protocol", i.wasm3("downstream_tls_protocol"))
	linker.DefineFunc("fastly_http_req", "downstream_tls_client_hello", i.wasm3("downstream_tls_client_hello"))

	linker.DefineFunc("fastly_http_req", "header_insert", i.wasm5("header_insert"))
	linker.DefineFunc("fastly_http_req", "send_async", i.wasm5("send_async"))

	linker.DefineFunc("fastly_http_req", "original_header_count", i.wasm1("original_header_count"))

	linker.DefineFunc("fastly_http_resp", "header_append", i.wasm5("header_append"))
	linker.DefineFunc("fastly_http_resp", "header_insert", i.wasm5("header_insert"))
	linker.DefineFunc("fastly_http_resp", "header_value_get", i.wasm6("header_value_get"))
	linker.DefineFunc("fastly_http_resp", "header_remove", i.wasm3("header_remove"))

	// End XQD Stubbing -}}}

	// xqd.go
	linker.DefineFunc("fastly_abi", "init", i.xqd_init)
	linker.DefineFunc("fastly_uap", "parse", i.xqd_uap_parse)

	// xqd_request.go
	linker.DefineFunc("fastly_http_req", "body_downstream_get", i.xqd_req_body_downstream_get)
	linker.DefineFunc("fastly_http_req", "downstream_client_ip_addr", i.xqd_req_downstream_client_ip_addr)
	linker.DefineFunc("fastly_http_req", "new", i.xqd_req_new)
	linker.DefineFunc("fastly_http_req", "version_get", i.xqd_req_version_get)
	linker.DefineFunc("fastly_http_req", "version_set", i.xqd_req_version_set)
	linker.DefineFunc("fastly_http_req", "method_get", i.xqd_req_method_get)
	linker.DefineFunc("fastly_http_req", "method_set", i.xqd_req_method_set)
	linker.DefineFunc("fastly_http_req", "uri_get", i.xqd_req_uri_get)
	linker.DefineFunc("fastly_http_req", "uri_set", i.xqd_req_uri_set)
	linker.DefineFunc("fastly_http_req", "header_names_get", i.xqd_req_header_names_get)
	linker.DefineFunc("fastly_http_req", "header_remove", i.xqd_req_header_remove)
	linker.DefineFunc("fastly_http_req", "header_value_get", i.xqd_req_header_value_get)
	linker.DefineFunc("fastly_http_req", "header_values_get", i.xqd_req_header_values_get)
	linker.DefineFunc("fastly_http_req", "header_values_set", i.xqd_req_header_values_set)
	linker.DefineFunc("fastly_http_req", "send", i.xqd_req_send)
	linker.DefineFunc("fastly_http_req", "cache_override_set", i.xqd_req_cache_override_set)
	linker.DefineFunc("fastly_http_req", "cache_override_v2_set", i.xqd_req_cache_override_v2_set)
	// The Go http implementation doesn't make it easy to get at the original headers in order, so
	// we just use the same sorted order
	linker.DefineFunc("fastly_http_req", "original_header_names_get", i.xqd_req_header_names_get)

	// xqd_response.go
	linker.DefineFunc("fastly_http_resp", "send_downstream", i.xqd_resp_send_downstream)
	linker.DefineFunc("fastly_http_resp", "new", i.xqd_resp_new)
	linker.DefineFunc("fastly_http_resp", "status_get", i.xqd_resp_status_get)
	linker.DefineFunc("fastly_http_resp", "status_set", i.xqd_resp_status_set)
	linker.DefineFunc("fastly_http_resp", "version_get", i.xqd_resp_version_get)
	linker.DefineFunc("fastly_http_resp", "version_set", i.xqd_resp_version_set)
	linker.DefineFunc("fastly_http_resp", "header_names_get", i.xqd_resp_header_names_get)
	linker.DefineFunc("fastly_http_resp", "header_remove", i.xqd_resp_header_remove)
	linker.DefineFunc("fastly_http_resp", "header_values_get", i.xqd_resp_header_values_get)
	linker.DefineFunc("fastly_http_resp", "header_values_set", i.xqd_resp_header_values_set)

	// xqd_body.go
	linker.DefineFunc("fastly_http_body", "new", i.xqd_body_new)
	linker.DefineFunc("fastly_http_body", "write", i.xqd_body_write)
	linker.DefineFunc("fastly_http_body", "read", i.xqd_body_read)
	linker.DefineFunc("fastly_http_body", "append", i.xqd_body_append)
	linker.DefineFunc("fastly_http_body", "close", i.xqd_body_close)
}

// linklegacy links in the abi methods using the legacy method names
func (i *Instance) linklegacy(linker *wasmtime.Linker) {
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

	linker.DefineFunc("env", "xqd_resp_header_append", i.wasm5("xqd_resp_header_append"))
	linker.DefineFunc("env", "xqd_resp_header_insert", i.wasm5("xqd_resp_header_insert"))
	linker.DefineFunc("env", "xqd_resp_header_value_get", i.wasm6("xqd_resp_header_value_get"))

	linker.DefineFunc("env", "xqd_body_close_downstream", i.xqd_body_close)

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
	linker.DefineFunc("env", "xqd_req_header_remove", i.xqd_req_header_remove)
	linker.DefineFunc("env", "xqd_req_header_names_get", i.xqd_req_header_names_get)
	linker.DefineFunc("env", "xqd_req_header_value_get", i.xqd_req_header_value_get)
	linker.DefineFunc("env", "xqd_req_header_values_get", i.xqd_req_header_values_get)
	linker.DefineFunc("env", "xqd_req_header_values_set", i.xqd_req_header_values_set)
	linker.DefineFunc("env", "xqd_req_send", i.xqd_req_send)
	linker.DefineFunc("env", "xqd_req_cache_override_set", i.xqd_req_cache_override_set)
	linker.DefineFunc("env", "xqd_req_cache_override_v2_set", i.xqd_req_cache_override_v2_set)
	// The Go http implementation doesn't make it easy to get at the original headers in order, so
	// we just use the same sorted order
	linker.DefineFunc("env", "xqd_req_original_header_names_get", i.xqd_req_header_names_get)

	// xqd_response.go
	linker.DefineFunc("env", "xqd_resp_new", i.xqd_resp_new)
	linker.DefineFunc("env", "xqd_resp_status_get", i.xqd_resp_status_get)
	linker.DefineFunc("env", "xqd_resp_status_set", i.xqd_resp_status_set)
	linker.DefineFunc("env", "xqd_resp_version_get", i.xqd_resp_version_get)
	linker.DefineFunc("env", "xqd_resp_version_set", i.xqd_resp_version_set)
	linker.DefineFunc("env", "xqd_resp_header_remove", i.xqd_resp_header_remove)
	linker.DefineFunc("env", "xqd_resp_header_names_get", i.xqd_resp_header_names_get)
	linker.DefineFunc("env", "xqd_resp_header_values_get", i.xqd_resp_header_values_get)
	linker.DefineFunc("env", "xqd_resp_header_values_set", i.xqd_resp_header_values_set)

	// xqd_body.go
	linker.DefineFunc("env", "xqd_body_new", i.xqd_body_new)
	linker.DefineFunc("env", "xqd_body_write", i.xqd_body_write)
	linker.DefineFunc("env", "xqd_body_read", i.xqd_body_read)
	linker.DefineFunc("env", "xqd_body_append", i.xqd_body_append)
}

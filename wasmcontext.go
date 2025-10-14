package fastlike

import (
	"github.com/bytecodealliance/wasmtime-go"
)

type wasmContext struct {
	store  *wasmtime.Store
	engine *wasmtime.Engine
	module *wasmtime.Module
	linker *wasmtime.Linker
}

func (i *Instance) compile(wasmbytes []byte) {
	config := wasmtime.NewConfig()

	check(config.CacheConfigLoadDefault())
	config.SetEpochInterruption(true)

	engine := wasmtime.NewEngineWithConfig(config)
	store := wasmtime.NewStore(engine)
	module, err := wasmtime.NewModule(store.Engine, wasmbytes)
	check(err)

	wasicfg := wasmtime.NewWasiConfig()
	wasicfg.InheritStdout()
	wasicfg.InheritStderr()

	store.SetWasi(wasicfg)

	linker := wasmtime.NewLinker(engine)
	check(linker.DefineWasi())

	i.link(store, linker)
	i.linklegacy(store, linker)

	i.wasmctx = &wasmContext{
		store:  store,
		engine: engine,
		module: module,
		linker: linker,
	}
}

func (i *Instance) link(store *wasmtime.Store, linker *wasmtime.Linker) {
	// XQD Stubbing -{{{
	// TODO: All of these XQD methods are stubbed. As they are implemented, they'll be removed from
	// here and explicitly linked in the section below.
	linker.DefineFunc(store, "fastly_http_req", "downstream_tls_cipher_openssl_name", i.wasm3("downstream_tls_cipher_openssl_name"))
	linker.DefineFunc(store, "fastly_http_req", "downstream_tls_protocol", i.wasm3("downstream_tls_protocol"))
	linker.DefineFunc(store, "fastly_http_req", "downstream_tls_client_hello", i.wasm3("downstream_tls_client_hello"))

	linker.DefineFunc(store, "fastly_http_req", "header_insert", i.wasm5("header_insert"))

	linker.DefineFunc(store, "fastly_http_req", "original_header_count", i.wasm1("original_header_count"))

	linker.DefineFunc(store, "fastly_http_resp", "header_append", i.wasm5("header_append"))
	linker.DefineFunc(store, "fastly_http_resp", "header_insert", i.wasm5("header_insert"))
	linker.DefineFunc(store, "fastly_http_resp", "header_value_get", i.wasm6("header_value_get"))
	linker.DefineFunc(store, "fastly_http_resp", "header_remove", i.wasm3("header_remove"))
	// End XQD Stubbing -}}}

	// xqd.go
	linker.DefineFunc(store, "fastly_abi", "init", i.xqd_init)
	linker.DefineFunc(store, "fastly_uap", "parse", i.xqd_uap_parse)

	// xqd_request.go
	linker.DefineFunc(store, "fastly_http_req", "body_downstream_get", i.xqd_req_body_downstream_get)
	linker.DefineFunc(store, "fastly_http_req", "downstream_client_ip_addr", i.xqd_req_downstream_client_ip_addr)
	linker.DefineFunc(store, "fastly_http_req", "new", i.xqd_req_new)
	linker.DefineFunc(store, "fastly_http_req", "version_get", i.xqd_req_version_get)
	linker.DefineFunc(store, "fastly_http_req", "version_set", i.xqd_req_version_set)
	linker.DefineFunc(store, "fastly_http_req", "method_get", i.xqd_req_method_get)
	linker.DefineFunc(store, "fastly_http_req", "method_set", i.xqd_req_method_set)
	linker.DefineFunc(store, "fastly_http_req", "uri_get", i.xqd_req_uri_get)
	linker.DefineFunc(store, "fastly_http_req", "uri_set", i.xqd_req_uri_set)
	linker.DefineFunc(store, "fastly_http_req", "header_names_get", i.xqd_req_header_names_get)
	linker.DefineFunc(store, "fastly_http_req", "header_remove", i.xqd_req_header_remove)
	linker.DefineFunc(store, "fastly_http_req", "header_value_get", i.xqd_req_header_value_get)
	linker.DefineFunc(store, "fastly_http_req", "header_values_get", i.xqd_req_header_values_get)
	linker.DefineFunc(store, "fastly_http_req", "header_values_set", i.xqd_req_header_values_set)
	linker.DefineFunc(store, "fastly_http_req", "send", i.xqd_req_send)
	linker.DefineFunc(store, "fastly_http_req", "send_async", i.xqd_req_send_async)
	linker.DefineFunc(store, "fastly_http_req", "send_async_streaming", i.xqd_req_send_async_streaming)
	linker.DefineFunc(store, "fastly_http_req", "send_async_v2", i.xqd_req_send_async_v2)
	linker.DefineFunc(store, "fastly_http_req", "pending_req_poll", i.xqd_pending_req_poll)
	linker.DefineFunc(store, "fastly_http_req", "pending_req_poll_v2", i.xqd_pending_req_poll_v2)
	linker.DefineFunc(store, "fastly_http_req", "pending_req_wait", i.xqd_pending_req_wait)
	linker.DefineFunc(store, "fastly_http_req", "pending_req_wait_v2", i.xqd_pending_req_wait_v2)
	linker.DefineFunc(store, "fastly_http_req", "pending_req_select", i.xqd_pending_req_select)
	linker.DefineFunc(store, "fastly_http_req", "pending_req_select_v2", i.xqd_pending_req_select_v2)
	linker.DefineFunc(store, "fastly_http_req", "cache_override_set", i.xqd_req_cache_override_set)
	linker.DefineFunc(store, "fastly_http_req", "cache_override_v2_set", i.xqd_req_cache_override_v2_set)
	// The Go http implementation doesn't make it easy to get at the original headers in order, so
	// we just use the same sorted order
	linker.DefineFunc(store, "fastly_http_req", "original_header_names_get", i.xqd_req_header_names_get)
	linker.DefineFunc(store, "fastly_http_req", "close", i.xqd_req_close)

	// xqd_response.go
	linker.DefineFunc(store, "fastly_http_resp", "send_downstream", i.xqd_resp_send_downstream)
	linker.DefineFunc(store, "fastly_http_resp", "new", i.xqd_resp_new)
	linker.DefineFunc(store, "fastly_http_resp", "status_get", i.xqd_resp_status_get)
	linker.DefineFunc(store, "fastly_http_resp", "status_set", i.xqd_resp_status_set)
	linker.DefineFunc(store, "fastly_http_resp", "version_get", i.xqd_resp_version_get)
	linker.DefineFunc(store, "fastly_http_resp", "version_set", i.xqd_resp_version_set)
	linker.DefineFunc(store, "fastly_http_resp", "header_names_get", i.xqd_resp_header_names_get)
	linker.DefineFunc(store, "fastly_http_resp", "header_remove", i.xqd_resp_header_remove)
	linker.DefineFunc(store, "fastly_http_resp", "header_values_get", i.xqd_resp_header_values_get)
	linker.DefineFunc(store, "fastly_http_resp", "header_values_set", i.xqd_resp_header_values_set)
	linker.DefineFunc(store, "fastly_http_resp", "close", i.xqd_resp_close)

	// xqd_body.go
	linker.DefineFunc(store, "fastly_http_body", "new", i.xqd_body_new)
	linker.DefineFunc(store, "fastly_http_body", "write", i.xqd_body_write)
	linker.DefineFunc(store, "fastly_http_body", "read", i.xqd_body_read)
	linker.DefineFunc(store, "fastly_http_body", "append", i.xqd_body_append)
	linker.DefineFunc(store, "fastly_http_body", "close", i.xqd_body_close)
	linker.DefineFunc(store, "fastly_http_body", "abandon", i.xqd_body_abandon)
	linker.DefineFunc(store, "fastly_http_body", "known_length", i.xqd_body_known_length)
	linker.DefineFunc(store, "fastly_http_body", "trailer_append", i.xqd_body_trailer_append)
	linker.DefineFunc(store, "fastly_http_body", "trailer_names_get", i.xqd_body_trailer_names_get)
	linker.DefineFunc(store, "fastly_http_body", "trailer_value_get", i.xqd_body_trailer_value_get)
	linker.DefineFunc(store, "fastly_http_body", "trailer_values_get", i.xqd_body_trailer_values_get)

	// xqd_log.go
	linker.DefineFunc(store, "fastly_log", "endpoint_get", i.xqd_log_endpoint_get)
	linker.DefineFunc(store, "fastly_log", "write", i.xqd_log_write)

	// xqd_dictionary.go
	linker.DefineFunc(store, "fastly_dictionary", "open", i.xqd_dictionary_open)
	linker.DefineFunc(store, "fastly_dictionary", "get", i.xqd_dictionary_get)
}

// linklegacy links in the abi methods using the legacy method names
func (i *Instance) linklegacy(store *wasmtime.Store, linker *wasmtime.Linker) {
	// XQD Stubbing -{{{
	// TODO: All of these XQD methods are stubbed. As they are implemented, they'll be removed from
	// here and explicitly linked in the section below.
	linker.DefineFunc(store, "env", "xqd_req_downstream_tls_cipher_openssl_name", i.wasm3("xqd_req_downstream_tls_cipher_openssl_name"))
	linker.DefineFunc(store, "env", "xqd_req_downstream_tls_protocol", i.wasm3("xqd_req_downstream_tls_protocol"))
	linker.DefineFunc(store, "env", "xqd_req_downstream_tls_client_hello", i.wasm3("xqd_req_downstream_tls_client_hello"))

	linker.DefineFunc(store, "env", "xqd_req_header_insert", i.wasm5("xqd_req_header_insert"))

	linker.DefineFunc(store, "env", "xqd_req_original_header_count", i.wasm1("xqd_req_original_header_count"))

	linker.DefineFunc(store, "env", "xqd_resp_header_append", i.wasm5("xqd_resp_header_append"))
	linker.DefineFunc(store, "env", "xqd_resp_header_insert", i.wasm5("xqd_resp_header_insert"))
	linker.DefineFunc(store, "env", "xqd_resp_header_value_get", i.wasm6("xqd_resp_header_value_get"))

	linker.DefineFunc(store, "env", "xqd_body_close_downstream", i.xqd_body_close)
	// End XQD Stubbing -}}}

	// xqd.go
	linker.DefineFunc(store, "fastly", "init", i.xqd_init)
	linker.DefineFunc(store, "fastly_uap", "parse", i.xqd_uap_parse)

	linker.DefineFunc(store, "env", "xqd_req_body_downstream_get", i.xqd_req_body_downstream_get)
	linker.DefineFunc(store, "env", "xqd_resp_send_downstream", i.xqd_resp_send_downstream)
	linker.DefineFunc(store, "env", "xqd_req_downstream_client_ip_addr", i.xqd_req_downstream_client_ip_addr)

	// xqd_request.go
	linker.DefineFunc(store, "env", "xqd_req_new", i.xqd_req_new)
	linker.DefineFunc(store, "env", "xqd_req_version_get", i.xqd_req_version_get)
	linker.DefineFunc(store, "env", "xqd_req_version_set", i.xqd_req_version_set)
	linker.DefineFunc(store, "env", "xqd_req_method_get", i.xqd_req_method_get)
	linker.DefineFunc(store, "env", "xqd_req_method_set", i.xqd_req_method_set)
	linker.DefineFunc(store, "env", "xqd_req_uri_get", i.xqd_req_uri_get)
	linker.DefineFunc(store, "env", "xqd_req_uri_set", i.xqd_req_uri_set)
	linker.DefineFunc(store, "env", "xqd_req_header_remove", i.xqd_req_header_remove)
	linker.DefineFunc(store, "env", "xqd_req_header_names_get", i.xqd_req_header_names_get)
	linker.DefineFunc(store, "env", "xqd_req_header_value_get", i.xqd_req_header_value_get)
	linker.DefineFunc(store, "env", "xqd_req_header_values_get", i.xqd_req_header_values_get)
	linker.DefineFunc(store, "env", "xqd_req_header_values_set", i.xqd_req_header_values_set)
	linker.DefineFunc(store, "env", "xqd_req_send", i.xqd_req_send)
	linker.DefineFunc(store, "env", "xqd_req_send_async", i.xqd_req_send_async)
	linker.DefineFunc(store, "env", "xqd_req_send_async_streaming", i.xqd_req_send_async_streaming)
	linker.DefineFunc(store, "env", "xqd_req_send_async_v2", i.xqd_req_send_async_v2)
	linker.DefineFunc(store, "env", "xqd_pending_req_poll", i.xqd_pending_req_poll)
	linker.DefineFunc(store, "env", "xqd_pending_req_poll_v2", i.xqd_pending_req_poll_v2)
	linker.DefineFunc(store, "env", "xqd_pending_req_wait", i.xqd_pending_req_wait)
	linker.DefineFunc(store, "env", "xqd_pending_req_wait_v2", i.xqd_pending_req_wait_v2)
	linker.DefineFunc(store, "env", "xqd_pending_req_select", i.xqd_pending_req_select)
	linker.DefineFunc(store, "env", "xqd_pending_req_select_v2", i.xqd_pending_req_select_v2)
	linker.DefineFunc(store, "env", "xqd_req_cache_override_set", i.xqd_req_cache_override_set)
	linker.DefineFunc(store, "env", "xqd_req_cache_override_v2_set", i.xqd_req_cache_override_v2_set)
	// The Go http implementation doesn't make it easy to get at the original headers in order, so
	// we just use the same sorted order
	linker.DefineFunc(store, "env", "xqd_req_original_header_names_get", i.xqd_req_header_names_get)
	linker.DefineFunc(store, "env", "xqd_req_close", i.xqd_req_close)

	// xqd_response.go
	linker.DefineFunc(store, "env", "xqd_resp_new", i.xqd_resp_new)
	linker.DefineFunc(store, "env", "xqd_resp_status_get", i.xqd_resp_status_get)
	linker.DefineFunc(store, "env", "xqd_resp_status_set", i.xqd_resp_status_set)
	linker.DefineFunc(store, "env", "xqd_resp_version_get", i.xqd_resp_version_get)
	linker.DefineFunc(store, "env", "xqd_resp_version_set", i.xqd_resp_version_set)
	linker.DefineFunc(store, "env", "xqd_resp_header_remove", i.xqd_resp_header_remove)
	linker.DefineFunc(store, "env", "xqd_resp_header_names_get", i.xqd_resp_header_names_get)
	linker.DefineFunc(store, "env", "xqd_resp_header_values_get", i.xqd_resp_header_values_get)
	linker.DefineFunc(store, "env", "xqd_resp_header_values_set", i.xqd_resp_header_values_set)
	linker.DefineFunc(store, "env", "xqd_resp_close", i.xqd_resp_close)

	// xqd_body.go
	linker.DefineFunc(store, "env", "xqd_body_new", i.xqd_body_new)
	linker.DefineFunc(store, "env", "xqd_body_write", i.xqd_body_write)
	linker.DefineFunc(store, "env", "xqd_body_read", i.xqd_body_read)
	linker.DefineFunc(store, "env", "xqd_body_append", i.xqd_body_append)
	linker.DefineFunc(store, "env", "xqd_body_abandon", i.xqd_body_abandon)
	linker.DefineFunc(store, "env", "xqd_body_known_length", i.xqd_body_known_length)
	linker.DefineFunc(store, "env", "xqd_body_trailer_append", i.xqd_body_trailer_append)
	linker.DefineFunc(store, "env", "xqd_body_trailer_names_get", i.xqd_body_trailer_names_get)
	linker.DefineFunc(store, "env", "xqd_body_trailer_value_get", i.xqd_body_trailer_value_get)
	linker.DefineFunc(store, "env", "xqd_body_trailer_values_get", i.xqd_body_trailer_values_get)

	// xqd_log.go
	linker.DefineFunc(store, "env", "xqd_log_endpoint_get", i.xqd_log_endpoint_get)
	linker.DefineFunc(store, "env", "xqd_log_write", i.xqd_log_write)
}

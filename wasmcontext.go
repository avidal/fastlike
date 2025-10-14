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
	_ = linker.DefineFunc(store, "fastly_http_req", "original_header_count", i.wasm1("original_header_count"))

	_ = linker.DefineFunc(store, "fastly_http_resp", "header_value_get", i.wasm6("header_value_get"))
	_ = linker.DefineFunc(store, "fastly_http_resp", "header_remove", i.wasm3("header_remove"))

	// xqd_http_cache.go
	_ = linker.DefineFunc(store, "fastly_http_cache", "is_request_cacheable", i.xqd_http_cache_is_request_cacheable)
	_ = linker.DefineFunc(store, "fastly_http_cache", "get_suggested_cache_key", i.xqd_http_cache_get_suggested_cache_key)
	// End XQD Stubbing -}}}

	// xqd.go
	_ = linker.DefineFunc(store, "fastly_abi", "init", i.xqd_init)
	_ = linker.DefineFunc(store, "fastly_uap", "parse", i.xqd_uap_parse)

	// xqd_request.go
	_ = linker.DefineFunc(store, "fastly_http_req", "body_downstream_get", i.xqd_req_body_downstream_get)
	_ = linker.DefineFunc(store, "fastly_http_req", "downstream_client_ip_addr", i.xqd_req_downstream_client_ip_addr)
	_ = linker.DefineFunc(store, "fastly_http_req", "new", i.xqd_req_new)
	_ = linker.DefineFunc(store, "fastly_http_req", "version_get", i.xqd_req_version_get)
	_ = linker.DefineFunc(store, "fastly_http_req", "version_set", i.xqd_req_version_set)
	_ = linker.DefineFunc(store, "fastly_http_req", "method_get", i.xqd_req_method_get)
	_ = linker.DefineFunc(store, "fastly_http_req", "method_set", i.xqd_req_method_set)
	_ = linker.DefineFunc(store, "fastly_http_req", "uri_get", i.xqd_req_uri_get)
	_ = linker.DefineFunc(store, "fastly_http_req", "uri_set", i.xqd_req_uri_set)
	_ = linker.DefineFunc(store, "fastly_http_req", "header_names_get", i.xqd_req_header_names_get)
	_ = linker.DefineFunc(store, "fastly_http_req", "header_remove", i.xqd_req_header_remove)
	_ = linker.DefineFunc(store, "fastly_http_req", "header_insert", i.xqd_req_header_insert)
	_ = linker.DefineFunc(store, "fastly_http_req", "header_append", i.xqd_req_header_append)
	_ = linker.DefineFunc(store, "fastly_http_req", "header_value_get", i.xqd_req_header_value_get)
	_ = linker.DefineFunc(store, "fastly_http_req", "header_values_get", i.xqd_req_header_values_get)
	_ = linker.DefineFunc(store, "fastly_http_req", "header_values_set", i.xqd_req_header_values_set)
	_ = linker.DefineFunc(store, "fastly_http_req", "send", i.xqd_req_send)
	_ = linker.DefineFunc(store, "fastly_http_req", "send_v2", i.xqd_req_send_v2)
	_ = linker.DefineFunc(store, "fastly_http_req", "send_v3", i.xqd_req_send_v3)
	_ = linker.DefineFunc(store, "fastly_http_req", "send_async", i.xqd_req_send_async)
	_ = linker.DefineFunc(store, "fastly_http_req", "send_async_streaming", i.xqd_req_send_async_streaming)
	_ = linker.DefineFunc(store, "fastly_http_req", "send_async_v2", i.xqd_req_send_async_v2)
	_ = linker.DefineFunc(store, "fastly_http_req", "pending_req_poll", i.xqd_pending_req_poll)
	_ = linker.DefineFunc(store, "fastly_http_req", "pending_req_poll_v2", i.xqd_pending_req_poll_v2)
	_ = linker.DefineFunc(store, "fastly_http_req", "pending_req_wait", i.xqd_pending_req_wait)
	_ = linker.DefineFunc(store, "fastly_http_req", "pending_req_wait_v2", i.xqd_pending_req_wait_v2)
	_ = linker.DefineFunc(store, "fastly_http_req", "pending_req_select", i.xqd_pending_req_select)
	_ = linker.DefineFunc(store, "fastly_http_req", "pending_req_select_v2", i.xqd_pending_req_select_v2)
	_ = linker.DefineFunc(store, "fastly_http_req", "cache_override_set", i.xqd_req_cache_override_set)
	_ = linker.DefineFunc(store, "fastly_http_req", "cache_override_v2_set", i.xqd_req_cache_override_v2_set)
	// The Go http implementation doesn't make it easy to get at the original headers in order, so
	// we just use the same sorted order
	_ = linker.DefineFunc(store, "fastly_http_req", "original_header_names_get", i.xqd_req_header_names_get)
	_ = linker.DefineFunc(store, "fastly_http_req", "close", i.xqd_req_close)
	_ = linker.DefineFunc(store, "fastly_http_req", "downstream_client_ddos_detected", i.xqd_req_downstream_client_ddos_detected)
	_ = linker.DefineFunc(store, "fastly_http_req", "fastly_key_is_valid", i.xqd_req_fastly_key_is_valid)
	_ = linker.DefineFunc(store, "fastly_http_req", "downstream_compliance_region", i.xqd_req_downstream_compliance_region)
	_ = linker.DefineFunc(store, "fastly_http_req", "on_behalf_of", i.xqd_req_on_behalf_of)
	_ = linker.DefineFunc(store, "fastly_http_req", "framing_headers_mode_set", i.xqd_req_framing_headers_mode_set)
	_ = linker.DefineFunc(store, "fastly_http_req", "auto_decompress_response_set", i.xqd_req_auto_decompress_response_set)
	_ = linker.DefineFunc(store, "fastly_http_req", "register_dynamic_backend", i.xqd_req_register_dynamic_backend)
	// DEPRECATED: use fastly_http_downstream versions
	_ = linker.DefineFunc(store, "fastly_http_req", "downstream_client_h2_fingerprint", i.xqd_http_downstream_client_h2_fingerprint)
	_ = linker.DefineFunc(store, "fastly_http_req", "downstream_client_oh_fingerprint", i.xqd_http_downstream_client_oh_fingerprint)
	_ = linker.DefineFunc(store, "fastly_http_req", "downstream_tls_ja3_md5", i.xqd_http_downstream_tls_ja3_md5)
	_ = linker.DefineFunc(store, "fastly_http_req", "downstream_tls_ja4", i.xqd_http_downstream_tls_ja4)

	// xqd_response.go
	_ = linker.DefineFunc(store, "fastly_http_resp", "send_downstream", i.xqd_resp_send_downstream)
	_ = linker.DefineFunc(store, "fastly_http_resp", "new", i.xqd_resp_new)
	_ = linker.DefineFunc(store, "fastly_http_resp", "status_get", i.xqd_resp_status_get)
	_ = linker.DefineFunc(store, "fastly_http_resp", "status_set", i.xqd_resp_status_set)
	_ = linker.DefineFunc(store, "fastly_http_resp", "version_get", i.xqd_resp_version_get)
	_ = linker.DefineFunc(store, "fastly_http_resp", "version_set", i.xqd_resp_version_set)
	_ = linker.DefineFunc(store, "fastly_http_resp", "header_names_get", i.xqd_resp_header_names_get)
	_ = linker.DefineFunc(store, "fastly_http_resp", "header_remove", i.xqd_resp_header_remove)
	_ = linker.DefineFunc(store, "fastly_http_resp", "header_insert", i.xqd_resp_header_insert)
	_ = linker.DefineFunc(store, "fastly_http_resp", "header_append", i.xqd_resp_header_append)
	_ = linker.DefineFunc(store, "fastly_http_resp", "header_values_get", i.xqd_resp_header_values_get)
	_ = linker.DefineFunc(store, "fastly_http_resp", "header_values_set", i.xqd_resp_header_values_set)
	_ = linker.DefineFunc(store, "fastly_http_resp", "close", i.xqd_resp_close)
	_ = linker.DefineFunc(store, "fastly_http_resp", "framing_headers_mode_set", i.xqd_resp_framing_headers_mode_set)
	_ = linker.DefineFunc(store, "fastly_http_resp", "http_keepalive_mode_set", i.xqd_resp_http_keepalive_mode_set)
	_ = linker.DefineFunc(store, "fastly_http_resp", "get_addr_dest_ip", i.xqd_resp_get_addr_dest_ip)
	_ = linker.DefineFunc(store, "fastly_http_resp", "get_addr_dest_port", i.xqd_resp_get_addr_dest_port)

	// xqd_body.go
	_ = linker.DefineFunc(store, "fastly_http_body", "new", i.xqd_body_new)
	_ = linker.DefineFunc(store, "fastly_http_body", "write", i.xqd_body_write)
	_ = linker.DefineFunc(store, "fastly_http_body", "read", i.xqd_body_read)
	_ = linker.DefineFunc(store, "fastly_http_body", "append", i.xqd_body_append)
	_ = linker.DefineFunc(store, "fastly_http_body", "close", i.xqd_body_close)
	_ = linker.DefineFunc(store, "fastly_http_body", "abandon", i.xqd_body_abandon)
	_ = linker.DefineFunc(store, "fastly_http_body", "known_length", i.xqd_body_known_length)
	_ = linker.DefineFunc(store, "fastly_http_body", "trailer_append", i.xqd_body_trailer_append)
	_ = linker.DefineFunc(store, "fastly_http_body", "trailer_names_get", i.xqd_body_trailer_names_get)
	_ = linker.DefineFunc(store, "fastly_http_body", "trailer_value_get", i.xqd_body_trailer_value_get)
	_ = linker.DefineFunc(store, "fastly_http_body", "trailer_values_get", i.xqd_body_trailer_values_get)

	// xqd_log.go
	_ = linker.DefineFunc(store, "fastly_log", "endpoint_get", i.xqd_log_endpoint_get)
	_ = linker.DefineFunc(store, "fastly_log", "write", i.xqd_log_write)

	// xqd_dictionary.go
	_ = linker.DefineFunc(store, "fastly_dictionary", "open", i.xqd_dictionary_open)
	_ = linker.DefineFunc(store, "fastly_dictionary", "get", i.xqd_dictionary_get)

	// xqd_config_store.go
	_ = linker.DefineFunc(store, "fastly_config_store", "open", i.xqd_config_store_open)
	_ = linker.DefineFunc(store, "fastly_config_store", "get", i.xqd_config_store_get)

	// xqd_secret_store.go
	_ = linker.DefineFunc(store, "fastly_secret_store", "open", i.xqd_secret_store_open)
	_ = linker.DefineFunc(store, "fastly_secret_store", "get", i.xqd_secret_store_get)
	_ = linker.DefineFunc(store, "fastly_secret_store", "plaintext", i.xqd_secret_store_plaintext)
	_ = linker.DefineFunc(store, "fastly_secret_store", "from_bytes", i.xqd_secret_store_from_bytes)

	// xqd_device_detection.go
	_ = linker.DefineFunc(store, "fastly_device_detection", "lookup", i.xqd_device_detection_lookup)

	// xqd_acl.go
	_ = linker.DefineFunc(store, "fastly_acl", "open", i.xqd_acl_open)
	_ = linker.DefineFunc(store, "fastly_acl", "lookup", i.xqd_acl_lookup)

	// xqd_erl.go
	_ = linker.DefineFunc(store, "fastly_erl", "check_rate", i.xqd_erl_check_rate)
	_ = linker.DefineFunc(store, "fastly_erl", "ratecounter_increment", i.xqd_erl_ratecounter_increment)
	_ = linker.DefineFunc(store, "fastly_erl", "ratecounter_lookup_rate", i.xqd_erl_ratecounter_lookup_rate)
	_ = linker.DefineFunc(store, "fastly_erl", "ratecounter_lookup_count", i.xqd_erl_ratecounter_lookup_count)
	_ = linker.DefineFunc(store, "fastly_erl", "penaltybox_add", i.xqd_erl_penaltybox_add)
	_ = linker.DefineFunc(store, "fastly_erl", "penaltybox_has", i.xqd_erl_penaltybox_has)

	// xqd_kv_store.go
	_ = linker.DefineFunc(store, "fastly_kv_store", "open", i.xqd_kv_store_open)
	_ = linker.DefineFunc(store, "fastly_kv_store", "lookup", i.xqd_kv_store_lookup)
	_ = linker.DefineFunc(store, "fastly_kv_store", "lookup_wait", i.xqd_kv_store_lookup_wait)
	_ = linker.DefineFunc(store, "fastly_kv_store", "lookup_wait_v2", i.xqd_kv_store_lookup_wait_v2)
	_ = linker.DefineFunc(store, "fastly_kv_store", "insert", i.xqd_kv_store_insert)
	_ = linker.DefineFunc(store, "fastly_kv_store", "insert_wait", i.xqd_kv_store_insert_wait)
	_ = linker.DefineFunc(store, "fastly_kv_store", "delete", i.xqd_kv_store_delete)
	_ = linker.DefineFunc(store, "fastly_kv_store", "delete_wait", i.xqd_kv_store_delete_wait)
	_ = linker.DefineFunc(store, "fastly_kv_store", "list", i.xqd_kv_store_list)
	_ = linker.DefineFunc(store, "fastly_kv_store", "list_wait", i.xqd_kv_store_list_wait)

	// xqd_backend.go
	_ = linker.DefineFunc(store, "fastly_backend", "exists", i.xqd_backend_exists)
	_ = linker.DefineFunc(store, "fastly_backend", "is_healthy", i.xqd_backend_is_healthy)
	_ = linker.DefineFunc(store, "fastly_backend", "is_dynamic", i.xqd_backend_is_dynamic)
	_ = linker.DefineFunc(store, "fastly_backend", "get_host", i.xqd_backend_get_host)
	_ = linker.DefineFunc(store, "fastly_backend", "get_override_host", i.xqd_backend_get_override_host)
	_ = linker.DefineFunc(store, "fastly_backend", "get_port", i.xqd_backend_get_port)
	_ = linker.DefineFunc(store, "fastly_backend", "get_connect_timeout_ms", i.xqd_backend_get_connect_timeout_ms)
	_ = linker.DefineFunc(store, "fastly_backend", "get_first_byte_timeout_ms", i.xqd_backend_get_first_byte_timeout_ms)
	_ = linker.DefineFunc(store, "fastly_backend", "get_between_bytes_timeout_ms", i.xqd_backend_get_between_bytes_timeout_ms)
	_ = linker.DefineFunc(store, "fastly_backend", "is_ssl", i.xqd_backend_is_ssl)
	_ = linker.DefineFunc(store, "fastly_backend", "get_ssl_min_version", i.xqd_backend_get_ssl_min_version)
	_ = linker.DefineFunc(store, "fastly_backend", "get_ssl_max_version", i.xqd_backend_get_ssl_max_version)
	_ = linker.DefineFunc(store, "fastly_backend", "get_http_keepalive_time", i.xqd_backend_get_http_keepalive_time)
	_ = linker.DefineFunc(store, "fastly_backend", "get_tcp_keepalive_enable", i.xqd_backend_get_tcp_keepalive_enable)
	_ = linker.DefineFunc(store, "fastly_backend", "get_tcp_keepalive_interval", i.xqd_backend_get_tcp_keepalive_interval)
	_ = linker.DefineFunc(store, "fastly_backend", "get_tcp_keepalive_probes", i.xqd_backend_get_tcp_keepalive_probes)
	_ = linker.DefineFunc(store, "fastly_backend", "get_tcp_keepalive_time", i.xqd_backend_get_tcp_keepalive_time)

	// xqd_compute_runtime.go
	_ = linker.DefineFunc(store, "fastly_compute_runtime", "get_vcpu_ms", i.xqd_compute_runtime_get_vcpu_ms)

	// xqd_cache.go
	_ = linker.DefineFunc(store, "fastly_cache", "lookup", i.xqd_cache_lookup)
	_ = linker.DefineFunc(store, "fastly_cache", "insert", i.xqd_cache_insert)
	_ = linker.DefineFunc(store, "fastly_cache", "transaction_lookup", i.xqd_cache_transaction_lookup)
	_ = linker.DefineFunc(store, "fastly_cache", "transaction_lookup_async", i.xqd_cache_transaction_lookup_async)
	_ = linker.DefineFunc(store, "fastly_cache", "cache_busy_handle_wait", i.xqd_cache_busy_handle_wait)
	_ = linker.DefineFunc(store, "fastly_cache", "transaction_insert", i.xqd_cache_transaction_insert)
	_ = linker.DefineFunc(store, "fastly_cache", "transaction_insert_and_stream_back", i.xqd_cache_transaction_insert_and_stream_back)
	_ = linker.DefineFunc(store, "fastly_cache", "transaction_update", i.xqd_cache_transaction_update)
	_ = linker.DefineFunc(store, "fastly_cache", "transaction_cancel", i.xqd_cache_transaction_cancel)
	_ = linker.DefineFunc(store, "fastly_cache", "close_busy", i.xqd_cache_close_busy)
	_ = linker.DefineFunc(store, "fastly_cache", "close", i.xqd_cache_close)
	_ = linker.DefineFunc(store, "fastly_cache", "get_state", i.xqd_cache_get_state)
	_ = linker.DefineFunc(store, "fastly_cache", "get_user_metadata", i.xqd_cache_get_user_metadata)
	_ = linker.DefineFunc(store, "fastly_cache", "get_body", i.xqd_cache_get_body)
	_ = linker.DefineFunc(store, "fastly_cache", "get_length", i.xqd_cache_get_length)
	_ = linker.DefineFunc(store, "fastly_cache", "get_max_age_ns", i.xqd_cache_get_max_age_ns)
	_ = linker.DefineFunc(store, "fastly_cache", "get_stale_while_revalidate_ns", i.xqd_cache_get_stale_while_revalidate_ns)
	_ = linker.DefineFunc(store, "fastly_cache", "get_age_ns", i.xqd_cache_get_age_ns)
	_ = linker.DefineFunc(store, "fastly_cache", "get_hits", i.xqd_cache_get_hits)

	// xqd_purge.go
	_ = linker.DefineFunc(store, "fastly_purge", "purge_surrogate_key", i.xqd_purge_surrogate_key)

	// xqd_async_io.go
	_ = linker.DefineFunc(store, "fastly_async_io", "select", i.xqd_async_io_select)
	_ = linker.DefineFunc(store, "fastly_async_io", "is_ready", i.xqd_async_io_is_ready)

	// xqd_http_downstream.go
	_ = linker.DefineFunc(store, "fastly_http_downstream", "next_request", i.xqd_http_downstream_next_request)
	_ = linker.DefineFunc(store, "fastly_http_downstream", "next_request_wait", i.xqd_http_downstream_next_request_wait)
	_ = linker.DefineFunc(store, "fastly_http_downstream", "next_request_abandon", i.xqd_http_downstream_next_request_abandon)
	_ = linker.DefineFunc(store, "fastly_http_downstream", "downstream_original_header_names", i.xqd_http_downstream_original_header_names)
	_ = linker.DefineFunc(store, "fastly_http_downstream", "downstream_original_header_count", i.xqd_http_downstream_original_header_count)
	_ = linker.DefineFunc(store, "fastly_http_downstream", "downstream_tls_cipher_openssl_name", i.xqd_http_downstream_tls_cipher_openssl_name)
	_ = linker.DefineFunc(store, "fastly_http_downstream", "downstream_tls_protocol", i.xqd_http_downstream_tls_protocol)
	_ = linker.DefineFunc(store, "fastly_http_downstream", "downstream_tls_client_servername", i.xqd_http_downstream_tls_client_servername)
	_ = linker.DefineFunc(store, "fastly_http_downstream", "downstream_tls_client_hello", i.xqd_http_downstream_tls_client_hello)
	_ = linker.DefineFunc(store, "fastly_http_downstream", "downstream_tls_raw_client_certificate", i.xqd_http_downstream_tls_raw_client_certificate)
	_ = linker.DefineFunc(store, "fastly_http_downstream", "downstream_tls_client_cert_verify_result", i.xqd_http_downstream_tls_client_cert_verify_result)
	_ = linker.DefineFunc(store, "fastly_http_downstream", "downstream_client_h2_fingerprint", i.xqd_http_downstream_client_h2_fingerprint)
	_ = linker.DefineFunc(store, "fastly_http_downstream", "downstream_client_oh_fingerprint", i.xqd_http_downstream_client_oh_fingerprint)
	_ = linker.DefineFunc(store, "fastly_http_downstream", "downstream_tls_ja3_md5", i.xqd_http_downstream_tls_ja3_md5)
	_ = linker.DefineFunc(store, "fastly_http_downstream", "downstream_tls_ja4", i.xqd_http_downstream_tls_ja4)
}

// linklegacy links in the abi methods using the legacy method names
func (i *Instance) linklegacy(store *wasmtime.Store, linker *wasmtime.Linker) {
	// XQD Stubbing -{{{
	// TODO: All of these XQD methods are stubbed. As they are implemented, they'll be removed from
	// here and explicitly linked in the section below.
	_ = linker.DefineFunc(store, "env", "xqd_req_original_header_count", i.wasm1("xqd_req_original_header_count"))

	_ = linker.DefineFunc(store, "env", "xqd_resp_header_value_get", i.wasm6("xqd_resp_header_value_get"))

	_ = linker.DefineFunc(store, "env", "xqd_body_close_downstream", i.xqd_body_close)
	// End XQD Stubbing -}}}

	// xqd.go
	_ = linker.DefineFunc(store, "fastly", "init", i.xqd_init)
	_ = linker.DefineFunc(store, "fastly_uap", "parse", i.xqd_uap_parse)

	_ = linker.DefineFunc(store, "env", "xqd_req_body_downstream_get", i.xqd_req_body_downstream_get)
	_ = linker.DefineFunc(store, "env", "xqd_resp_send_downstream", i.xqd_resp_send_downstream)
	_ = linker.DefineFunc(store, "env", "xqd_req_downstream_client_ip_addr", i.xqd_req_downstream_client_ip_addr)

	// xqd_request.go
	_ = linker.DefineFunc(store, "env", "xqd_req_new", i.xqd_req_new)
	_ = linker.DefineFunc(store, "env", "xqd_req_version_get", i.xqd_req_version_get)
	_ = linker.DefineFunc(store, "env", "xqd_req_version_set", i.xqd_req_version_set)
	_ = linker.DefineFunc(store, "env", "xqd_req_method_get", i.xqd_req_method_get)
	_ = linker.DefineFunc(store, "env", "xqd_req_method_set", i.xqd_req_method_set)
	_ = linker.DefineFunc(store, "env", "xqd_req_uri_get", i.xqd_req_uri_get)
	_ = linker.DefineFunc(store, "env", "xqd_req_uri_set", i.xqd_req_uri_set)
	_ = linker.DefineFunc(store, "env", "xqd_req_header_remove", i.xqd_req_header_remove)
	_ = linker.DefineFunc(store, "env", "xqd_req_header_insert", i.xqd_req_header_insert)
	_ = linker.DefineFunc(store, "env", "xqd_req_header_append", i.xqd_req_header_append)
	_ = linker.DefineFunc(store, "env", "xqd_req_header_names_get", i.xqd_req_header_names_get)
	_ = linker.DefineFunc(store, "env", "xqd_req_header_value_get", i.xqd_req_header_value_get)
	_ = linker.DefineFunc(store, "env", "xqd_req_header_values_get", i.xqd_req_header_values_get)
	_ = linker.DefineFunc(store, "env", "xqd_req_header_values_set", i.xqd_req_header_values_set)
	_ = linker.DefineFunc(store, "env", "xqd_req_send", i.xqd_req_send)
	_ = linker.DefineFunc(store, "env", "xqd_req_send_v2", i.xqd_req_send_v2)
	_ = linker.DefineFunc(store, "env", "xqd_req_send_v3", i.xqd_req_send_v3)
	_ = linker.DefineFunc(store, "env", "xqd_req_send_async", i.xqd_req_send_async)
	_ = linker.DefineFunc(store, "env", "xqd_req_send_async_streaming", i.xqd_req_send_async_streaming)
	_ = linker.DefineFunc(store, "env", "xqd_req_send_async_v2", i.xqd_req_send_async_v2)
	_ = linker.DefineFunc(store, "env", "xqd_pending_req_poll", i.xqd_pending_req_poll)
	_ = linker.DefineFunc(store, "env", "xqd_pending_req_poll_v2", i.xqd_pending_req_poll_v2)
	_ = linker.DefineFunc(store, "env", "xqd_pending_req_wait", i.xqd_pending_req_wait)
	_ = linker.DefineFunc(store, "env", "xqd_pending_req_wait_v2", i.xqd_pending_req_wait_v2)
	_ = linker.DefineFunc(store, "env", "xqd_pending_req_select", i.xqd_pending_req_select)
	_ = linker.DefineFunc(store, "env", "xqd_pending_req_select_v2", i.xqd_pending_req_select_v2)
	_ = linker.DefineFunc(store, "env", "xqd_req_cache_override_set", i.xqd_req_cache_override_set)
	_ = linker.DefineFunc(store, "env", "xqd_req_cache_override_v2_set", i.xqd_req_cache_override_v2_set)
	// The Go http implementation doesn't make it easy to get at the original headers in order, so
	// we just use the same sorted order
	_ = linker.DefineFunc(store, "env", "xqd_req_original_header_names_get", i.xqd_req_header_names_get)
	_ = linker.DefineFunc(store, "env", "xqd_req_close", i.xqd_req_close)
	_ = linker.DefineFunc(store, "env", "xqd_req_downstream_client_ddos_detected", i.xqd_req_downstream_client_ddos_detected)
	_ = linker.DefineFunc(store, "env", "xqd_req_fastly_key_is_valid", i.xqd_req_fastly_key_is_valid)
	_ = linker.DefineFunc(store, "env", "xqd_req_downstream_compliance_region", i.xqd_req_downstream_compliance_region)
	_ = linker.DefineFunc(store, "env", "xqd_req_on_behalf_of", i.xqd_req_on_behalf_of)
	_ = linker.DefineFunc(store, "env", "xqd_req_framing_headers_mode_set", i.xqd_req_framing_headers_mode_set)
	_ = linker.DefineFunc(store, "env", "xqd_req_auto_decompress_response_set", i.xqd_req_auto_decompress_response_set)
	_ = linker.DefineFunc(store, "env", "xqd_req_register_dynamic_backend", i.xqd_req_register_dynamic_backend)

	// xqd_response.go
	_ = linker.DefineFunc(store, "env", "xqd_resp_new", i.xqd_resp_new)
	_ = linker.DefineFunc(store, "env", "xqd_resp_status_get", i.xqd_resp_status_get)
	_ = linker.DefineFunc(store, "env", "xqd_resp_status_set", i.xqd_resp_status_set)
	_ = linker.DefineFunc(store, "env", "xqd_resp_version_get", i.xqd_resp_version_get)
	_ = linker.DefineFunc(store, "env", "xqd_resp_version_set", i.xqd_resp_version_set)
	_ = linker.DefineFunc(store, "env", "xqd_resp_header_remove", i.xqd_resp_header_remove)
	_ = linker.DefineFunc(store, "env", "xqd_resp_header_insert", i.xqd_resp_header_insert)
	_ = linker.DefineFunc(store, "env", "xqd_resp_header_append", i.xqd_resp_header_append)
	_ = linker.DefineFunc(store, "env", "xqd_resp_header_names_get", i.xqd_resp_header_names_get)
	_ = linker.DefineFunc(store, "env", "xqd_resp_header_values_get", i.xqd_resp_header_values_get)
	_ = linker.DefineFunc(store, "env", "xqd_resp_header_values_set", i.xqd_resp_header_values_set)
	_ = linker.DefineFunc(store, "env", "xqd_resp_close", i.xqd_resp_close)
	_ = linker.DefineFunc(store, "env", "xqd_resp_framing_headers_mode_set", i.xqd_resp_framing_headers_mode_set)
	_ = linker.DefineFunc(store, "env", "xqd_resp_http_keepalive_mode_set", i.xqd_resp_http_keepalive_mode_set)
	_ = linker.DefineFunc(store, "env", "xqd_resp_get_addr_dest_ip", i.xqd_resp_get_addr_dest_ip)
	_ = linker.DefineFunc(store, "env", "xqd_resp_get_addr_dest_port", i.xqd_resp_get_addr_dest_port)

	// xqd_body.go
	_ = linker.DefineFunc(store, "env", "xqd_body_new", i.xqd_body_new)
	_ = linker.DefineFunc(store, "env", "xqd_body_write", i.xqd_body_write)
	_ = linker.DefineFunc(store, "env", "xqd_body_read", i.xqd_body_read)
	_ = linker.DefineFunc(store, "env", "xqd_body_append", i.xqd_body_append)
	_ = linker.DefineFunc(store, "env", "xqd_body_abandon", i.xqd_body_abandon)
	_ = linker.DefineFunc(store, "env", "xqd_body_known_length", i.xqd_body_known_length)
	_ = linker.DefineFunc(store, "env", "xqd_body_trailer_append", i.xqd_body_trailer_append)
	_ = linker.DefineFunc(store, "env", "xqd_body_trailer_names_get", i.xqd_body_trailer_names_get)
	_ = linker.DefineFunc(store, "env", "xqd_body_trailer_value_get", i.xqd_body_trailer_value_get)
	_ = linker.DefineFunc(store, "env", "xqd_body_trailer_values_get", i.xqd_body_trailer_values_get)

	// xqd_log.go
	_ = linker.DefineFunc(store, "env", "xqd_log_endpoint_get", i.xqd_log_endpoint_get)
	_ = linker.DefineFunc(store, "env", "xqd_log_write", i.xqd_log_write)

	// xqd_acl.go
	_ = linker.DefineFunc(store, "env", "xqd_acl_open", i.xqd_acl_open)
	_ = linker.DefineFunc(store, "env", "xqd_acl_lookup", i.xqd_acl_lookup)

	// xqd_erl.go
	_ = linker.DefineFunc(store, "env", "xqd_erl_check_rate", i.xqd_erl_check_rate)
	_ = linker.DefineFunc(store, "env", "xqd_erl_ratecounter_increment", i.xqd_erl_ratecounter_increment)
	_ = linker.DefineFunc(store, "env", "xqd_erl_ratecounter_lookup_rate", i.xqd_erl_ratecounter_lookup_rate)
	_ = linker.DefineFunc(store, "env", "xqd_erl_ratecounter_lookup_count", i.xqd_erl_ratecounter_lookup_count)
	_ = linker.DefineFunc(store, "env", "xqd_erl_penaltybox_add", i.xqd_erl_penaltybox_add)
	_ = linker.DefineFunc(store, "env", "xqd_erl_penaltybox_has", i.xqd_erl_penaltybox_has)

	// xqd_compute_runtime.go
	_ = linker.DefineFunc(store, "env", "xqd_compute_runtime_get_vcpu_ms", i.xqd_compute_runtime_get_vcpu_ms)

	// xqd_async_io.go
	_ = linker.DefineFunc(store, "env", "xqd_async_io_select", i.xqd_async_io_select)
	_ = linker.DefineFunc(store, "env", "xqd_async_io_is_ready", i.xqd_async_io_is_ready)

	// xqd_http_downstream.go
	_ = linker.DefineFunc(store, "env", "xqd_http_downstream_next_request", i.xqd_http_downstream_next_request)
	_ = linker.DefineFunc(store, "env", "xqd_http_downstream_next_request_wait", i.xqd_http_downstream_next_request_wait)
	_ = linker.DefineFunc(store, "env", "xqd_http_downstream_next_request_abandon", i.xqd_http_downstream_next_request_abandon)
	_ = linker.DefineFunc(store, "env", "xqd_http_downstream_original_header_names", i.xqd_http_downstream_original_header_names)
	_ = linker.DefineFunc(store, "env", "xqd_http_downstream_original_header_count", i.xqd_http_downstream_original_header_count)
	_ = linker.DefineFunc(store, "env", "xqd_req_downstream_tls_cipher_openssl_name", i.xqd_http_downstream_tls_cipher_openssl_name)
	_ = linker.DefineFunc(store, "env", "xqd_req_downstream_tls_protocol", i.xqd_http_downstream_tls_protocol)
	_ = linker.DefineFunc(store, "env", "xqd_req_downstream_tls_client_servername", i.xqd_http_downstream_tls_client_servername)
	_ = linker.DefineFunc(store, "env", "xqd_req_downstream_tls_client_hello", i.xqd_http_downstream_tls_client_hello)
	_ = linker.DefineFunc(store, "env", "xqd_req_downstream_tls_raw_client_certificate", i.xqd_http_downstream_tls_raw_client_certificate)
	_ = linker.DefineFunc(store, "env", "xqd_req_downstream_tls_client_cert_verify_result", i.xqd_http_downstream_tls_client_cert_verify_result)
	_ = linker.DefineFunc(store, "env", "xqd_req_downstream_client_h2_fingerprint", i.xqd_http_downstream_client_h2_fingerprint)
	_ = linker.DefineFunc(store, "env", "xqd_req_downstream_client_oh_fingerprint", i.xqd_http_downstream_client_oh_fingerprint)
	_ = linker.DefineFunc(store, "env", "xqd_req_downstream_tls_ja3_md5", i.xqd_http_downstream_tls_ja3_md5)
	_ = linker.DefineFunc(store, "env", "xqd_req_downstream_tls_ja4", i.xqd_http_downstream_tls_ja4)
}

package fastlike

import (
	"fmt"

	"github.com/bytecodealliance/wasmtime-go/v37"
)

type wasmContext struct {
	engine *wasmtime.Engine
	module *wasmtime.Module
}

func (i *Instance) compile(wasmbytes []byte) {
	config := wasmtime.NewConfig()

	check(config.CacheConfigLoadDefault())
	// Temporarily disable epoch interruption to debug the issue
	// config.SetEpochInterruption(true)

	engine := wasmtime.NewEngineWithConfig(config)
	module, err := wasmtime.NewModule(engine, wasmbytes)
	check(err)

	i.wasmctx = &wasmContext{
		engine: engine,
		module: module,
	}
}

func (i *Instance) link(store *wasmtime.Store, linker *wasmtime.Linker) {
	// XQD Stubbing -{{{
	// TODO: All of these XQD methods are stubbed. As they are implemented, they'll be removed from
	// here and explicitly linked in the section below.
	// Note: In wasmtime-go v37, we must wrap instance methods in closures to avoid nil pointer issues
	_ = linker.FuncWrap("fastly_http_req", "original_header_count", func(a int32) int32 {
		return i.wasm1("original_header_count")(a)
	})

	_ = linker.FuncWrap("fastly_http_resp", "header_value_get", i.wasm6("header_value_get"))
	_ = linker.FuncWrap("fastly_http_resp", "header_remove", i.wasm3("header_remove"))

	// End XQD Stubbing -}}}

	// xqd_http_cache.go
	_ = linker.FuncWrap("fastly_http_cache", "is_request_cacheable", func(req_handle int32) int32 {
		return i.xqd_http_cache_is_request_cacheable(req_handle)
	})
	_ = linker.FuncWrap("fastly_http_cache", "get_suggested_cache_key", func(req_handle int32, key_out int32, key_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_cache_get_suggested_cache_key(req_handle, key_out, key_max_len, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "lookup", func(req_handle int32, options_mask uint32, options int32, cache_handle_out int32) int32 {
		return i.xqd_http_cache_lookup(req_handle, options_mask, options, cache_handle_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "transaction_lookup", func(req_handle int32, options_mask uint32, options int32, cache_handle_out int32) int32 {
		return i.xqd_http_cache_transaction_lookup(req_handle, options_mask, options, cache_handle_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "transaction_insert", func(cache_handle int32, resp_handle int32, options_mask uint32, options int32, body_handle_out int32) int32 {
		return i.xqd_http_cache_transaction_insert(cache_handle, resp_handle, options_mask, options, body_handle_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "transaction_insert_and_stream_back", func(cache_handle int32, resp_handle int32, options_mask uint32, options int32, body_handle_out int32, cache_handle_out int32) int32 {
		return i.xqd_http_cache_transaction_insert_and_stream_back(cache_handle, resp_handle, options_mask, options, body_handle_out, cache_handle_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "transaction_update", func(cache_handle int32, resp_handle int32, options_mask uint32, options int32) int32 {
		return i.xqd_http_cache_transaction_update(cache_handle, resp_handle, options_mask, options)
	})
	_ = linker.FuncWrap("fastly_http_cache", "transaction_update_and_return_fresh", func(cache_handle int32, resp_handle int32, options_mask uint32, options int32, cache_handle_out int32) int32 {
		return i.xqd_http_cache_transaction_update_and_return_fresh(cache_handle, resp_handle, options_mask, options, cache_handle_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "transaction_record_not_cacheable", func(cache_handle int32, options_mask uint32, options int32) int32 {
		return i.xqd_http_cache_transaction_record_not_cacheable(cache_handle, options_mask, options)
	})
	_ = linker.FuncWrap("fastly_http_cache", "transaction_abandon", func(cache_handle int32) int32 {
		return i.xqd_http_cache_transaction_abandon(cache_handle)
	})
	_ = linker.FuncWrap("fastly_http_cache", "close", func(cache_handle int32) int32 {
		return i.xqd_http_cache_close(cache_handle)
	})
	_ = linker.FuncWrap("fastly_http_cache", "get_suggested_backend_request", func(resp_handle int32, req_handle_out int32) int32 {
		return i.xqd_http_cache_get_suggested_backend_request(resp_handle, req_handle_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "get_suggested_cache_options", func(cache_handle int32, resp_handle int32, requested_mask uint32, requested_options int32, options_mask_out int32, options_out int32) int32 {
		return i.xqd_http_cache_get_suggested_cache_options(cache_handle, resp_handle, requested_mask, requested_options, options_mask_out, options_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "prepare_response_for_storage", func(cache_handle int32, resp_handle int32, storage_action_out int32, resp_handle_out int32) int32 {
		return i.xqd_http_cache_prepare_response_for_storage(cache_handle, resp_handle, storage_action_out, resp_handle_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "get_found_response", func(cache_handle int32, transform_for_client uint32, resp_handle_out int32, body_handle_out int32) int32 {
		return i.xqd_http_cache_get_found_response(cache_handle, transform_for_client, resp_handle_out, body_handle_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "get_state", func(cache_handle int32, state_out int32) int32 {
		return i.xqd_http_cache_get_state(cache_handle, state_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "get_length", func(cache_handle int32, length_out int32) int32 {
		return i.xqd_http_cache_get_length(cache_handle, length_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "get_max_age_ns", func(cache_handle int32, max_age_ns_out int32) int32 {
		return i.xqd_http_cache_get_max_age_ns(cache_handle, max_age_ns_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "get_stale_while_revalidate_ns", func(cache_handle int32, stale_while_revalidate_ns_out int32) int32 {
		return i.xqd_http_cache_get_stale_while_revalidate_ns(cache_handle, stale_while_revalidate_ns_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "get_age_ns", func(cache_handle int32, age_ns_out int32) int32 {
		return i.xqd_http_cache_get_age_ns(cache_handle, age_ns_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "get_hits", func(cache_handle int32, hits_out int32) int32 {
		return i.xqd_http_cache_get_hits(cache_handle, hits_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "get_sensitive_data", func(cache_handle int32, sensitive_data_out int32) int32 {
		return i.xqd_http_cache_get_sensitive_data(cache_handle, sensitive_data_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "get_surrogate_keys", func(cache_handle int32, surrogate_keys_out int32, surrogate_keys_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_cache_get_surrogate_keys(cache_handle, surrogate_keys_out, surrogate_keys_max_len, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_cache", "get_vary_rule", func(cache_handle int32, vary_rule_out int32, vary_rule_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_cache_get_vary_rule(cache_handle, vary_rule_out, vary_rule_max_len, nwritten_out)
	})

	// xqd.go
	// Use FuncWrap for v37 compatibility (doesn't require store parameter)
	err := linker.FuncWrap("fastly_abi", "init", func(abiv int64) int32 {
		return i.xqd_init(abiv)
	})
	if err != nil {
		panic(fmt.Sprintf("Failed to define fastly_abi::init: %v", err))
	}

	err = linker.FuncWrap("fastly_uap", "parse", i.xqd_uap_parse)
	if err != nil {
		panic(fmt.Sprintf("Failed to define fastly_uap::parse: %v", err))
	}

	// xqd_request.go
	// Use FuncWrap for all functions for v37 compatibility
	_ = linker.FuncWrap("fastly_http_req", "body_downstream_get", func(request_handle_out int32, body_handle_out int32) int32 {
		if i.memory == nil || i.ds_request == nil {
			// Return stub values if called during initialization
			return XqdErrUnsupported
		}
		return i.xqd_req_body_downstream_get(request_handle_out, body_handle_out)
	})
	_ = linker.FuncWrap("fastly_http_req", "downstream_client_ip_addr", func(octets_out int32, nwritten_out int32) int32 {
		if i.memory == nil || i.ds_request == nil {
			return XqdStatusOK // Return OK with no data written
		}
		return i.xqd_req_downstream_client_ip_addr(octets_out, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_req", "new", func(handle_out int32) int32 {
		if i == nil {
			panic("Instance is nil in xqd_req_new")
		}
		if i.memory == nil {
			// It's OK for memory to be nil during initial setup for req_new
			// Just create a request handle without writing to memory
			rhid, _ := i.requests.New()
			// We can't write to memory yet, but return success
			return int32(rhid)
		}
		return i.xqd_req_new(handle_out)
	})
	_ = linker.FuncWrap("fastly_http_req", "version_get", func(handle int32, version_out int32) int32 {
		if i == nil || i.memory == nil {
			return XqdErrUnsupported
		}
		return i.xqd_req_version_get(handle, version_out)
	})
	_ = linker.FuncWrap("fastly_http_req", "version_set", func(handle int32, version int32) int32 {
		if i == nil {
			return XqdErrUnsupported
		}
		return i.xqd_req_version_set(handle, version)
	})
	_ = linker.FuncWrap("fastly_http_req", "method_get", i.xqd_req_method_get)
	_ = linker.FuncWrap("fastly_http_req", "method_set", i.xqd_req_method_set)
	_ = linker.FuncWrap("fastly_http_req", "uri_get", i.xqd_req_uri_get)
	_ = linker.FuncWrap("fastly_http_req", "uri_set", i.xqd_req_uri_set)
	_ = linker.FuncWrap("fastly_http_req", "header_names_get", i.xqd_req_header_names_get)
	_ = linker.FuncWrap("fastly_http_req", "header_remove", i.xqd_req_header_remove)
	_ = linker.FuncWrap("fastly_http_req", "header_insert", i.xqd_req_header_insert)
	_ = linker.FuncWrap("fastly_http_req", "header_append", i.xqd_req_header_append)
	_ = linker.FuncWrap("fastly_http_req", "header_value_get", i.xqd_req_header_value_get)
	_ = linker.FuncWrap("fastly_http_req", "header_values_get", i.xqd_req_header_values_get)
	_ = linker.FuncWrap("fastly_http_req", "header_values_set", i.xqd_req_header_values_set)
	_ = linker.FuncWrap("fastly_http_req", "send", i.xqd_req_send)
	_ = linker.FuncWrap("fastly_http_req", "send_v2", i.xqd_req_send_v2)
	_ = linker.FuncWrap("fastly_http_req", "send_v3", i.xqd_req_send_v3)
	_ = linker.FuncWrap("fastly_http_req", "send_async", i.xqd_req_send_async)
	_ = linker.FuncWrap("fastly_http_req", "send_async_streaming", i.xqd_req_send_async_streaming)
	_ = linker.FuncWrap("fastly_http_req", "send_async_v2", i.xqd_req_send_async_v2)
	_ = linker.FuncWrap("fastly_http_req", "pending_req_poll", i.xqd_pending_req_poll)
	_ = linker.FuncWrap("fastly_http_req", "pending_req_poll_v2", i.xqd_pending_req_poll_v2)
	_ = linker.FuncWrap("fastly_http_req", "pending_req_wait", i.xqd_pending_req_wait)
	_ = linker.FuncWrap("fastly_http_req", "pending_req_wait_v2", i.xqd_pending_req_wait_v2)
	_ = linker.FuncWrap("fastly_http_req", "pending_req_select", i.xqd_pending_req_select)
	_ = linker.FuncWrap("fastly_http_req", "pending_req_select_v2", i.xqd_pending_req_select_v2)
	_ = linker.FuncWrap("fastly_http_req", "cache_override_set", i.xqd_req_cache_override_set)
	_ = linker.FuncWrap("fastly_http_req", "cache_override_v2_set", i.xqd_req_cache_override_v2_set)
	// The Go http implementation doesn't make it easy to get at the original headers in order, so
	// we just use the same sorted order
	_ = linker.FuncWrap("fastly_http_req", "original_header_names_get", i.xqd_req_header_names_get)
	// Try using FuncWrap instead of DefineFunc for v37 compatibility
	_ = linker.FuncWrap("fastly_http_req", "close", func(handle int32) int32 {
		if i == nil {
			panic("Instance is nil in close")
		}
		return i.xqd_req_close(handle)
	})
	_ = linker.FuncWrap("fastly_http_req", "downstream_client_ddos_detected", i.xqd_req_downstream_client_ddos_detected)
	_ = linker.FuncWrap("fastly_http_req", "fastly_key_is_valid", i.xqd_req_fastly_key_is_valid)
	_ = linker.FuncWrap("fastly_http_req", "downstream_compliance_region", i.xqd_req_downstream_compliance_region)
	_ = linker.FuncWrap("fastly_http_req", "on_behalf_of", i.xqd_req_on_behalf_of)
	_ = linker.FuncWrap("fastly_http_req", "framing_headers_mode_set", i.xqd_req_framing_headers_mode_set)
	_ = linker.FuncWrap("fastly_http_req", "auto_decompress_response_set", i.xqd_req_auto_decompress_response_set)
	_ = linker.FuncWrap("fastly_http_req", "register_dynamic_backend", i.xqd_req_register_dynamic_backend)
	// DEPRECATED: use fastly_http_downstream versions
	_ = linker.FuncWrap("fastly_http_req", "downstream_client_h2_fingerprint", i.xqd_http_downstream_client_h2_fingerprint)
	_ = linker.FuncWrap("fastly_http_req", "downstream_client_oh_fingerprint", i.xqd_http_downstream_client_oh_fingerprint)
	_ = linker.FuncWrap("fastly_http_req", "downstream_tls_ja3_md5", i.xqd_http_downstream_tls_ja3_md5)
	_ = linker.FuncWrap("fastly_http_req", "downstream_tls_ja4", i.xqd_http_downstream_tls_ja4)

	// xqd_response.go
	_ = linker.FuncWrap("fastly_http_resp", "send_downstream", func(resp_handle int32, body_handle int32, streaming int32) int32 {
		return i.xqd_resp_send_downstream(resp_handle, body_handle, streaming)
	})
	_ = linker.FuncWrap("fastly_http_resp", "new", func(handle_out int32) int32 {
		return i.xqd_resp_new(handle_out)
	})
	_ = linker.FuncWrap("fastly_http_resp", "status_get", func(handle int32, status_out int32) int32 {
		return i.xqd_resp_status_get(handle, status_out)
	})
	_ = linker.FuncWrap("fastly_http_resp", "status_set", func(handle int32, status int32) int32 {
		return i.xqd_resp_status_set(handle, status)
	})
	_ = linker.FuncWrap("fastly_http_resp", "version_get", func(handle int32, version_out int32) int32 {
		return i.xqd_resp_version_get(handle, version_out)
	})
	_ = linker.FuncWrap("fastly_http_resp", "version_set", func(handle int32, version int32) int32 {
		return i.xqd_resp_version_set(handle, version)
	})
	_ = linker.FuncWrap("fastly_http_resp", "header_names_get", func(handle int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
		return i.xqd_resp_header_names_get(handle, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_resp", "header_remove", func(handle int32, name_addr int32, name_size int32) int32 {
		return i.xqd_resp_header_remove(handle, name_addr, name_size)
	})
	_ = linker.FuncWrap("fastly_http_resp", "header_insert", func(handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
		return i.xqd_resp_header_insert(handle, name_addr, name_size, value_addr, value_size)
	})
	_ = linker.FuncWrap("fastly_http_resp", "header_append", func(handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
		return i.xqd_resp_header_append(handle, name_addr, name_size, value_addr, value_size)
	})
	_ = linker.FuncWrap("fastly_http_resp", "header_values_get", func(handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
		return i.xqd_resp_header_values_get(handle, name_addr, name_size, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_resp", "header_values_set", func(handle int32, name_addr int32, name_size int32, values_addr int32, values_size int32) int32 {
		return i.xqd_resp_header_values_set(handle, name_addr, name_size, values_addr, values_size)
	})
	_ = linker.FuncWrap("fastly_http_resp", "close", func(handle int32) int32 {
		return i.xqd_resp_close(handle)
	})
	_ = linker.FuncWrap("fastly_http_resp", "framing_headers_mode_set", func(resp_handle int32, mode int32) int32 {
		return i.xqd_resp_framing_headers_mode_set(resp_handle, mode)
	})
	_ = linker.FuncWrap("fastly_http_resp", "http_keepalive_mode_set", func(resp_handle int32, mode int32) int32 {
		return i.xqd_resp_http_keepalive_mode_set(resp_handle, mode)
	})
	_ = linker.FuncWrap("fastly_http_resp", "get_addr_dest_ip", func(handle int32, addr int32, nwritten_out int32) int32 {
		return i.xqd_resp_get_addr_dest_ip(handle, addr, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_resp", "get_addr_dest_port", func(handle int32, port_out int32) int32 {
		return i.xqd_resp_get_addr_dest_port(handle, port_out)
	})

	// xqd_body.go
	_ = linker.FuncWrap("fastly_http_body", "new", func(handle_out int32) int32 {
		return i.xqd_body_new(handle_out)
	})
	_ = linker.FuncWrap("fastly_http_body", "write", func(body_handle int32, addr int32, size int32, end int32, nwritten_out int32) int32 {
		return i.xqd_body_write(body_handle, addr, size, end, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_body", "read", func(body_handle int32, addr int32, size int32, nread_out int32) int32 {
		return i.xqd_body_read(body_handle, addr, size, nread_out)
	})
	_ = linker.FuncWrap("fastly_http_body", "append", func(dst int32, src int32) int32 {
		return i.xqd_body_append(dst, src)
	})
	_ = linker.FuncWrap("fastly_http_body", "close", func(body_handle int32) int32 {
		return i.xqd_body_close(body_handle)
	})
	_ = linker.FuncWrap("fastly_http_body", "abandon", func(body_handle int32) int32 {
		return i.xqd_body_abandon(body_handle)
	})
	_ = linker.FuncWrap("fastly_http_body", "known_length", func(body_handle int32, known_length_out int32) int32 {
		return i.xqd_body_known_length(body_handle, known_length_out)
	})
	_ = linker.FuncWrap("fastly_http_body", "trailer_append", func(body_handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
		return i.xqd_body_trailer_append(body_handle, name_addr, name_size, value_addr, value_size)
	})
	_ = linker.FuncWrap("fastly_http_body", "trailer_names_get", func(body_handle int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
		return i.xqd_body_trailer_names_get(body_handle, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_body", "trailer_value_get", func(body_handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32, nwritten_out int32) int32 {
		return i.xqd_body_trailer_value_get(body_handle, name_addr, name_size, value_addr, value_size, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_body", "trailer_values_get", func(body_handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
		return i.xqd_body_trailer_values_get(body_handle, name_addr, name_size, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
	})

	// xqd_log.go
	_ = linker.FuncWrap("fastly_log", "endpoint_get", func(name_addr int32, name_size int32, endpoint_handle_out int32) int32 {
		return i.xqd_log_endpoint_get(name_addr, name_size, endpoint_handle_out)
	})
	_ = linker.FuncWrap("fastly_log", "write", func(endpoint_handle int32, addr int32, size int32, nwritten_out int32) int32 {
		return i.xqd_log_write(endpoint_handle, addr, size, nwritten_out)
	})

	// xqd_dictionary.go
	_ = linker.FuncWrap("fastly_dictionary", "open", func(name_addr int32, name_size int32, dict_handle_out int32) int32 {
		return i.xqd_dictionary_open(name_addr, name_size, dict_handle_out)
	})
	_ = linker.FuncWrap("fastly_dictionary", "get", func(dict_handle int32, key_addr int32, key_size int32, value_addr int32, value_size int32, nwritten_out int32) int32 {
		return i.xqd_dictionary_get(dict_handle, key_addr, key_size, value_addr, value_size, nwritten_out)
	})

	// xqd_config_store.go
	_ = linker.FuncWrap("fastly_config_store", "open", func(name_addr int32, name_size int32, store_handle_out int32) int32 {
		return i.xqd_config_store_open(name_addr, name_size, store_handle_out)
	})
	_ = linker.FuncWrap("fastly_config_store", "get", func(store_handle int32, key_addr int32, key_size int32, value_addr int32, value_size int32, nwritten_out int32) int32 {
		return i.xqd_config_store_get(store_handle, key_addr, key_size, value_addr, value_size, nwritten_out)
	})

	// xqd_secret_store.go
	_ = linker.FuncWrap("fastly_secret_store", "open", func(name_addr int32, name_size int32, store_handle_out int32) int32 {
		return i.xqd_secret_store_open(name_addr, name_size, store_handle_out)
	})
	_ = linker.FuncWrap("fastly_secret_store", "get", func(store_handle int32, key_addr int32, key_size int32, secret_handle_out int32) int32 {
		return i.xqd_secret_store_get(store_handle, key_addr, key_size, secret_handle_out)
	})
	_ = linker.FuncWrap("fastly_secret_store", "plaintext", func(secret_handle int32, value_addr int32, value_size int32, nwritten_out int32) int32 {
		return i.xqd_secret_store_plaintext(secret_handle, value_addr, value_size, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_secret_store", "from_bytes", func(value_addr int32, value_size int32, secret_handle_out int32) int32 {
		return i.xqd_secret_store_from_bytes(value_addr, value_size, secret_handle_out)
	})

	// xqd_device_detection.go
	_ = linker.FuncWrap("fastly_device_detection", "lookup", func(user_agent_addr int32, user_agent_size int32, buf int32, buf_len int32, nwritten_out int32) int32 {
		return i.xqd_device_detection_lookup(user_agent_addr, user_agent_size, buf, buf_len, nwritten_out)
	})

	// xqd_image_optimizer.go
	_ = linker.FuncWrap("fastly_image_optimizer", "transform_image_optimizer_request", func(originImageRequest int32, originImageRequestBody int32, originImageRequestBackendPtr int32, originImageRequestBackendLen int32, ioTransformConfigMask uint32, ioTransformConfigPtr int32, ioErrorDetailPtr int32, respHandleOut int32, bodyHandleOut int32) int32 {
		return i.xqd_image_optimizer_transform_request(originImageRequest, originImageRequestBody, originImageRequestBackendPtr, originImageRequestBackendLen, ioTransformConfigMask, ioTransformConfigPtr, ioErrorDetailPtr, respHandleOut, bodyHandleOut)
	})

	// xqd_acl.go
	_ = linker.FuncWrap("fastly_acl", "open", func(name_addr int32, name_size int32, acl_handle_out int32) int32 {
		return i.xqd_acl_open(name_addr, name_size, acl_handle_out)
	})
	_ = linker.FuncWrap("fastly_acl", "lookup", func(acl_handle int32, ip_addr int32, ip_size int32, body_addr int32, body_size int32) int32 {
		return i.xqd_acl_lookup(acl_handle, ip_addr, ip_size, body_addr, body_size)
	})

	// xqd_erl.go
	_ = linker.FuncWrap("fastly_erl", "check_rate", func(rc_addr int32, rc_size int32, entry_addr int32, entry_size int32, delta uint32, window uint32, limit uint32, pb_addr int32, pb_size int32, ttl uint32, blocked_out int32) int32 {
		return i.xqd_erl_check_rate(rc_addr, rc_size, entry_addr, entry_size, delta, window, limit, pb_addr, pb_size, ttl, blocked_out)
	})
	_ = linker.FuncWrap("fastly_erl", "ratecounter_increment", func(rc_addr int32, rc_size int32, entry_addr int32, entry_size int32, delta uint32) int32 {
		return i.xqd_erl_ratecounter_increment(rc_addr, rc_size, entry_addr, entry_size, delta)
	})
	_ = linker.FuncWrap("fastly_erl", "ratecounter_lookup_rate", func(rc_addr int32, rc_size int32, entry_addr int32, entry_size int32, window uint32, rate_out int32) int32 {
		return i.xqd_erl_ratecounter_lookup_rate(rc_addr, rc_size, entry_addr, entry_size, window, rate_out)
	})
	_ = linker.FuncWrap("fastly_erl", "ratecounter_lookup_count", func(rc_addr int32, rc_size int32, entry_addr int32, entry_size int32, duration uint32, count_out int32) int32 {
		return i.xqd_erl_ratecounter_lookup_count(rc_addr, rc_size, entry_addr, entry_size, duration, count_out)
	})
	_ = linker.FuncWrap("fastly_erl", "penaltybox_add", func(pb_addr int32, pb_size int32, entry_addr int32, entry_size int32, ttl uint32) int32 {
		return i.xqd_erl_penaltybox_add(pb_addr, pb_size, entry_addr, entry_size, ttl)
	})
	_ = linker.FuncWrap("fastly_erl", "penaltybox_has", func(pb_addr int32, pb_size int32, entry_addr int32, entry_size int32, has_out int32) int32 {
		return i.xqd_erl_penaltybox_has(pb_addr, pb_size, entry_addr, entry_size, has_out)
	})

	// xqd_kv_store.go
	_ = linker.FuncWrap("fastly_kv_store", "open", func(name_addr int32, name_size int32, store_handle_out int32) int32 {
		return i.xqd_kv_store_open(name_addr, name_size, store_handle_out)
	})
	_ = linker.FuncWrap("fastly_kv_store", "lookup", func(store_handle int32, key_addr int32, key_size int32, lookup_handle_out int32) int32 {
		return i.xqd_kv_store_lookup(store_handle, key_addr, key_size, lookup_handle_out)
	})
	_ = linker.FuncWrap("fastly_kv_store", "lookup_wait", func(lookup_handle int32, body_handle_out int32, metadata_out int32, metadata_max_len int32, metadata_len_out int32, generation_out int32, content_type_out int32, content_type_max_len int32, content_type_len_out int32) int32 {
		return i.xqd_kv_store_lookup_wait(lookup_handle, body_handle_out, metadata_out, metadata_max_len, metadata_len_out, generation_out, content_type_out, content_type_max_len, content_type_len_out)
	})
	_ = linker.FuncWrap("fastly_kv_store", "lookup_wait_v2", func(lookup_handle int32, body_handle_out int32, metadata_out int32, metadata_max_len int32, metadata_len_out int32, generation_out int32) int32 {
		return i.xqd_kv_store_lookup_wait_v2(lookup_handle, body_handle_out, metadata_out, metadata_max_len, metadata_len_out, generation_out)
	})
	_ = linker.FuncWrap("fastly_kv_store", "insert", func(store_handle int32, key_addr int32, key_size int32, body_handle int32, metadata_addr int32, metadata_size int32, insert_mode int32, insert_config_mask uint32, insert_config_buf int32, insert_handle_out int32) int32 {
		return i.xqd_kv_store_insert(store_handle, key_addr, key_size, body_handle, metadata_addr, metadata_size, insert_mode, insert_config_mask, insert_config_buf, insert_handle_out)
	})
	_ = linker.FuncWrap("fastly_kv_store", "insert_wait", func(insert_handle int32, generation_out int32) int32 {
		return i.xqd_kv_store_insert_wait(insert_handle, generation_out)
	})
	_ = linker.FuncWrap("fastly_kv_store", "delete", func(store_handle int32, key_addr int32, key_size int32, delete_handle_out int32) int32 {
		return i.xqd_kv_store_delete(store_handle, key_addr, key_size, delete_handle_out)
	})
	_ = linker.FuncWrap("fastly_kv_store", "delete_wait", func(delete_handle int32) int32 {
		return i.xqd_kv_store_delete_wait(delete_handle)
	})
	_ = linker.FuncWrap("fastly_kv_store", "list", func(store_handle int32, list_config_mask uint32, list_config_buf int32, list_handle_out int32) int32 {
		return i.xqd_kv_store_list(store_handle, list_config_mask, list_config_buf, list_handle_out)
	})
	_ = linker.FuncWrap("fastly_kv_store", "list_wait", func(list_handle int32, body_handle_out int32, metadata_out int32, metadata_max_len int32, metadata_len_out int32) int32 {
		return i.xqd_kv_store_list_wait(list_handle, body_handle_out, metadata_out, metadata_max_len, metadata_len_out)
	})

	// xqd_backend.go
	_ = linker.FuncWrap("fastly_backend", "exists", func(backend_addr int32, backend_size int32, exists_out int32) int32 {
		return i.xqd_backend_exists(backend_addr, backend_size, exists_out)
	})
	_ = linker.FuncWrap("fastly_backend", "is_healthy", func(backend_addr int32, backend_size int32, health_out int32) int32 {
		return i.xqd_backend_is_healthy(backend_addr, backend_size, health_out)
	})
	_ = linker.FuncWrap("fastly_backend", "is_dynamic", func(backend_addr int32, backend_size int32, is_dynamic_out int32) int32 {
		return i.xqd_backend_is_dynamic(backend_addr, backend_size, is_dynamic_out)
	})
	_ = linker.FuncWrap("fastly_backend", "get_host", func(backend_addr int32, backend_size int32, value_out int32, value_max_len int32, nwritten_out int32) int32 {
		return i.xqd_backend_get_host(backend_addr, backend_size, value_out, value_max_len, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_backend", "get_override_host", func(backend_addr int32, backend_size int32, value_out int32, value_max_len int32, nwritten_out int32) int32 {
		return i.xqd_backend_get_override_host(backend_addr, backend_size, value_out, value_max_len, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_backend", "get_port", func(backend_addr int32, backend_size int32, port_out int32) int32 {
		return i.xqd_backend_get_port(backend_addr, backend_size, port_out)
	})
	_ = linker.FuncWrap("fastly_backend", "get_connect_timeout_ms", func(backend_addr int32, backend_size int32, timeout_out int32) int32 {
		return i.xqd_backend_get_connect_timeout_ms(backend_addr, backend_size, timeout_out)
	})
	_ = linker.FuncWrap("fastly_backend", "get_first_byte_timeout_ms", func(backend_addr int32, backend_size int32, timeout_out int32) int32 {
		return i.xqd_backend_get_first_byte_timeout_ms(backend_addr, backend_size, timeout_out)
	})
	_ = linker.FuncWrap("fastly_backend", "get_between_bytes_timeout_ms", func(backend_addr int32, backend_size int32, timeout_out int32) int32 {
		return i.xqd_backend_get_between_bytes_timeout_ms(backend_addr, backend_size, timeout_out)
	})
	_ = linker.FuncWrap("fastly_backend", "is_ssl", func(backend_addr int32, backend_size int32, is_ssl_out int32) int32 {
		return i.xqd_backend_is_ssl(backend_addr, backend_size, is_ssl_out)
	})
	_ = linker.FuncWrap("fastly_backend", "get_ssl_min_version", func(backend_addr int32, backend_size int32, version_out int32) int32 {
		return i.xqd_backend_get_ssl_min_version(backend_addr, backend_size, version_out)
	})
	_ = linker.FuncWrap("fastly_backend", "get_ssl_max_version", func(backend_addr int32, backend_size int32, version_out int32) int32 {
		return i.xqd_backend_get_ssl_max_version(backend_addr, backend_size, version_out)
	})
	_ = linker.FuncWrap("fastly_backend", "get_http_keepalive_time", func(backend_addr int32, backend_size int32, timeout_out int32) int32 {
		return i.xqd_backend_get_http_keepalive_time(backend_addr, backend_size, timeout_out)
	})
	_ = linker.FuncWrap("fastly_backend", "get_tcp_keepalive_enable", func(backend_addr int32, backend_size int32, is_enabled_out int32) int32 {
		return i.xqd_backend_get_tcp_keepalive_enable(backend_addr, backend_size, is_enabled_out)
	})
	_ = linker.FuncWrap("fastly_backend", "get_tcp_keepalive_interval", func(backend_addr int32, backend_size int32, timeout_out int32) int32 {
		return i.xqd_backend_get_tcp_keepalive_interval(backend_addr, backend_size, timeout_out)
	})
	_ = linker.FuncWrap("fastly_backend", "get_tcp_keepalive_probes", func(backend_addr int32, backend_size int32, probes_out int32) int32 {
		return i.xqd_backend_get_tcp_keepalive_probes(backend_addr, backend_size, probes_out)
	})
	_ = linker.FuncWrap("fastly_backend", "get_tcp_keepalive_time", func(backend_addr int32, backend_size int32, timeout_out int32) int32 {
		return i.xqd_backend_get_tcp_keepalive_time(backend_addr, backend_size, timeout_out)
	})

	// xqd_compute_runtime.go
	_ = linker.FuncWrap("fastly_compute_runtime", "get_vcpu_ms", func(vcpu_time_ms_out int32) int32 {
		return i.xqd_compute_runtime_get_vcpu_ms(vcpu_time_ms_out)
	})

	// xqd_cache.go
	_ = linker.FuncWrap("fastly_cache", "lookup", func(cache_key int32, cache_key_len int32, options_mask uint32, options int32, handle_out int32) int32 {
		return i.xqd_cache_lookup(cache_key, cache_key_len, options_mask, options, handle_out)
	})
	_ = linker.FuncWrap("fastly_cache", "insert", func(cache_key int32, cache_key_len int32, options_mask uint32, options int32, body_handle_out int32) int32 {
		return i.xqd_cache_insert(cache_key, cache_key_len, options_mask, options, body_handle_out)
	})
	_ = linker.FuncWrap("fastly_cache", "transaction_lookup", func(cache_key int32, cache_key_len int32, options_mask uint32, options int32, cache_handle_out int32) int32 {
		return i.xqd_cache_transaction_lookup(cache_key, cache_key_len, options_mask, options, cache_handle_out)
	})
	_ = linker.FuncWrap("fastly_cache", "transaction_lookup_async", func(cache_key int32, cache_key_len int32, options_mask uint32, options int32, cache_busy_handle_out int32) int32 {
		return i.xqd_cache_transaction_lookup_async(cache_key, cache_key_len, options_mask, options, cache_busy_handle_out)
	})
	_ = linker.FuncWrap("fastly_cache", "cache_busy_handle_wait", func(busy_handle int32, cache_handle_out int32) int32 {
		return i.xqd_cache_busy_handle_wait(busy_handle, cache_handle_out)
	})
	_ = linker.FuncWrap("fastly_cache", "transaction_insert", func(cache_handle int32, options_mask uint32, options int32, body_handle_out int32) int32 {
		return i.xqd_cache_transaction_insert(cache_handle, options_mask, options, body_handle_out)
	})
	_ = linker.FuncWrap("fastly_cache", "transaction_insert_and_stream_back", func(cache_handle int32, options_mask uint32, options int32, body_handle_out int32, cache_handle_out int32) int32 {
		return i.xqd_cache_transaction_insert_and_stream_back(cache_handle, options_mask, options, body_handle_out, cache_handle_out)
	})
	_ = linker.FuncWrap("fastly_cache", "transaction_update", func(cache_handle int32, options_mask uint32, options int32) int32 {
		return i.xqd_cache_transaction_update(cache_handle, options_mask, options)
	})
	_ = linker.FuncWrap("fastly_cache", "transaction_cancel", func(cache_handle int32) int32 {
		return i.xqd_cache_transaction_cancel(cache_handle)
	})
	_ = linker.FuncWrap("fastly_cache", "close_busy", func(busy_handle int32) int32 {
		return i.xqd_cache_close_busy(busy_handle)
	})
	_ = linker.FuncWrap("fastly_cache", "close", func(cache_handle int32) int32 {
		return i.xqd_cache_close(cache_handle)
	})
	_ = linker.FuncWrap("fastly_cache", "get_state", func(cache_handle int32, state_out int32) int32 {
		return i.xqd_cache_get_state(cache_handle, state_out)
	})
	_ = linker.FuncWrap("fastly_cache", "get_user_metadata", func(cache_handle int32, user_metadata_out int32, user_metadata_out_len int32, nwritten int32) int32 {
		return i.xqd_cache_get_user_metadata(cache_handle, user_metadata_out, user_metadata_out_len, nwritten)
	})
	_ = linker.FuncWrap("fastly_cache", "get_body", func(cache_handle int32, options_mask uint32, options int32, body_handle_out int32) int32 {
		return i.xqd_cache_get_body(cache_handle, options_mask, options, body_handle_out)
	})
	_ = linker.FuncWrap("fastly_cache", "get_length", func(cache_handle int32, length_out int32) int32 {
		return i.xqd_cache_get_length(cache_handle, length_out)
	})
	_ = linker.FuncWrap("fastly_cache", "get_max_age_ns", func(cache_handle int32, max_age_ns_out int32) int32 {
		return i.xqd_cache_get_max_age_ns(cache_handle, max_age_ns_out)
	})
	_ = linker.FuncWrap("fastly_cache", "get_stale_while_revalidate_ns", func(cache_handle int32, stale_while_revalidate_ns_out int32) int32 {
		return i.xqd_cache_get_stale_while_revalidate_ns(cache_handle, stale_while_revalidate_ns_out)
	})
	_ = linker.FuncWrap("fastly_cache", "get_age_ns", func(cache_handle int32, age_ns_out int32) int32 {
		return i.xqd_cache_get_age_ns(cache_handle, age_ns_out)
	})
	_ = linker.FuncWrap("fastly_cache", "get_hits", func(cache_handle int32, hits_out int32) int32 {
		return i.xqd_cache_get_hits(cache_handle, hits_out)
	})
	// Cache replace API (stubs - not implemented, returns XqdErrUnsupported like Viceroy)
	_ = linker.FuncWrap("fastly_cache", "replace", func(cache_key int32, cache_key_len int32, options_mask uint32, options int32, replace_handle_out int32) int32 {
		return i.xqd_cache_replace(cache_key, cache_key_len, options_mask, options, replace_handle_out)
	})
	_ = linker.FuncWrap("fastly_cache", "replace_insert", func(replace_handle int32, options_mask uint32, options int32, body_handle_out int32) int32 {
		return i.xqd_cache_replace_insert(replace_handle, options_mask, options, body_handle_out)
	})
	_ = linker.FuncWrap("fastly_cache", "replace_get_age_ns", func(replace_handle int32, age_ns_out int32) int32 {
		return i.xqd_cache_replace_get_age_ns(replace_handle, age_ns_out)
	})
	_ = linker.FuncWrap("fastly_cache", "replace_get_body", func(replace_handle int32, options_mask uint32, options int32, body_handle_out int32) int32 {
		return i.xqd_cache_replace_get_body(replace_handle, options_mask, options, body_handle_out)
	})
	_ = linker.FuncWrap("fastly_cache", "replace_get_hits", func(replace_handle int32, hits_out int32) int32 {
		return i.xqd_cache_replace_get_hits(replace_handle, hits_out)
	})
	_ = linker.FuncWrap("fastly_cache", "replace_get_length", func(replace_handle int32, length_out int32) int32 {
		return i.xqd_cache_replace_get_length(replace_handle, length_out)
	})
	_ = linker.FuncWrap("fastly_cache", "replace_get_max_age_ns", func(replace_handle int32, max_age_ns_out int32) int32 {
		return i.xqd_cache_replace_get_max_age_ns(replace_handle, max_age_ns_out)
	})
	_ = linker.FuncWrap("fastly_cache", "replace_get_stale_while_revalidate_ns", func(replace_handle int32, stale_while_revalidate_ns_out int32) int32 {
		return i.xqd_cache_replace_get_stale_while_revalidate_ns(replace_handle, stale_while_revalidate_ns_out)
	})
	_ = linker.FuncWrap("fastly_cache", "replace_get_state", func(replace_handle int32, state_out int32) int32 {
		return i.xqd_cache_replace_get_state(replace_handle, state_out)
	})
	_ = linker.FuncWrap("fastly_cache", "replace_get_user_metadata", func(replace_handle int32, user_metadata_out int32, user_metadata_out_len int32, nwritten int32) int32 {
		return i.xqd_cache_replace_get_user_metadata(replace_handle, user_metadata_out, user_metadata_out_len, nwritten)
	})

	// xqd_purge.go
	_ = linker.FuncWrap("fastly_purge", "purge_surrogate_key", func(surrogate_key int32, surrogate_key_len int32, options_mask uint32, options int32) int32 {
		return i.xqd_purge_surrogate_key(surrogate_key, surrogate_key_len, options_mask, options)
	})

	// xqd_async_io.go
	_ = linker.FuncWrap("fastly_async_io", "select", func(handles_addr int32, handles_len int32, timeout_ms int32, ready_idx_out int32) int32 {
		return i.xqd_async_io_select(handles_addr, handles_len, timeout_ms, ready_idx_out)
	})
	_ = linker.FuncWrap("fastly_async_io", "is_ready", func(handle int32, is_ready_out int32) int32 {
		return i.xqd_async_io_is_ready(handle, is_ready_out)
	})

	// xqd_http_downstream.go
	_ = linker.FuncWrap("fastly_http_downstream", "next_request", func(options_mask int32, options_ptr int32) int32 {
		return i.xqd_http_downstream_next_request(options_mask, options_ptr)
	})
	_ = linker.FuncWrap("fastly_http_downstream", "next_request_wait", func(promise_handle int32, req_handle_out int32, body_handle_out int32) int32 {
		return i.xqd_http_downstream_next_request_wait(promise_handle, req_handle_out, body_handle_out)
	})
	_ = linker.FuncWrap("fastly_http_downstream", "next_request_abandon", func(promise_handle int32) int32 {
		return i.xqd_http_downstream_next_request_abandon(promise_handle)
	})
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_original_header_names", func(request_handle int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_original_header_names(request_handle, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_original_header_count", func(request_handle int32, count_out int32) int32 {
		return i.xqd_http_downstream_original_header_count(request_handle, count_out)
	})
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_cipher_openssl_name", func(req_handle int32, addr int32, maxlen int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_cipher_openssl_name(req_handle, addr, maxlen, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_protocol", func(req_handle int32, addr int32, maxlen int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_protocol(req_handle, addr, maxlen, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_client_servername", func(req_handle int32, addr int32, maxlen int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_client_servername(req_handle, addr, maxlen, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_client_hello", func(req_handle int32, addr int32, maxlen int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_client_hello(req_handle, addr, maxlen, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_raw_client_certificate", func(addr int32, maxlen int32, nwritten_out int32, cert_count_out int32) int32 {
		return i.xqd_http_downstream_tls_raw_client_certificate(addr, maxlen, nwritten_out, cert_count_out)
	})
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_client_cert_verify_result", func(result_out int32) int32 {
		return i.xqd_http_downstream_tls_client_cert_verify_result(result_out)
	})
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_client_h2_fingerprint", func(req_handle int32, h2_out int32, h2_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_client_h2_fingerprint(req_handle, h2_out, h2_max_len, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_client_oh_fingerprint", func(req_handle int32, oh_out int32, oh_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_client_oh_fingerprint(req_handle, oh_out, oh_max_len, nwritten_out)
	})
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_ja3_md5", func(req_handle int32, ja3_md5_out int32) int32 {
		return i.xqd_http_downstream_tls_ja3_md5(req_handle, ja3_md5_out)
	})
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_ja4", func(req_handle int32, ja4_out int32, ja4_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_ja4(req_handle, ja4_out, ja4_max_len, nwritten_out)
	})
}

// linklegacy links in the abi methods using the legacy method names
func (i *Instance) linklegacy(store *wasmtime.Store, linker *wasmtime.Linker) {
	// XQD Stubbing -{{{
	// TODO: All of these XQD methods are stubbed. As they are implemented, they'll be removed from
	// here and explicitly linked in the section below.
	_ = linker.FuncWrap("env", "xqd_req_original_header_count", i.wasm1("xqd_req_original_header_count"))

	_ = linker.FuncWrap("env", "xqd_resp_header_value_get", i.wasm6("xqd_resp_header_value_get"))

	_ = linker.FuncWrap("env", "xqd_body_close_downstream", i.xqd_body_close)
	// End XQD Stubbing -}}}

	// xqd.go
	_ = linker.FuncWrap("fastly", "init", func(abiv int64) int32 {
		return i.xqd_init(abiv)
	})
	_ = linker.FuncWrap("fastly_uap", "parse", func(user_agent int32, user_agent_len int32, family int32, family_max_len int32, family_written int32, major int32, major_max_len int32, major_written int32, minor int32, minor_max_len int32, minor_written int32, patch int32, patch_max_len int32, patch_written int32) int32 {
		return i.xqd_uap_parse(user_agent, user_agent_len, family, family_max_len, family_written, major, major_max_len, major_written, minor, minor_max_len, minor_written, patch, patch_max_len, patch_written)
	})

	_ = linker.FuncWrap("env", "xqd_req_body_downstream_get", func(request_handle_out int32, body_handle_out int32) int32 {
		return i.xqd_req_body_downstream_get(request_handle_out, body_handle_out)
	})
	_ = linker.FuncWrap("env", "xqd_resp_send_downstream", func(resp_handle int32, body_handle int32, streaming int32) int32 {
		return i.xqd_resp_send_downstream(resp_handle, body_handle, streaming)
	})
	_ = linker.FuncWrap("env", "xqd_req_downstream_client_ip_addr", func(octets_out int32, nwritten_out int32) int32 {
		return i.xqd_req_downstream_client_ip_addr(octets_out, nwritten_out)
	})

	// xqd_request.go
	_ = linker.FuncWrap("env", "xqd_req_new", func(handle_out int32) int32 {
		return i.xqd_req_new(handle_out)
	})
	_ = linker.FuncWrap("env", "xqd_req_version_get", func(handle int32, version_out int32) int32 {
		return i.xqd_req_version_get(handle, version_out)
	})
	_ = linker.FuncWrap("env", "xqd_req_version_set", func(handle int32, version int32) int32 {
		return i.xqd_req_version_set(handle, version)
	})
	_ = linker.FuncWrap("env", "xqd_req_method_get", i.xqd_req_method_get)
	_ = linker.FuncWrap("env", "xqd_req_method_set", i.xqd_req_method_set)
	_ = linker.FuncWrap("env", "xqd_req_uri_get", i.xqd_req_uri_get)
	_ = linker.FuncWrap("env", "xqd_req_uri_set", i.xqd_req_uri_set)
	_ = linker.FuncWrap("env", "xqd_req_header_remove", i.xqd_req_header_remove)
	_ = linker.FuncWrap("env", "xqd_req_header_insert", i.xqd_req_header_insert)
	_ = linker.FuncWrap("env", "xqd_req_header_append", i.xqd_req_header_append)
	_ = linker.FuncWrap("env", "xqd_req_header_names_get", i.xqd_req_header_names_get)
	_ = linker.FuncWrap("env", "xqd_req_header_value_get", i.xqd_req_header_value_get)
	_ = linker.FuncWrap("env", "xqd_req_header_values_get", i.xqd_req_header_values_get)
	_ = linker.FuncWrap("env", "xqd_req_header_values_set", i.xqd_req_header_values_set)
	_ = linker.FuncWrap("env", "xqd_req_send", i.xqd_req_send)
	_ = linker.FuncWrap("env", "xqd_req_send_v2", i.xqd_req_send_v2)
	_ = linker.FuncWrap("env", "xqd_req_send_v3", i.xqd_req_send_v3)
	_ = linker.FuncWrap("env", "xqd_req_send_async", i.xqd_req_send_async)
	_ = linker.FuncWrap("env", "xqd_req_send_async_streaming", i.xqd_req_send_async_streaming)
	_ = linker.FuncWrap("env", "xqd_req_send_async_v2", i.xqd_req_send_async_v2)
	_ = linker.FuncWrap("env", "xqd_pending_req_poll", i.xqd_pending_req_poll)
	_ = linker.FuncWrap("env", "xqd_pending_req_poll_v2", i.xqd_pending_req_poll_v2)
	_ = linker.FuncWrap("env", "xqd_pending_req_wait", i.xqd_pending_req_wait)
	_ = linker.FuncWrap("env", "xqd_pending_req_wait_v2", i.xqd_pending_req_wait_v2)
	_ = linker.FuncWrap("env", "xqd_pending_req_select", i.xqd_pending_req_select)
	_ = linker.FuncWrap("env", "xqd_pending_req_select_v2", i.xqd_pending_req_select_v2)
	_ = linker.FuncWrap("env", "xqd_req_cache_override_set", i.xqd_req_cache_override_set)
	_ = linker.FuncWrap("env", "xqd_req_cache_override_v2_set", i.xqd_req_cache_override_v2_set)
	// The Go http implementation doesn't make it easy to get at the original headers in order, so
	// we just use the same sorted order
	_ = linker.FuncWrap("env", "xqd_req_original_header_names_get", i.xqd_req_header_names_get)
	_ = linker.FuncWrap("env", "xqd_req_close", i.xqd_req_close)
	_ = linker.FuncWrap("env", "xqd_req_downstream_client_ddos_detected", i.xqd_req_downstream_client_ddos_detected)
	_ = linker.FuncWrap("env", "xqd_req_fastly_key_is_valid", i.xqd_req_fastly_key_is_valid)
	_ = linker.FuncWrap("env", "xqd_req_downstream_compliance_region", i.xqd_req_downstream_compliance_region)
	_ = linker.FuncWrap("env", "xqd_req_on_behalf_of", i.xqd_req_on_behalf_of)
	_ = linker.FuncWrap("env", "xqd_req_framing_headers_mode_set", i.xqd_req_framing_headers_mode_set)
	_ = linker.FuncWrap("env", "xqd_req_auto_decompress_response_set", i.xqd_req_auto_decompress_response_set)
	_ = linker.FuncWrap("env", "xqd_req_register_dynamic_backend", i.xqd_req_register_dynamic_backend)

	// xqd_response.go
	_ = linker.FuncWrap("env", "xqd_resp_new", i.xqd_resp_new)
	_ = linker.FuncWrap("env", "xqd_resp_status_get", i.xqd_resp_status_get)
	_ = linker.FuncWrap("env", "xqd_resp_status_set", i.xqd_resp_status_set)
	_ = linker.FuncWrap("env", "xqd_resp_version_get", i.xqd_resp_version_get)
	_ = linker.FuncWrap("env", "xqd_resp_version_set", i.xqd_resp_version_set)
	_ = linker.FuncWrap("env", "xqd_resp_header_remove", i.xqd_resp_header_remove)
	_ = linker.FuncWrap("env", "xqd_resp_header_insert", i.xqd_resp_header_insert)
	_ = linker.FuncWrap("env", "xqd_resp_header_append", i.xqd_resp_header_append)
	_ = linker.FuncWrap("env", "xqd_resp_header_names_get", i.xqd_resp_header_names_get)
	_ = linker.FuncWrap("env", "xqd_resp_header_values_get", i.xqd_resp_header_values_get)
	_ = linker.FuncWrap("env", "xqd_resp_header_values_set", i.xqd_resp_header_values_set)
	_ = linker.FuncWrap("env", "xqd_resp_close", i.xqd_resp_close)
	_ = linker.FuncWrap("env", "xqd_resp_framing_headers_mode_set", i.xqd_resp_framing_headers_mode_set)
	_ = linker.FuncWrap("env", "xqd_resp_http_keepalive_mode_set", i.xqd_resp_http_keepalive_mode_set)
	_ = linker.FuncWrap("env", "xqd_resp_get_addr_dest_ip", i.xqd_resp_get_addr_dest_ip)
	_ = linker.FuncWrap("env", "xqd_resp_get_addr_dest_port", i.xqd_resp_get_addr_dest_port)

	// xqd_body.go
	_ = linker.FuncWrap("env", "xqd_body_new", i.xqd_body_new)
	_ = linker.FuncWrap("env", "xqd_body_write", i.xqd_body_write)
	_ = linker.FuncWrap("env", "xqd_body_read", i.xqd_body_read)
	_ = linker.FuncWrap("env", "xqd_body_append", i.xqd_body_append)
	_ = linker.FuncWrap("env", "xqd_body_abandon", i.xqd_body_abandon)
	_ = linker.FuncWrap("env", "xqd_body_known_length", i.xqd_body_known_length)
	_ = linker.FuncWrap("env", "xqd_body_trailer_append", i.xqd_body_trailer_append)
	_ = linker.FuncWrap("env", "xqd_body_trailer_names_get", i.xqd_body_trailer_names_get)
	_ = linker.FuncWrap("env", "xqd_body_trailer_value_get", i.xqd_body_trailer_value_get)
	_ = linker.FuncWrap("env", "xqd_body_trailer_values_get", i.xqd_body_trailer_values_get)

	// xqd_log.go
	_ = linker.FuncWrap("env", "xqd_log_endpoint_get", i.xqd_log_endpoint_get)
	_ = linker.FuncWrap("env", "xqd_log_write", i.xqd_log_write)

	// xqd_image_optimizer.go
	_ = linker.FuncWrap("env", "xqd_image_optimizer_transform_request", i.xqd_image_optimizer_transform_request)

	// xqd_acl.go
	_ = linker.FuncWrap("env", "xqd_acl_open", i.xqd_acl_open)
	_ = linker.FuncWrap("env", "xqd_acl_lookup", i.xqd_acl_lookup)

	// xqd_erl.go
	_ = linker.FuncWrap("env", "xqd_erl_check_rate", i.xqd_erl_check_rate)
	_ = linker.FuncWrap("env", "xqd_erl_ratecounter_increment", i.xqd_erl_ratecounter_increment)
	_ = linker.FuncWrap("env", "xqd_erl_ratecounter_lookup_rate", i.xqd_erl_ratecounter_lookup_rate)
	_ = linker.FuncWrap("env", "xqd_erl_ratecounter_lookup_count", i.xqd_erl_ratecounter_lookup_count)
	_ = linker.FuncWrap("env", "xqd_erl_penaltybox_add", i.xqd_erl_penaltybox_add)
	_ = linker.FuncWrap("env", "xqd_erl_penaltybox_has", i.xqd_erl_penaltybox_has)

	// xqd_compute_runtime.go
	_ = linker.FuncWrap("env", "xqd_compute_runtime_get_vcpu_ms", i.xqd_compute_runtime_get_vcpu_ms)

	// xqd_async_io.go
	_ = linker.FuncWrap("env", "xqd_async_io_select", i.xqd_async_io_select)
	_ = linker.FuncWrap("env", "xqd_async_io_is_ready", i.xqd_async_io_is_ready)

	// xqd_http_downstream.go
	_ = linker.FuncWrap("env", "xqd_http_downstream_next_request", i.xqd_http_downstream_next_request)
	_ = linker.FuncWrap("env", "xqd_http_downstream_next_request_wait", i.xqd_http_downstream_next_request_wait)
	_ = linker.FuncWrap("env", "xqd_http_downstream_next_request_abandon", i.xqd_http_downstream_next_request_abandon)
	_ = linker.FuncWrap("env", "xqd_http_downstream_original_header_names", i.xqd_http_downstream_original_header_names)
	_ = linker.FuncWrap("env", "xqd_http_downstream_original_header_count", i.xqd_http_downstream_original_header_count)
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_cipher_openssl_name", i.xqd_http_downstream_tls_cipher_openssl_name)
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_protocol", i.xqd_http_downstream_tls_protocol)
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_client_servername", i.xqd_http_downstream_tls_client_servername)
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_client_hello", i.xqd_http_downstream_tls_client_hello)
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_raw_client_certificate", i.xqd_http_downstream_tls_raw_client_certificate)
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_client_cert_verify_result", i.xqd_http_downstream_tls_client_cert_verify_result)
	_ = linker.FuncWrap("env", "xqd_req_downstream_client_h2_fingerprint", i.xqd_http_downstream_client_h2_fingerprint)
	_ = linker.FuncWrap("env", "xqd_req_downstream_client_oh_fingerprint", i.xqd_http_downstream_client_oh_fingerprint)
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_ja3_md5", i.xqd_http_downstream_tls_ja3_md5)
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_ja4", i.xqd_http_downstream_tls_ja4)
}

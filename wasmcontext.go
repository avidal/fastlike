package fastlike

import (
	"fmt"

	"github.com/bytecodealliance/wasmtime-go/v38"
)

// wasmContext holds the compiled wasm module, engine, and shared linker that are reused across all requests.
// This allows amortizing the expensive compilation and linking steps across multiple request instances.
//
// Thread-safe sharing model:
//   - engine, module, linker: Read-only, safely shared across all instances
//   - store: Created fresh per-request in Instance.setup()
//   - wasm instance: Created fresh per-request in Instance.setup()
//
// The linker can be shared because host functions retrieve per-request state from the store's
// attached data (via caller.Data()) rather than capturing instance state in closures.
type wasmContext struct {
	engine *wasmtime.Engine // Shared wasm engine
	module *wasmtime.Module // Compiled wasm module (shared, read-only)
	linker *wasmtime.Linker // Shared linker with host functions (shared, read-only)
}

// safeWrap1 wraps a 1-argument host function with panic recovery to work around wasmtime-go v37 bug.
// When host functions with *Caller panic, wasmtime-go v37 has a nil pointer dereference bug.
// This wrapper catches panics and converts them to proper error returns.
// The Instance is retrieved from caller.Data(), enabling the use of a shared linker.
func safeWrap1(name string, fn func(*Instance, int32) int32) func(*wasmtime.Caller, int32) int32 {
	return func(caller *wasmtime.Caller, a int32) (ret int32) {
		i := caller.Data().(*Instance)
		defer func() {
			if r := recover(); r != nil {
				i.abilog.Printf("PANIC in %s: %v", name, r)
				ret = XqdError
			}
		}()
		return fn(i, a)
	}
}

// safeWrap2 wraps a 2-argument host function with panic recovery.
func safeWrap2(name string, fn func(*Instance, int32, int32) int32) func(*wasmtime.Caller, int32, int32) int32 {
	return func(caller *wasmtime.Caller, a, b int32) (ret int32) {
		i := caller.Data().(*Instance)
		defer func() {
			if r := recover(); r != nil {
				i.abilog.Printf("PANIC in %s: %v", name, r)
				ret = XqdError
			}
		}()
		return fn(i, a, b)
	}
}

// safeWrap3 wraps a 3-argument host function with panic recovery.
func safeWrap3(name string, fn func(*Instance, int32, int32, int32) int32) func(*wasmtime.Caller, int32, int32, int32) int32 {
	return func(caller *wasmtime.Caller, a, b, c int32) (ret int32) {
		i := caller.Data().(*Instance)
		defer func() {
			if r := recover(); r != nil {
				i.abilog.Printf("PANIC in %s: %v", name, r)
				ret = XqdError
			}
		}()
		return fn(i, a, b, c)
	}
}

// safeWrap4 wraps a 4-argument host function with panic recovery.
func safeWrap4(name string, fn func(*Instance, int32, int32, int32, int32) int32) func(*wasmtime.Caller, int32, int32, int32, int32) int32 {
	return func(caller *wasmtime.Caller, a, b, c, d int32) (ret int32) {
		i := caller.Data().(*Instance)
		defer func() {
			if r := recover(); r != nil {
				i.abilog.Printf("PANIC in %s: %v", name, r)
				ret = XqdError
			}
		}()
		return fn(i, a, b, c, d)
	}
}

// safeWrap5 wraps a 5-argument host function with panic recovery.
func safeWrap5(name string, fn func(*Instance, int32, int32, int32, int32, int32) int32) func(*wasmtime.Caller, int32, int32, int32, int32, int32) int32 {
	return func(caller *wasmtime.Caller, a, b, c, d, e int32) (ret int32) {
		i := caller.Data().(*Instance)
		defer func() {
			if r := recover(); r != nil {
				i.abilog.Printf("PANIC in %s: %v", name, r)
				ret = XqdError
			}
		}()
		return fn(i, a, b, c, d, e)
	}
}

// safeWrap6 wraps a 6-argument host function with panic recovery.
func safeWrap6(name string, fn func(*Instance, int32, int32, int32, int32, int32, int32) int32) func(*wasmtime.Caller, int32, int32, int32, int32, int32, int32) int32 {
	return func(caller *wasmtime.Caller, a, b, c, d, e, f int32) (ret int32) {
		i := caller.Data().(*Instance)
		defer func() {
			if r := recover(); r != nil {
				i.abilog.Printf("PANIC in %s: %v", name, r)
				ret = XqdError
			}
		}()
		return fn(i, a, b, c, d, e, f)
	}
}

// safeWrap7 wraps a 7-argument host function with panic recovery.
func safeWrap7(name string, fn func(*Instance, int32, int32, int32, int32, int32, int32, int32) int32) func(*wasmtime.Caller, int32, int32, int32, int32, int32, int32, int32) int32 {
	return func(caller *wasmtime.Caller, a, b, c, d, e, f, g int32) (ret int32) {
		i := caller.Data().(*Instance)
		defer func() {
			if r := recover(); r != nil {
				i.abilog.Printf("PANIC in %s: %v", name, r)
				ret = XqdError
			}
		}()
		return fn(i, a, b, c, d, e, f, g)
	}
}

func safeWrap8(name string, fn func(*Instance, int32, int32, int32, int32, int32, int32, int32, int32) int32) func(*wasmtime.Caller, int32, int32, int32, int32, int32, int32, int32, int32) int32 {
	return func(caller *wasmtime.Caller, a, b, c, d, e, f, g, h int32) (ret int32) {
		i := caller.Data().(*Instance)
		defer func() {
			if r := recover(); r != nil {
				i.abilog.Printf("PANIC in %s: %v", name, r)
				ret = XqdError
			}
		}()
		return fn(i, a, b, c, d, e, f, g, h)
	}
}

// Additional wrappers for uint32 parameters (for cache functions)
func safeWrap9(name string, fn func(*Instance, int32, int32, int32, int32, int32, int32, int32, int32, int32) int32) func(*wasmtime.Caller, int32, int32, int32, int32, int32, int32, int32, int32, int32) int32 {
	return func(caller *wasmtime.Caller, a, b, c, d, e, f, g, h, j int32) (ret int32) {
		i := caller.Data().(*Instance)
		defer func() {
			if r := recover(); r != nil {
				i.abilog.Printf("PANIC in %s: %v", name, r)
				ret = XqdError
			}
		}()
		return fn(i, a, b, c, d, e, f, g, h, j)
	}
}

func safeWrap11(name string, fn func(*Instance, int32, int32, int32, int32, int32, int32, int32, int32, int32, int32, int32) int32) func(*wasmtime.Caller, int32, int32, int32, int32, int32, int32, int32, int32, int32, int32, int32) int32 {
	return func(caller *wasmtime.Caller, a, b, c, d, e, f, g, h, j, k, l int32) (ret int32) {
		i := caller.Data().(*Instance)
		defer func() {
			if r := recover(); r != nil {
				i.abilog.Printf("PANIC in %s: %v", name, r)
				ret = XqdError
			}
		}()
		return fn(i, a, b, c, d, e, f, g, h, j, k, l)
	}
}

func safeWrap14(name string, fn func(*Instance, int32, int32, int32, int32, int32, int32, int32, int32, int32, int32, int32, int32, int32, int32) int32) func(*wasmtime.Caller, int32, int32, int32, int32, int32, int32, int32, int32, int32, int32, int32, int32, int32, int32) int32 {
	return func(caller *wasmtime.Caller, a, b, c, d, e, f, g, h, j, k, l, m, n, o int32) (ret int32) {
		i := caller.Data().(*Instance)
		defer func() {
			if r := recover(); r != nil {
				i.abilog.Printf("PANIC in %s: %v", name, r)
				ret = XqdError
			}
		}()
		return fn(i, a, b, c, d, e, f, g, h, j, k, l, m, n, o)
	}
}

// Additional wrappers for special parameter types
func safeWrap1i64(name string, fn func(*Instance, int64) int32) func(*wasmtime.Caller, int64) int32 {
	return func(caller *wasmtime.Caller, a int64) (ret int32) {
		i := caller.Data().(*Instance)
		defer func() {
			if r := recover(); r != nil {
				i.abilog.Printf("PANIC in %s: %v", name, r)
				ret = XqdError
			}
		}()
		return fn(i, a)
	}
}

// compile creates a wasm engine, module, and shared linker from the provided wasm bytes.
// The compiled artifacts are stored in wasmContext for reuse across requests.
// This is called once per Fastlike instance (or when reloading).
func (i *Instance) compile(wasmbytes []byte) {
	// Create a wasmtime config with default settings
	config := wasmtime.NewConfig()

	// Enable compilation caching for faster startup
	check(config.CacheConfigLoadDefault())

	// Note: Epoch interruption is temporarily disabled due to bugs in wasmtime-go v37
	// TODO: Re-enable when upgrading: config.SetEpochInterruption(true)

	// Create the engine and compile the module
	engine := wasmtime.NewEngineWithConfig(config)
	module, err := wasmtime.NewModule(engine, wasmbytes)
	check(err)

	// Create a shared linker and link all host functions
	// The linker is shared across all requests because host functions retrieve
	// per-request state from caller.Data() rather than capturing it in closures
	linker := wasmtime.NewLinker(engine)
	check(linker.DefineWasi())
	link(linker)
	linklegacy(linker)

	// Store for reuse across all request instances
	i.wasmctx = &wasmContext{
		engine: engine,
		module: module,
		linker: linker,
	}
}

// link binds all XQD ABI host functions to the linker using modern naming conventions (fastly_*).
//
// This function links all the host functions that implement the XQD ABI. The guest wasm program
// calls these functions to interact with the host (e.g., to send HTTP requests, manipulate
// headers, access dictionaries, etc.).
//
// Each function is wrapped with safeWrap* to provide panic recovery, working around bugs in
// wasmtime-go v37 where panics in host functions can cause nil pointer dereferences.
//
// The linker is created once at compile time and shared across all requests. Host functions
// retrieve per-request state from caller.Data(), which returns the Instance attached to the Store.
func link(linker *wasmtime.Linker) {
	// Additional HTTP request/response functions
	_ = linker.FuncWrap("fastly_http_req", "original_header_count", safeWrap1("original_header_count", func(i *Instance, count_out int32) int32 {
		return i.xqd_http_downstream_original_header_count(i.downstreamRequestHandle, count_out)
	}))

	_ = linker.FuncWrap("fastly_http_resp", "header_value_get", safeWrap6("header_value_get", func(i *Instance, handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32, nwritten_out int32) int32 {
		return i.xqd_resp_header_value_get(handle, name_addr, name_size, value_addr, value_size, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "header_remove", safeWrap3("header_remove", func(i *Instance, handle int32, name_addr int32, name_size int32) int32 {
		return i.xqd_resp_header_remove(handle, name_addr, name_size)
	}))

	// xqd_http_cache.go
	_ = linker.FuncWrap("fastly_http_cache", "is_request_cacheable", safeWrap2("is_request_cacheable", func(i *Instance, req_handle int32, is_cacheable_out int32) int32 {
		return i.xqd_http_cache_is_request_cacheable(req_handle, is_cacheable_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "get_suggested_cache_key", safeWrap4("get_suggested_cache_key", func(i *Instance, req_handle int32, key_out int32, key_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_cache_get_suggested_cache_key(req_handle, key_out, key_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "lookup", safeWrap4("lookup", func(i *Instance, req_handle int32, options_mask int32, options int32, cache_handle_out int32) int32 {
		return i.xqd_http_cache_lookup(req_handle, uint32(options_mask), options, cache_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "transaction_lookup", safeWrap4("transaction_lookup", func(i *Instance, req_handle int32, options_mask int32, options int32, cache_handle_out int32) int32 {
		return i.xqd_http_cache_transaction_lookup(req_handle, uint32(options_mask), options, cache_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "transaction_insert", safeWrap5("transaction_insert", func(i *Instance, cache_handle int32, resp_handle int32, options_mask int32, options int32, body_handle_out int32) int32 {
		return i.xqd_http_cache_transaction_insert(cache_handle, resp_handle, uint32(options_mask), options, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "transaction_insert_and_stream_back", safeWrap6("transaction_insert_and_stream_back", func(i *Instance, cache_handle int32, resp_handle int32, options_mask int32, options int32, body_handle_out int32, cache_handle_out int32) int32 {
		return i.xqd_http_cache_transaction_insert_and_stream_back(cache_handle, resp_handle, uint32(options_mask), options, body_handle_out, cache_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "transaction_update", safeWrap4("transaction_update", func(i *Instance, cache_handle int32, resp_handle int32, options_mask int32, options int32) int32 {
		return i.xqd_http_cache_transaction_update(cache_handle, resp_handle, uint32(options_mask), options)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "transaction_update_and_return_fresh", safeWrap5("transaction_update_and_return_fresh", func(i *Instance, cache_handle int32, resp_handle int32, options_mask int32, options int32, cache_handle_out int32) int32 {
		return i.xqd_http_cache_transaction_update_and_return_fresh(cache_handle, resp_handle, uint32(options_mask), options, cache_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "transaction_record_not_cacheable", safeWrap3("transaction_record_not_cacheable", func(i *Instance, cache_handle int32, options_mask int32, options int32) int32 {
		return i.xqd_http_cache_transaction_record_not_cacheable(cache_handle, uint32(options_mask), options)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "transaction_abandon", safeWrap1("transaction_abandon", func(i *Instance, cache_handle int32) int32 {
		return i.xqd_http_cache_transaction_abandon(cache_handle)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "close", safeWrap1("close", func(i *Instance, cache_handle int32) int32 {
		return i.xqd_http_cache_close(cache_handle)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "get_suggested_backend_request", safeWrap2("get_suggested_backend_request", func(i *Instance, resp_handle int32, req_handle_out int32) int32 {
		return i.xqd_http_cache_get_suggested_backend_request(resp_handle, req_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "get_suggested_cache_options", safeWrap6("get_suggested_cache_options", func(i *Instance, cache_handle int32, resp_handle int32, requested_mask int32, requested_options int32, options_mask_out int32, options_out int32) int32 {
		return i.xqd_http_cache_get_suggested_cache_options(cache_handle, resp_handle, uint32(requested_mask), requested_options, options_mask_out, options_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "prepare_response_for_storage", safeWrap4("prepare_response_for_storage", func(i *Instance, cache_handle int32, resp_handle int32, storage_action_out int32, resp_handle_out int32) int32 {
		return i.xqd_http_cache_prepare_response_for_storage(cache_handle, resp_handle, storage_action_out, resp_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "get_found_response", safeWrap4("get_found_response", func(i *Instance, cache_handle int32, transform_for_client int32, resp_handle_out int32, body_handle_out int32) int32 {
		return i.xqd_http_cache_get_found_response(cache_handle, uint32(transform_for_client), resp_handle_out, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "get_state", safeWrap2("get_state", func(i *Instance, cache_handle int32, state_out int32) int32 {
		return i.xqd_http_cache_get_state(cache_handle, state_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "get_length", safeWrap2("get_length", func(i *Instance, cache_handle int32, length_out int32) int32 {
		return i.xqd_http_cache_get_length(cache_handle, length_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "get_max_age_ns", safeWrap2("get_max_age_ns", func(i *Instance, cache_handle int32, max_age_ns_out int32) int32 {
		return i.xqd_http_cache_get_max_age_ns(cache_handle, max_age_ns_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "get_stale_while_revalidate_ns", safeWrap2("get_stale_while_revalidate_ns", func(i *Instance, cache_handle int32, stale_while_revalidate_ns_out int32) int32 {
		return i.xqd_http_cache_get_stale_while_revalidate_ns(cache_handle, stale_while_revalidate_ns_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "get_age_ns", safeWrap2("get_age_ns", func(i *Instance, cache_handle int32, age_ns_out int32) int32 {
		return i.xqd_http_cache_get_age_ns(cache_handle, age_ns_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "get_hits", safeWrap2("get_hits", func(i *Instance, cache_handle int32, hits_out int32) int32 {
		return i.xqd_http_cache_get_hits(cache_handle, hits_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "get_sensitive_data", safeWrap2("get_sensitive_data", func(i *Instance, cache_handle int32, sensitive_data_out int32) int32 {
		return i.xqd_http_cache_get_sensitive_data(cache_handle, sensitive_data_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "get_surrogate_keys", safeWrap4("get_surrogate_keys", func(i *Instance, cache_handle int32, surrogate_keys_out int32, surrogate_keys_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_cache_get_surrogate_keys(cache_handle, surrogate_keys_out, surrogate_keys_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_cache", "get_vary_rule", safeWrap4("get_vary_rule", func(i *Instance, cache_handle int32, vary_rule_out int32, vary_rule_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_cache_get_vary_rule(cache_handle, vary_rule_out, vary_rule_max_len, nwritten_out)
	}))

	// xqd.go
	// Use FuncWrap for v37 compatibility with *Caller parameter
	// Add panic recovery to work around wasmtime-go v37 bug with FuncWrap panic handling
	err := linker.FuncWrap("fastly_abi", "init", safeWrap1i64("init", func(i *Instance, abiv int64) int32 {
		return i.xqd_init(abiv)
	}))
	if err != nil {
		panic(fmt.Sprintf("Failed to define fastly_abi::init: %v", err))
	}

	err = linker.FuncWrap("fastly_uap", "parse", safeWrap14("parse", func(i *Instance, addr int32, size int32, family_out int32, family_maxlen int32, family_nwritten_out int32, major_out int32, major_maxlen int32, major_nwritten_out int32, minor_out int32, minor_maxlen int32, minor_nwritten_out int32, patch_out int32, patch_maxlen int32, patch_nwritten_out int32) int32 {
		return i.xqd_uap_parse(addr, size, family_out, family_maxlen, family_nwritten_out, major_out, major_maxlen, major_nwritten_out, minor_out, minor_maxlen, minor_nwritten_out, patch_out, patch_maxlen, patch_nwritten_out)
	}))
	if err != nil {
		panic(fmt.Sprintf("Failed to define fastly_uap::parse: %v", err))
	}

	// xqd_request.go
	// Use FuncWrap for all functions for v37 compatibility
	_ = linker.FuncWrap("fastly_http_req", "body_downstream_get", safeWrap2("body_downstream_get", func(i *Instance, request_handle_out int32, body_handle_out int32) int32 {
		if i.memory == nil || i.ds_request == nil {
			// Return stub values if called during initialization
			return XqdErrUnsupported
		}
		return i.xqd_req_body_downstream_get(request_handle_out, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "downstream_client_ip_addr", safeWrap2("downstream_client_ip_addr", func(i *Instance, octets_out int32, nwritten_out int32) int32 {
		if i.memory == nil || i.ds_request == nil {
			return XqdStatusOK // Return OK with no data written
		}
		return i.xqd_req_downstream_client_ip_addr(octets_out, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "new", safeWrap1("new", func(i *Instance, handle_out int32) int32 {
		// Add panic recovery to work around wasmtime-go v37 bug
		return i.xqd_req_new(handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "version_get", safeWrap2("version_get", func(i *Instance, handle int32, version_out int32) int32 {
		if i == nil || i.memory == nil {
			return XqdErrUnsupported
		}
		return i.xqd_req_version_get(handle, version_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "version_set", safeWrap2("version_set", func(i *Instance, handle int32, version int32) int32 {
		if i == nil {
			return XqdErrUnsupported
		}
		return i.xqd_req_version_set(handle, version)
	}))
	_ = linker.FuncWrap("fastly_http_req", "method_get", safeWrap4("method_get", func(i *Instance, handle int32, addr int32, maxlen int32, nwritten_out int32) int32 {
		return i.xqd_req_method_get(handle, addr, maxlen, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "method_set", safeWrap3("method_set", func(i *Instance, handle int32, addr int32, size int32) int32 {
		return i.xqd_req_method_set(handle, addr, size)
	}))
	_ = linker.FuncWrap("fastly_http_req", "uri_get", safeWrap4("uri_get", func(i *Instance, handle int32, addr int32, maxlen int32, nwritten_out int32) int32 {
		return i.xqd_req_uri_get(handle, addr, maxlen, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "uri_set", safeWrap3("uri_set", func(i *Instance, handle int32, addr int32, size int32) int32 {
		return i.xqd_req_uri_set(handle, addr, size)
	}))
	_ = linker.FuncWrap("fastly_http_req", "header_names_get", safeWrap6("header_names_get", func(i *Instance, handle int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
		return i.xqd_req_header_names_get(handle, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "header_remove", safeWrap3("header_remove", func(i *Instance, handle int32, name_addr int32, name_size int32) int32 {
		return i.xqd_req_header_remove(handle, name_addr, name_size)
	}))
	_ = linker.FuncWrap("fastly_http_req", "header_insert", safeWrap5("header_insert", func(i *Instance, handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
		return i.xqd_req_header_insert(handle, name_addr, name_size, value_addr, value_size)
	}))
	_ = linker.FuncWrap("fastly_http_req", "header_append", safeWrap5("header_append", func(i *Instance, handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
		return i.xqd_req_header_append(handle, name_addr, name_size, value_addr, value_size)
	}))
	_ = linker.FuncWrap("fastly_http_req", "header_value_get", safeWrap6("header_value_get", func(i *Instance, handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32, nwritten_out int32) int32 {
		return i.xqd_req_header_value_get(handle, name_addr, name_size, value_addr, value_size, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "header_values_get", safeWrap8("header_values_get", func(i *Instance, handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
		return i.xqd_req_header_values_get(handle, name_addr, name_size, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "header_values_set", safeWrap5("header_values_set", func(i *Instance, handle int32, name_addr int32, name_size int32, values_addr int32, values_size int32) int32 {
		return i.xqd_req_header_values_set(handle, name_addr, name_size, values_addr, values_size)
	}))
	_ = linker.FuncWrap("fastly_http_req", "send", safeWrap6("send", func(i *Instance, req_handle int32, body_handle int32, backend_addr int32, backend_size int32, resp_handle_out int32, body_handle_out int32) int32 {
		return i.xqd_req_send(req_handle, body_handle, backend_addr, backend_size, resp_handle_out, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "send_v2", safeWrap7("send_v2", func(i *Instance, req_handle int32, body_handle int32, backend_addr int32, backend_size int32, error_detail int32, resp_handle_out int32, body_handle_out int32) int32 {
		return i.xqd_req_send_v2(req_handle, body_handle, backend_addr, backend_size, error_detail, resp_handle_out, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "send_v3", safeWrap7("send_v3", func(i *Instance, req_handle int32, body_handle int32, backend_addr int32, backend_size int32, error_detail int32, resp_handle_out int32, body_handle_out int32) int32 {
		return i.xqd_req_send_v3(req_handle, body_handle, backend_addr, backend_size, error_detail, resp_handle_out, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "send_async", safeWrap5("send_async", func(i *Instance, req_handle int32, body_handle int32, backend_addr int32, backend_size int32, pending_req_handle_out int32) int32 {
		return i.xqd_req_send_async(req_handle, body_handle, backend_addr, backend_size, pending_req_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "send_async_streaming", safeWrap5("send_async_streaming", func(i *Instance, req_handle int32, body_handle int32, backend_addr int32, backend_size int32, pending_req_handle_out int32) int32 {
		return i.xqd_req_send_async_streaming(req_handle, body_handle, backend_addr, backend_size, pending_req_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "send_async_v2", safeWrap6("send_async_v2", func(i *Instance, req_handle int32, body_handle int32, backend_addr int32, backend_size int32, error_detail int32, pending_req_handle_out int32) int32 {
		return i.xqd_req_send_async_v2(req_handle, body_handle, backend_addr, backend_size, error_detail, pending_req_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "pending_req_poll", safeWrap4("pending_req_poll", func(i *Instance, pending_req_handle int32, is_done_out int32, resp_handle_out int32, body_handle_out int32) int32 {
		return i.xqd_pending_req_poll(pending_req_handle, is_done_out, resp_handle_out, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "pending_req_poll_v2", safeWrap5("pending_req_poll_v2", func(i *Instance, pending_req_handle int32, error_detail int32, is_done_out int32, resp_handle_out int32, body_handle_out int32) int32 {
		return i.xqd_pending_req_poll_v2(pending_req_handle, error_detail, is_done_out, resp_handle_out, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "pending_req_wait", safeWrap3("pending_req_wait", func(i *Instance, pending_req_handle int32, resp_handle_out int32, body_handle_out int32) int32 {
		return i.xqd_pending_req_wait(pending_req_handle, resp_handle_out, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "pending_req_wait_v2", safeWrap4("pending_req_wait_v2", func(i *Instance, pending_req_handle int32, error_detail int32, resp_handle_out int32, body_handle_out int32) int32 {
		return i.xqd_pending_req_wait_v2(pending_req_handle, error_detail, resp_handle_out, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "pending_req_select", safeWrap5("pending_req_select", func(i *Instance, pending_req_handles int32, pending_req_handles_len int32, done_index_out int32, resp_handle_out int32, body_handle_out int32) int32 {
		return i.xqd_pending_req_select(pending_req_handles, pending_req_handles_len, done_index_out, resp_handle_out, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "pending_req_select_v2", safeWrap6("pending_req_select_v2", func(i *Instance, pending_req_handles int32, pending_req_handles_len int32, error_detail int32, done_index_out int32, resp_handle_out int32, body_handle_out int32) int32 {
		return i.xqd_pending_req_select_v2(pending_req_handles, pending_req_handles_len, error_detail, done_index_out, resp_handle_out, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "cache_override_set", safeWrap4("cache_override_set", func(i *Instance, req_handle int32, tag int32, ttl int32, stale_while_revalidate int32) int32 {
		return i.xqd_req_cache_override_set(req_handle, tag, ttl, stale_while_revalidate)
	}))
	_ = linker.FuncWrap("fastly_http_req", "cache_override_v2_set", safeWrap6("cache_override_v2_set", func(i *Instance, req_handle int32, tag int32, ttl int32, stale_while_revalidate int32, sk_addr int32, sk_size int32) int32 {
		return i.xqd_req_cache_override_v2_set(req_handle, tag, ttl, stale_while_revalidate, sk_addr, sk_size)
	}))
	// The Go http implementation doesn't make it easy to get at the original headers in order, so
	// we just use the same sorted order
	_ = linker.FuncWrap("fastly_http_req", "original_header_names_get", safeWrap5("original_header_names_get", func(i *Instance, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_original_header_names(i.downstreamRequestHandle, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
	}))
	// Try using FuncWrap instead of DefineFunc for v37 compatibility
	_ = linker.FuncWrap("fastly_http_req", "close", safeWrap1("close", func(i *Instance, handle int32) int32 {
		if i == nil {
			panic("Instance is nil in close")
		}
		return i.xqd_req_close(handle)
	}))
	_ = linker.FuncWrap("fastly_http_req", "downstream_client_ddos_detected", safeWrap1("downstream_client_ddos_detected", func(i *Instance, is_ddos_out int32) int32 {
		return i.xqd_req_downstream_client_ddos_detected(is_ddos_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "fastly_key_is_valid", safeWrap1("fastly_key_is_valid", func(i *Instance, is_valid_out int32) int32 {
		return i.xqd_req_fastly_key_is_valid(is_valid_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "downstream_compliance_region", safeWrap3("downstream_compliance_region", func(i *Instance, region_out int32, region_max_len int32, nwritten_out int32) int32 {
		return i.xqd_req_downstream_compliance_region(region_out, region_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "on_behalf_of", safeWrap3("on_behalf_of", func(i *Instance, handle int32, service_addr int32, service_size int32) int32 {
		return i.xqd_req_on_behalf_of(handle, service_addr, service_size)
	}))
	_ = linker.FuncWrap("fastly_http_req", "framing_headers_mode_set", safeWrap2("framing_headers_mode_set", func(i *Instance, req_handle int32, mode int32) int32 {
		return i.xqd_req_framing_headers_mode_set(req_handle, mode)
	}))
	_ = linker.FuncWrap("fastly_http_req", "auto_decompress_response_set", safeWrap2("auto_decompress_response_set", func(i *Instance, req_handle int32, mode int32) int32 {
		return i.xqd_req_auto_decompress_response_set(req_handle, mode)
	}))
	_ = linker.FuncWrap("fastly_http_req", "register_dynamic_backend", safeWrap6("register_dynamic_backend", func(i *Instance, backend_addr int32, backend_size int32, target_addr int32, target_size int32, options_mask int32, options_ptr int32) int32 {
		return i.xqd_req_register_dynamic_backend(backend_addr, backend_size, target_addr, target_size, options_mask, options_ptr)
	}))
	_ = linker.FuncWrap("fastly_http_req", "inspect", safeWrap6("inspect", func(i *Instance, req int32, body int32, insp_info_mask int32, insp_info int32, buf int32, buf_len int32) int32 {
		return i.xqd_req_inspect(req, body, insp_info_mask, insp_info, buf, buf_len)
	}))
	// DEPRECATED: use fastly_http_downstream versions
	_ = linker.FuncWrap("fastly_http_req", "downstream_client_h2_fingerprint", safeWrap3("downstream_client_h2_fingerprint", func(i *Instance, h2_out int32, h2_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_client_h2_fingerprint(i.downstreamRequestHandle, h2_out, h2_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "downstream_client_oh_fingerprint", safeWrap3("downstream_client_oh_fingerprint", func(i *Instance, oh_out int32, oh_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_client_oh_fingerprint(i.downstreamRequestHandle, oh_out, oh_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "downstream_tls_ja3_md5", safeWrap2("downstream_tls_ja3_md5", func(i *Instance, ja3_md5_out int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_ja3_md5(i.downstreamRequestHandle, ja3_md5_out, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "downstream_tls_ja4", safeWrap3("downstream_tls_ja4", func(i *Instance, ja4_out int32, ja4_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_ja4(i.downstreamRequestHandle, ja4_out, ja4_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "downstream_tls_protocol", safeWrap3("downstream_tls_protocol", func(i *Instance, protocol_out int32, protocol_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_protocol(i.downstreamRequestHandle, protocol_out, protocol_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "downstream_tls_cipher_openssl_name", safeWrap3("downstream_tls_cipher_openssl_name", func(i *Instance, cipher_out int32, cipher_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_cipher_openssl_name(i.downstreamRequestHandle, cipher_out, cipher_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "downstream_tls_client_hello", safeWrap3("downstream_tls_client_hello", func(i *Instance, client_hello_out int32, client_hello_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_client_hello(i.downstreamRequestHandle, client_hello_out, client_hello_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "downstream_tls_client_servername", safeWrap3("downstream_tls_client_servername", func(i *Instance, servername_out int32, servername_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_client_servername(i.downstreamRequestHandle, servername_out, servername_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "downstream_tls_raw_client_certificate", safeWrap3("downstream_tls_raw_client_certificate", func(i *Instance, cert_out int32, cert_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_raw_client_certificate(i.downstreamRequestHandle, cert_out, cert_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_req", "downstream_tls_client_cert_verify_result", safeWrap1("downstream_tls_client_cert_verify_result", func(i *Instance, verify_result_out int32) int32 {
		return i.xqd_http_downstream_tls_client_cert_verify_result(i.downstreamRequestHandle, verify_result_out)
	}))

	// xqd_response.go
	_ = linker.FuncWrap("fastly_http_resp", "send_downstream", safeWrap3("send_downstream", func(i *Instance, resp_handle int32, body_handle int32, streaming int32) int32 {
		return i.xqd_resp_send_downstream(resp_handle, body_handle, streaming)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "new", safeWrap1("new", func(i *Instance, handle_out int32) int32 {
		return i.xqd_resp_new(handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "status_get", safeWrap2("status_get", func(i *Instance, handle int32, status_out int32) int32 {
		return i.xqd_resp_status_get(handle, status_out)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "status_set", safeWrap2("status_set", func(i *Instance, handle int32, status int32) int32 {
		return i.xqd_resp_status_set(handle, status)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "version_get", safeWrap2("version_get", func(i *Instance, handle int32, version_out int32) int32 {
		return i.xqd_resp_version_get(handle, version_out)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "version_set", safeWrap2("version_set", func(i *Instance, handle int32, version int32) int32 {
		return i.xqd_resp_version_set(handle, version)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "header_names_get", safeWrap6("header_names_get", func(i *Instance, handle int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
		return i.xqd_resp_header_names_get(handle, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "header_remove", safeWrap3("header_remove", func(i *Instance, handle int32, name_addr int32, name_size int32) int32 {
		return i.xqd_resp_header_remove(handle, name_addr, name_size)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "header_insert", safeWrap5("header_insert", func(i *Instance, handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
		return i.xqd_resp_header_insert(handle, name_addr, name_size, value_addr, value_size)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "header_append", safeWrap5("header_append", func(i *Instance, handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
		return i.xqd_resp_header_append(handle, name_addr, name_size, value_addr, value_size)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "header_values_get", safeWrap8("header_values_get", func(i *Instance, handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
		return i.xqd_resp_header_values_get(handle, name_addr, name_size, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "header_values_set", safeWrap5("header_values_set", func(i *Instance, handle int32, name_addr int32, name_size int32, values_addr int32, values_size int32) int32 {
		return i.xqd_resp_header_values_set(handle, name_addr, name_size, values_addr, values_size)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "close", safeWrap1("close", func(i *Instance, handle int32) int32 {
		return i.xqd_resp_close(handle)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "framing_headers_mode_set", safeWrap2("framing_headers_mode_set", func(i *Instance, resp_handle int32, mode int32) int32 {
		return i.xqd_resp_framing_headers_mode_set(resp_handle, mode)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "http_keepalive_mode_set", safeWrap2("http_keepalive_mode_set", func(i *Instance, resp_handle int32, mode int32) int32 {
		return i.xqd_resp_http_keepalive_mode_set(resp_handle, mode)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "get_addr_dest_ip", safeWrap3("get_addr_dest_ip", func(i *Instance, handle int32, addr int32, nwritten_out int32) int32 {
		return i.xqd_resp_get_addr_dest_ip(handle, addr, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "get_addr_dest_port", safeWrap2("get_addr_dest_port", func(i *Instance, handle int32, port_out int32) int32 {
		return i.xqd_resp_get_addr_dest_port(handle, port_out)
	}))
	_ = linker.FuncWrap("fastly_http_resp", "send_informational_response", safeWrap2("send_informational_response", func(i *Instance, resp_handle int32, status int32) int32 {
		return i.xqd_resp_send_informational_response(resp_handle, status)
	}))

	// xqd_body.go
	_ = linker.FuncWrap("fastly_http_body", "new", safeWrap1("new", func(i *Instance, handle_out int32) int32 {
		return i.xqd_body_new(handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_body", "write", safeWrap5("write", func(i *Instance, body_handle int32, addr int32, size int32, end int32, nwritten_out int32) int32 {
		return i.xqd_body_write(body_handle, addr, size, end, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_body", "read", safeWrap4("read", func(i *Instance, body_handle int32, addr int32, size int32, nread_out int32) int32 {
		return i.xqd_body_read(body_handle, addr, size, nread_out)
	}))
	_ = linker.FuncWrap("fastly_http_body", "append", safeWrap2("append", func(i *Instance, dst int32, src int32) int32 {
		return i.xqd_body_append(dst, src)
	}))
	_ = linker.FuncWrap("fastly_http_body", "close", safeWrap1("close", func(i *Instance, body_handle int32) int32 {
		return i.xqd_body_close(body_handle)
	}))
	_ = linker.FuncWrap("fastly_http_body", "abandon", safeWrap1("abandon", func(i *Instance, body_handle int32) int32 {
		return i.xqd_body_abandon(body_handle)
	}))
	_ = linker.FuncWrap("fastly_http_body", "known_length", safeWrap2("known_length", func(i *Instance, body_handle int32, known_length_out int32) int32 {
		return i.xqd_body_known_length(body_handle, known_length_out)
	}))
	_ = linker.FuncWrap("fastly_http_body", "trailer_append", safeWrap5("trailer_append", func(i *Instance, body_handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
		return i.xqd_body_trailer_append(body_handle, name_addr, name_size, value_addr, value_size)
	}))
	_ = linker.FuncWrap("fastly_http_body", "trailer_names_get", safeWrap6("trailer_names_get", func(i *Instance, body_handle int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
		return i.xqd_body_trailer_names_get(body_handle, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_body", "trailer_value_get", safeWrap6("trailer_value_get", func(i *Instance, body_handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32, nwritten_out int32) int32 {
		return i.xqd_body_trailer_value_get(body_handle, name_addr, name_size, value_addr, value_size, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_body", "trailer_values_get", safeWrap8("trailer_values_get", func(i *Instance, body_handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
		return i.xqd_body_trailer_values_get(body_handle, name_addr, name_size, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
	}))

	// xqd_log.go
	_ = linker.FuncWrap("fastly_log", "endpoint_get", safeWrap3("endpoint_get", func(i *Instance, name_addr int32, name_size int32, endpoint_handle_out int32) int32 {
		return i.xqd_log_endpoint_get(name_addr, name_size, endpoint_handle_out)
	}))
	_ = linker.FuncWrap("fastly_log", "write", safeWrap4("write", func(i *Instance, endpoint_handle int32, addr int32, size int32, nwritten_out int32) int32 {
		return i.xqd_log_write(endpoint_handle, addr, size, nwritten_out)
	}))

	// xqd_dictionary.go
	_ = linker.FuncWrap("fastly_dictionary", "open", safeWrap3("open", func(i *Instance, name_addr int32, name_size int32, dict_handle_out int32) int32 {
		return i.xqd_dictionary_open(name_addr, name_size, dict_handle_out)
	}))
	_ = linker.FuncWrap("fastly_dictionary", "get", safeWrap6("get", func(i *Instance, dict_handle int32, key_addr int32, key_size int32, value_addr int32, value_size int32, nwritten_out int32) int32 {
		return i.xqd_dictionary_get(dict_handle, key_addr, key_size, value_addr, value_size, nwritten_out)
	}))

	// xqd_geo.go
	_ = linker.FuncWrap("fastly_geo", "lookup", safeWrap5("lookup", func(i *Instance, addr_octets int32, addr_len int32, buf int32, buf_len int32, nwritten_out int32) int32 {
		return i.xqd_geo_lookup(addr_octets, addr_len, buf, buf_len, nwritten_out)
	}))

	// xqd_config_store.go
	_ = linker.FuncWrap("fastly_config_store", "open", safeWrap3("open", func(i *Instance, name_addr int32, name_size int32, store_handle_out int32) int32 {
		return i.xqd_config_store_open(name_addr, name_size, store_handle_out)
	}))
	_ = linker.FuncWrap("fastly_config_store", "get", safeWrap6("get", func(i *Instance, store_handle int32, key_addr int32, key_size int32, value_addr int32, value_size int32, nwritten_out int32) int32 {
		return i.xqd_config_store_get(store_handle, key_addr, key_size, value_addr, value_size, nwritten_out)
	}))

	// xqd_secret_store.go
	_ = linker.FuncWrap("fastly_secret_store", "open", safeWrap3("open", func(i *Instance, name_addr int32, name_size int32, store_handle_out int32) int32 {
		return i.xqd_secret_store_open(name_addr, name_size, store_handle_out)
	}))
	_ = linker.FuncWrap("fastly_secret_store", "get", safeWrap4("get", func(i *Instance, store_handle int32, key_addr int32, key_size int32, secret_handle_out int32) int32 {
		return i.xqd_secret_store_get(store_handle, key_addr, key_size, secret_handle_out)
	}))
	_ = linker.FuncWrap("fastly_secret_store", "plaintext", safeWrap4("plaintext", func(i *Instance, secret_handle int32, value_addr int32, value_size int32, nwritten_out int32) int32 {
		return i.xqd_secret_store_plaintext(secret_handle, value_addr, value_size, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_secret_store", "from_bytes", safeWrap3("from_bytes", func(i *Instance, value_addr int32, value_size int32, secret_handle_out int32) int32 {
		return i.xqd_secret_store_from_bytes(value_addr, value_size, secret_handle_out)
	}))

	// xqd_device_detection.go
	_ = linker.FuncWrap("fastly_device_detection", "lookup", safeWrap5("lookup", func(i *Instance, user_agent_addr int32, user_agent_size int32, buf int32, buf_len int32, nwritten_out int32) int32 {
		return i.xqd_device_detection_lookup(user_agent_addr, user_agent_size, buf, buf_len, nwritten_out)
	}))

	// xqd_image_optimizer.go
	_ = linker.FuncWrap("fastly_image_optimizer", "transform_image_optimizer_request", safeWrap9("transform_image_optimizer_request", func(i *Instance, originImageRequest int32, originImageRequestBody int32, originImageRequestBackendPtr int32, originImageRequestBackendLen int32, ioTransformConfigMask int32, ioTransformConfigPtr int32, ioErrorDetailPtr int32, respHandleOut int32, bodyHandleOut int32) int32 {
		return i.xqd_image_optimizer_transform_request(originImageRequest, originImageRequestBody, originImageRequestBackendPtr, originImageRequestBackendLen, uint32(ioTransformConfigMask), ioTransformConfigPtr, ioErrorDetailPtr, respHandleOut, bodyHandleOut)
	}))

	// xqd_acl.go
	_ = linker.FuncWrap("fastly_acl", "open", safeWrap3("open", func(i *Instance, name_addr int32, name_size int32, acl_handle_out int32) int32 {
		return i.xqd_acl_open(name_addr, name_size, acl_handle_out)
	}))
	_ = linker.FuncWrap("fastly_acl", "lookup", safeWrap5("lookup", func(i *Instance, acl_handle int32, ip_addr int32, ip_size int32, body_addr int32, body_size int32) int32 {
		return i.xqd_acl_lookup(acl_handle, ip_addr, ip_size, body_addr, body_size)
	}))

	// xqd_erl.go
	_ = linker.FuncWrap("fastly_erl", "check_rate", safeWrap11("check_rate", func(i *Instance, rc_addr int32, rc_size int32, entry_addr int32, entry_size int32, delta int32, window int32, limit int32, pb_addr int32, pb_size int32, ttl int32, blocked_out int32) int32 {
		return i.xqd_erl_check_rate(rc_addr, rc_size, entry_addr, entry_size, uint32(delta), uint32(window), uint32(limit), pb_addr, pb_size, uint32(ttl), blocked_out)
	}))
	_ = linker.FuncWrap("fastly_erl", "ratecounter_increment", safeWrap5("ratecounter_increment", func(i *Instance, rc_addr int32, rc_size int32, entry_addr int32, entry_size int32, delta int32) int32 {
		return i.xqd_erl_ratecounter_increment(rc_addr, rc_size, entry_addr, entry_size, uint32(delta))
	}))
	_ = linker.FuncWrap("fastly_erl", "ratecounter_lookup_rate", safeWrap6("ratecounter_lookup_rate", func(i *Instance, rc_addr int32, rc_size int32, entry_addr int32, entry_size int32, window int32, rate_out int32) int32 {
		return i.xqd_erl_ratecounter_lookup_rate(rc_addr, rc_size, entry_addr, entry_size, uint32(window), rate_out)
	}))
	_ = linker.FuncWrap("fastly_erl", "ratecounter_lookup_count", safeWrap6("ratecounter_lookup_count", func(i *Instance, rc_addr int32, rc_size int32, entry_addr int32, entry_size int32, duration int32, count_out int32) int32 {
		return i.xqd_erl_ratecounter_lookup_count(rc_addr, rc_size, entry_addr, entry_size, uint32(duration), count_out)
	}))
	_ = linker.FuncWrap("fastly_erl", "penaltybox_add", safeWrap5("penaltybox_add", func(i *Instance, pb_addr int32, pb_size int32, entry_addr int32, entry_size int32, ttl int32) int32 {
		return i.xqd_erl_penaltybox_add(pb_addr, pb_size, entry_addr, entry_size, uint32(ttl))
	}))
	_ = linker.FuncWrap("fastly_erl", "penaltybox_has", safeWrap5("penaltybox_has", func(i *Instance, pb_addr int32, pb_size int32, entry_addr int32, entry_size int32, has_out int32) int32 {
		return i.xqd_erl_penaltybox_has(pb_addr, pb_size, entry_addr, entry_size, has_out)
	}))

	// xqd_kv_store.go
	_ = linker.FuncWrap("fastly_kv_store", "open", safeWrap3("open", func(i *Instance, name_addr int32, name_size int32, store_handle_out int32) int32 {
		return i.xqd_kv_store_open(name_addr, name_size, store_handle_out)
	}))
	_ = linker.FuncWrap("fastly_kv_store", "open_v2", safeWrap3("open_v2", func(i *Instance, name_addr int32, name_size int32, store_handle_out int32) int32 {
		return i.xqd_kv_store_open(name_addr, name_size, store_handle_out)
	}))
	_ = linker.FuncWrap("fastly_kv_store", "lookup", safeWrap6("lookup", func(i *Instance, store_handle int32, key_addr int32, key_size int32, lookup_config_mask int32, lookup_config_buf int32, lookup_handle_out int32) int32 {
		return i.xqd_kv_store_lookup(store_handle, key_addr, key_size, lookup_config_mask, lookup_config_buf, lookup_handle_out)
	}))
	_ = linker.FuncWrap("fastly_kv_store", "lookup_v2", safeWrap6("lookup_v2", func(i *Instance, store_handle int32, key_addr int32, key_size int32, lookup_config_mask int32, lookup_config_buf int32, lookup_handle_out int32) int32 {
		return i.xqd_kv_store_lookup(store_handle, key_addr, key_size, lookup_config_mask, lookup_config_buf, lookup_handle_out)
	}))
	_ = linker.FuncWrap("fastly_kv_store", "lookup_wait", safeWrap7("lookup_wait", func(i *Instance, lookup_handle int32, body_handle_out int32, metadata_out int32, metadata_max_len int32, metadata_len_out int32, generation_out int32, kv_error_out int32) int32 {
		return i.xqd_kv_store_lookup_wait(lookup_handle, body_handle_out, metadata_out, metadata_max_len, metadata_len_out, generation_out, kv_error_out)
	}))
	_ = linker.FuncWrap("fastly_kv_store", "lookup_wait_v2", safeWrap7("lookup_wait_v2", func(i *Instance, lookup_handle int32, body_handle_out int32, metadata_out int32, metadata_max_len int32, metadata_len_out int32, generation_out int32, kv_error_out int32) int32 {
		return i.xqd_kv_store_lookup_wait_v2(lookup_handle, body_handle_out, metadata_out, metadata_max_len, metadata_len_out, generation_out, kv_error_out)
	}))
	_ = linker.FuncWrap("fastly_kv_store", "insert", safeWrap7("insert", func(i *Instance, store_handle int32, key_addr int32, key_size int32, body_handle int32, insert_config_mask int32, insert_config_buf int32, insert_handle_out int32) int32 {
		return i.xqd_kv_store_insert(store_handle, key_addr, key_size, body_handle, insert_config_mask, insert_config_buf, insert_handle_out)
	}))
	_ = linker.FuncWrap("fastly_kv_store", "insert_wait", safeWrap2("insert_wait", func(i *Instance, insert_handle int32, kv_error_out int32) int32 {
		return i.xqd_kv_store_insert_wait(insert_handle, kv_error_out)
	}))
	_ = linker.FuncWrap("fastly_kv_store", "delete", safeWrap6("delete", func(i *Instance, store_handle int32, key_addr int32, key_size int32, delete_config_mask int32, delete_config_buf int32, delete_handle_out int32) int32 {
		return i.xqd_kv_store_delete(store_handle, key_addr, key_size, delete_config_mask, delete_config_buf, delete_handle_out)
	}))
	_ = linker.FuncWrap("fastly_kv_store", "delete_wait", safeWrap2("delete_wait", func(i *Instance, delete_handle int32, kv_error_out int32) int32 {
		return i.xqd_kv_store_delete_wait(delete_handle, kv_error_out)
	}))
	_ = linker.FuncWrap("fastly_kv_store", "list", safeWrap4("list", func(i *Instance, store_handle int32, list_config_mask int32, list_config_buf int32, list_handle_out int32) int32 {
		return i.xqd_kv_store_list(store_handle, uint32(list_config_mask), list_config_buf, list_handle_out)
	}))
	_ = linker.FuncWrap("fastly_kv_store", "list_wait", safeWrap3("list_wait", func(i *Instance, list_handle int32, body_handle_out int32, kv_error_out int32) int32 {
		return i.xqd_kv_store_list_wait(list_handle, body_handle_out, kv_error_out)
	}))

	// xqd_backend.go
	_ = linker.FuncWrap("fastly_backend", "exists", safeWrap3("exists", func(i *Instance, backend_addr int32, backend_size int32, exists_out int32) int32 {
		return i.xqd_backend_exists(backend_addr, backend_size, exists_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "is_healthy", safeWrap3("is_healthy", func(i *Instance, backend_addr int32, backend_size int32, health_out int32) int32 {
		return i.xqd_backend_is_healthy(backend_addr, backend_size, health_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "is_dynamic", safeWrap3("is_dynamic", func(i *Instance, backend_addr int32, backend_size int32, is_dynamic_out int32) int32 {
		return i.xqd_backend_is_dynamic(backend_addr, backend_size, is_dynamic_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_host", safeWrap5("get_host", func(i *Instance, backend_addr int32, backend_size int32, value_out int32, value_max_len int32, nwritten_out int32) int32 {
		return i.xqd_backend_get_host(backend_addr, backend_size, value_out, value_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_override_host", safeWrap5("get_override_host", func(i *Instance, backend_addr int32, backend_size int32, value_out int32, value_max_len int32, nwritten_out int32) int32 {
		return i.xqd_backend_get_override_host(backend_addr, backend_size, value_out, value_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_port", safeWrap3("get_port", func(i *Instance, backend_addr int32, backend_size int32, port_out int32) int32 {
		return i.xqd_backend_get_port(backend_addr, backend_size, port_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_connect_timeout_ms", safeWrap3("get_connect_timeout_ms", func(i *Instance, backend_addr int32, backend_size int32, timeout_out int32) int32 {
		return i.xqd_backend_get_connect_timeout_ms(backend_addr, backend_size, timeout_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_first_byte_timeout_ms", safeWrap3("get_first_byte_timeout_ms", func(i *Instance, backend_addr int32, backend_size int32, timeout_out int32) int32 {
		return i.xqd_backend_get_first_byte_timeout_ms(backend_addr, backend_size, timeout_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_between_bytes_timeout_ms", safeWrap3("get_between_bytes_timeout_ms", func(i *Instance, backend_addr int32, backend_size int32, timeout_out int32) int32 {
		return i.xqd_backend_get_between_bytes_timeout_ms(backend_addr, backend_size, timeout_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "is_ssl", safeWrap3("is_ssl", func(i *Instance, backend_addr int32, backend_size int32, is_ssl_out int32) int32 {
		return i.xqd_backend_is_ssl(backend_addr, backend_size, is_ssl_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_ssl_min_version", safeWrap3("get_ssl_min_version", func(i *Instance, backend_addr int32, backend_size int32, version_out int32) int32 {
		return i.xqd_backend_get_ssl_min_version(backend_addr, backend_size, version_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_ssl_max_version", safeWrap3("get_ssl_max_version", func(i *Instance, backend_addr int32, backend_size int32, version_out int32) int32 {
		return i.xqd_backend_get_ssl_max_version(backend_addr, backend_size, version_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_http_keepalive_time", safeWrap3("get_http_keepalive_time", func(i *Instance, backend_addr int32, backend_size int32, timeout_out int32) int32 {
		return i.xqd_backend_get_http_keepalive_time(backend_addr, backend_size, timeout_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_tcp_keepalive_enable", safeWrap3("get_tcp_keepalive_enable", func(i *Instance, backend_addr int32, backend_size int32, is_enabled_out int32) int32 {
		return i.xqd_backend_get_tcp_keepalive_enable(backend_addr, backend_size, is_enabled_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_tcp_keepalive_interval", safeWrap3("get_tcp_keepalive_interval", func(i *Instance, backend_addr int32, backend_size int32, timeout_out int32) int32 {
		return i.xqd_backend_get_tcp_keepalive_interval(backend_addr, backend_size, timeout_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_tcp_keepalive_probes", safeWrap3("get_tcp_keepalive_probes", func(i *Instance, backend_addr int32, backend_size int32, probes_out int32) int32 {
		return i.xqd_backend_get_tcp_keepalive_probes(backend_addr, backend_size, probes_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_tcp_keepalive_time", safeWrap3("get_tcp_keepalive_time", func(i *Instance, backend_addr int32, backend_size int32, timeout_out int32) int32 {
		return i.xqd_backend_get_tcp_keepalive_time(backend_addr, backend_size, timeout_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "is_ipv6_preferred", safeWrap3("is_ipv6_preferred", func(i *Instance, backend_addr int32, backend_size int32, is_preferred_out int32) int32 {
		return i.xqd_backend_is_ipv6_preferred(backend_addr, backend_size, is_preferred_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_max_connections", safeWrap3("get_max_connections", func(i *Instance, backend_addr int32, backend_size int32, max_out int32) int32 {
		return i.xqd_backend_get_max_connections(backend_addr, backend_size, max_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_max_use", safeWrap3("get_max_use", func(i *Instance, backend_addr int32, backend_size int32, max_out int32) int32 {
		return i.xqd_backend_get_max_use(backend_addr, backend_size, max_out)
	}))
	_ = linker.FuncWrap("fastly_backend", "get_max_lifetime_ms", safeWrap3("get_max_lifetime_ms", func(i *Instance, backend_addr int32, backend_size int32, max_out int32) int32 {
		return i.xqd_backend_get_max_lifetime_ms(backend_addr, backend_size, max_out)
	}))

	// xqd_compute_runtime.go
	_ = linker.FuncWrap("fastly_compute_runtime", "get_vcpu_ms", safeWrap1("get_vcpu_ms", func(i *Instance, vcpu_time_ms_out int32) int32 {
		return i.xqd_compute_runtime_get_vcpu_ms(vcpu_time_ms_out)
	}))
	_ = linker.FuncWrap("fastly_compute_runtime", "get_heap_mib", safeWrap1("get_heap_mib", func(i *Instance, heap_mib_out int32) int32 {
		return i.xqd_compute_runtime_get_heap_mib(heap_mib_out)
	}))
	_ = linker.FuncWrap("fastly_compute_runtime", "get_sandbox_id", safeWrap3("get_sandbox_id", func(i *Instance, sandbox_id_out int32, sandbox_id_max_len int32, nwritten_out int32) int32 {
		return i.xqd_compute_runtime_get_sandbox_id(sandbox_id_out, sandbox_id_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_compute_runtime", "get_trace_id", safeWrap3("get_trace_id", func(i *Instance, trace_id_out int32, trace_id_max_len int32, nwritten_out int32) int32 {
		return i.xqd_compute_runtime_get_trace_id(trace_id_out, trace_id_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_compute_runtime", "get_service_version", safeWrap3("get_service_version", func(i *Instance, version_out int32, version_max_len int32, nwritten_out int32) int32 {
		return i.xqd_compute_runtime_get_service_version(version_out, version_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_compute_runtime", "get_hostname", safeWrap3("get_hostname", func(i *Instance, hostname_out int32, hostname_max_len int32, nwritten_out int32) int32 {
		return i.xqd_compute_runtime_get_hostname(hostname_out, hostname_max_len, nwritten_out)
	}))

	// xqd_cache.go
	_ = linker.FuncWrap("fastly_cache", "lookup", safeWrap5("lookup", func(i *Instance, cache_key int32, cache_key_len int32, options_mask int32, options int32, handle_out int32) int32 {
		return i.xqd_cache_lookup(cache_key, cache_key_len, uint32(options_mask), options, handle_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "insert", safeWrap5("insert", func(i *Instance, cache_key int32, cache_key_len int32, options_mask int32, options int32, body_handle_out int32) int32 {
		return i.xqd_cache_insert(cache_key, cache_key_len, uint32(options_mask), options, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "transaction_lookup", safeWrap5("transaction_lookup", func(i *Instance, cache_key int32, cache_key_len int32, options_mask int32, options int32, cache_handle_out int32) int32 {
		return i.xqd_cache_transaction_lookup(cache_key, cache_key_len, uint32(options_mask), options, cache_handle_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "transaction_lookup_async", safeWrap5("transaction_lookup_async", func(i *Instance, cache_key int32, cache_key_len int32, options_mask int32, options int32, cache_busy_handle_out int32) int32 {
		return i.xqd_cache_transaction_lookup_async(cache_key, cache_key_len, uint32(options_mask), options, cache_busy_handle_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "cache_busy_handle_wait", safeWrap2("cache_busy_handle_wait", func(i *Instance, busy_handle int32, cache_handle_out int32) int32 {
		return i.xqd_cache_busy_handle_wait(busy_handle, cache_handle_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "transaction_insert", safeWrap4("transaction_insert", func(i *Instance, cache_handle int32, options_mask int32, options int32, body_handle_out int32) int32 {
		return i.xqd_cache_transaction_insert(cache_handle, uint32(options_mask), options, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "transaction_insert_and_stream_back", safeWrap5("transaction_insert_and_stream_back", func(i *Instance, cache_handle int32, options_mask int32, options int32, body_handle_out int32, cache_handle_out int32) int32 {
		return i.xqd_cache_transaction_insert_and_stream_back(cache_handle, uint32(options_mask), options, body_handle_out, cache_handle_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "transaction_update", safeWrap3("transaction_update", func(i *Instance, cache_handle int32, options_mask int32, options int32) int32 {
		return i.xqd_cache_transaction_update(cache_handle, uint32(options_mask), options)
	}))
	_ = linker.FuncWrap("fastly_cache", "transaction_cancel", safeWrap1("transaction_cancel", func(i *Instance, cache_handle int32) int32 {
		return i.xqd_cache_transaction_cancel(cache_handle)
	}))
	_ = linker.FuncWrap("fastly_cache", "close_busy", safeWrap1("close_busy", func(i *Instance, busy_handle int32) int32 {
		return i.xqd_cache_close_busy(busy_handle)
	}))
	_ = linker.FuncWrap("fastly_cache", "close", safeWrap1("close", func(i *Instance, cache_handle int32) int32 {
		return i.xqd_cache_close(cache_handle)
	}))
	_ = linker.FuncWrap("fastly_cache", "get_state", safeWrap2("get_state", func(i *Instance, cache_handle int32, state_out int32) int32 {
		return i.xqd_cache_get_state(cache_handle, state_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "get_user_metadata", safeWrap4("get_user_metadata", func(i *Instance, cache_handle int32, user_metadata_out int32, user_metadata_out_len int32, nwritten int32) int32 {
		return i.xqd_cache_get_user_metadata(cache_handle, user_metadata_out, user_metadata_out_len, nwritten)
	}))
	_ = linker.FuncWrap("fastly_cache", "get_body", safeWrap4("get_body", func(i *Instance, cache_handle int32, options_mask int32, options int32, body_handle_out int32) int32 {
		return i.xqd_cache_get_body(cache_handle, uint32(options_mask), options, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "get_length", safeWrap2("get_length", func(i *Instance, cache_handle int32, length_out int32) int32 {
		return i.xqd_cache_get_length(cache_handle, length_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "get_max_age_ns", safeWrap2("get_max_age_ns", func(i *Instance, cache_handle int32, max_age_ns_out int32) int32 {
		return i.xqd_cache_get_max_age_ns(cache_handle, max_age_ns_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "get_stale_while_revalidate_ns", safeWrap2("get_stale_while_revalidate_ns", func(i *Instance, cache_handle int32, stale_while_revalidate_ns_out int32) int32 {
		return i.xqd_cache_get_stale_while_revalidate_ns(cache_handle, stale_while_revalidate_ns_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "get_age_ns", safeWrap2("get_age_ns", func(i *Instance, cache_handle int32, age_ns_out int32) int32 {
		return i.xqd_cache_get_age_ns(cache_handle, age_ns_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "get_hits", safeWrap2("get_hits", func(i *Instance, cache_handle int32, hits_out int32) int32 {
		return i.xqd_cache_get_hits(cache_handle, hits_out)
	}))
	// Cache replace API (stubs - not implemented, returns XqdErrUnsupported like Viceroy)
	_ = linker.FuncWrap("fastly_cache", "replace", safeWrap5("replace", func(i *Instance, cache_key int32, cache_key_len int32, options_mask int32, options int32, replace_handle_out int32) int32 {
		return i.xqd_cache_replace(cache_key, cache_key_len, uint32(options_mask), options, replace_handle_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "replace_insert", safeWrap4("replace_insert", func(i *Instance, replace_handle int32, options_mask int32, options int32, body_handle_out int32) int32 {
		return i.xqd_cache_replace_insert(replace_handle, uint32(options_mask), options, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "replace_get_age_ns", safeWrap2("replace_get_age_ns", func(i *Instance, replace_handle int32, age_ns_out int32) int32 {
		return i.xqd_cache_replace_get_age_ns(replace_handle, age_ns_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "replace_get_body", safeWrap4("replace_get_body", func(i *Instance, replace_handle int32, options_mask int32, options int32, body_handle_out int32) int32 {
		return i.xqd_cache_replace_get_body(replace_handle, uint32(options_mask), options, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "replace_get_hits", safeWrap2("replace_get_hits", func(i *Instance, replace_handle int32, hits_out int32) int32 {
		return i.xqd_cache_replace_get_hits(replace_handle, hits_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "replace_get_length", safeWrap2("replace_get_length", func(i *Instance, replace_handle int32, length_out int32) int32 {
		return i.xqd_cache_replace_get_length(replace_handle, length_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "replace_get_max_age_ns", safeWrap2("replace_get_max_age_ns", func(i *Instance, replace_handle int32, max_age_ns_out int32) int32 {
		return i.xqd_cache_replace_get_max_age_ns(replace_handle, max_age_ns_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "replace_get_stale_while_revalidate_ns", safeWrap2("replace_get_stale_while_revalidate_ns", func(i *Instance, replace_handle int32, stale_while_revalidate_ns_out int32) int32 {
		return i.xqd_cache_replace_get_stale_while_revalidate_ns(replace_handle, stale_while_revalidate_ns_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "replace_get_state", safeWrap2("replace_get_state", func(i *Instance, replace_handle int32, state_out int32) int32 {
		return i.xqd_cache_replace_get_state(replace_handle, state_out)
	}))
	_ = linker.FuncWrap("fastly_cache", "replace_get_user_metadata", safeWrap4("replace_get_user_metadata", func(i *Instance, replace_handle int32, user_metadata_out int32, user_metadata_out_len int32, nwritten int32) int32 {
		return i.xqd_cache_replace_get_user_metadata(replace_handle, user_metadata_out, user_metadata_out_len, nwritten)
	}))

	// xqd_purge.go
	_ = linker.FuncWrap("fastly_purge", "purge_surrogate_key", safeWrap4("purge_surrogate_key", func(i *Instance, surrogate_key int32, surrogate_key_len int32, options_mask int32, options int32) int32 {
		return i.xqd_purge_surrogate_key(surrogate_key, surrogate_key_len, uint32(options_mask), options)
	}))

	// xqd_async_io.go
	_ = linker.FuncWrap("fastly_async_io", "select", safeWrap4("select", func(i *Instance, handles_addr int32, handles_len int32, timeout_ms int32, ready_idx_out int32) int32 {
		return i.xqd_async_io_select(handles_addr, handles_len, timeout_ms, ready_idx_out)
	}))
	_ = linker.FuncWrap("fastly_async_io", "is_ready", safeWrap2("is_ready", func(i *Instance, handle int32, is_ready_out int32) int32 {
		return i.xqd_async_io_is_ready(handle, is_ready_out)
	}))

	// xqd_http_downstream.go
	_ = linker.FuncWrap("fastly_http_downstream", "next_request", safeWrap3("next_request", func(i *Instance, options_mask int32, options_ptr int32, handle_out int32) int32 {
		return i.xqd_http_downstream_next_request(options_mask, options_ptr, handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "next_request_wait", safeWrap3("next_request_wait", func(i *Instance, promise_handle int32, req_handle_out int32, body_handle_out int32) int32 {
		return i.xqd_http_downstream_next_request_wait(promise_handle, req_handle_out, body_handle_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "next_request_abandon", safeWrap1("next_request_abandon", func(i *Instance, promise_handle int32) int32 {
		return i.xqd_http_downstream_next_request_abandon(promise_handle)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_original_header_names", safeWrap6("downstream_original_header_names", func(i *Instance, request_handle int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_original_header_names(request_handle, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_original_header_count", safeWrap2("downstream_original_header_count", func(i *Instance, request_handle int32, count_out int32) int32 {
		return i.xqd_http_downstream_original_header_count(request_handle, count_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_cipher_openssl_name", safeWrap4("downstream_tls_cipher_openssl_name", func(i *Instance, req_handle int32, addr int32, maxlen int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_cipher_openssl_name(req_handle, addr, maxlen, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_protocol", safeWrap4("downstream_tls_protocol", func(i *Instance, req_handle int32, addr int32, maxlen int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_protocol(req_handle, addr, maxlen, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_client_servername", safeWrap4("downstream_tls_client_servername", func(i *Instance, req_handle int32, addr int32, maxlen int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_client_servername(req_handle, addr, maxlen, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_client_hello", safeWrap4("downstream_tls_client_hello", func(i *Instance, req_handle int32, addr int32, maxlen int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_client_hello(req_handle, addr, maxlen, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_raw_client_certificate", safeWrap4("downstream_tls_raw_client_certificate", func(i *Instance, req_handle int32, cert_out int32, cert_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_raw_client_certificate(req_handle, cert_out, cert_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_client_cert_verify_result", safeWrap2("downstream_tls_client_cert_verify_result", func(i *Instance, req_handle int32, verify_result_out int32) int32 {
		return i.xqd_http_downstream_tls_client_cert_verify_result(req_handle, verify_result_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_client_h2_fingerprint", safeWrap4("downstream_client_h2_fingerprint", func(i *Instance, req_handle int32, h2_out int32, h2_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_client_h2_fingerprint(req_handle, h2_out, h2_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_client_oh_fingerprint", safeWrap4("downstream_client_oh_fingerprint", func(i *Instance, req_handle int32, oh_out int32, oh_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_client_oh_fingerprint(req_handle, oh_out, oh_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_ja3_md5", safeWrap3("downstream_tls_ja3_md5", func(i *Instance, req_handle int32, ja3_md5_out int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_ja3_md5(req_handle, ja3_md5_out, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_tls_ja4", safeWrap4("downstream_tls_ja4", func(i *Instance, req_handle int32, ja4_out int32, ja4_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_tls_ja4(req_handle, ja4_out, ja4_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_client_request_id", safeWrap4("downstream_client_request_id", func(i *Instance, req_handle int32, reqid_out int32, reqid_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_client_request_id(req_handle, reqid_out, reqid_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_client_ddos_detected", safeWrap2("downstream_client_ddos_detected", func(i *Instance, req_handle int32, ddos_detected_out int32) int32 {
		return i.xqd_http_downstream_client_ddos_detected(req_handle, ddos_detected_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_compliance_region", safeWrap4("downstream_compliance_region", func(i *Instance, req_handle int32, region_out int32, region_max_len int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_compliance_region(req_handle, region_out, region_max_len, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_client_ip_addr", safeWrap3("downstream_client_ip_addr", func(i *Instance, req_handle int32, addr_octets_out int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_client_ip_addr(req_handle, addr_octets_out, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "downstream_server_ip_addr", safeWrap3("downstream_server_ip_addr", func(i *Instance, req_handle int32, addr_octets_out int32, nwritten_out int32) int32 {
		return i.xqd_http_downstream_server_ip_addr(req_handle, addr_octets_out, nwritten_out)
	}))
	_ = linker.FuncWrap("fastly_http_downstream", "fastly_key_is_valid", safeWrap2("fastly_key_is_valid", func(i *Instance, req_handle int32, is_valid_out int32) int32 {
		return i.xqd_http_downstream_fastly_key_is_valid(req_handle, is_valid_out)
	}))
}

// linklegacy binds legacy XQD ABI functions using old naming conventions (xqd_*, env module).
//
// This provides backwards compatibility with older wasm programs that were compiled against
// the legacy ABI naming scheme. Modern programs use the fastly_* naming scheme (linked in link()).
//
// The implementations are the same; only the module names and function names differ.
// The linker is created once at compile time and shared across all requests.
func linklegacy(linker *wasmtime.Linker) {
	// Additional legacy HTTP request/response functions
	_ = linker.FuncWrap("env", "xqd_req_original_header_count", safeWrap1("xqd_req_original_header_count", func(i *Instance, a int32) int32 {
		return i.wasm1("xqd_req_original_header_count")(a)
	}))

	_ = linker.FuncWrap("env", "xqd_resp_header_value_get", safeWrap6("xqd_resp_header_value_get", func(i *Instance, a, b, c, d, e, f int32) int32 {
		return i.wasm6("xqd_resp_header_value_get")(a, b, c, d, e, f)
	}))

	_ = linker.FuncWrap("env", "xqd_body_close_downstream", safeWrap1("xqd_body_close_downstream", func(i *Instance, a int32) int32 {
		return i.xqd_body_close(a)
	}))

	// xqd.go
	_ = linker.FuncWrap("fastly", "init", safeWrap1i64("init", func(i *Instance, abiv int64) int32 {
		// Add panic recovery to work around wasmtime-go v37 bug
		return i.xqd_init(abiv)
	}))
	_ = linker.FuncWrap("fastly_uap", "parse", safeWrap14("parse", func(i *Instance, user_agent int32, user_agent_len int32, family int32, family_max_len int32, family_written int32, major int32, major_max_len int32, major_written int32, minor int32, minor_max_len int32, minor_written int32, patch int32, patch_max_len int32, patch_written int32) int32 {
		return i.xqd_uap_parse(user_agent, user_agent_len, family, family_max_len, family_written, major, major_max_len, major_written, minor, minor_max_len, minor_written, patch, patch_max_len, patch_written)
	}))

	_ = linker.FuncWrap("env", "xqd_req_body_downstream_get", safeWrap2("xqd_req_body_downstream_get", func(i *Instance, request_handle_out int32, body_handle_out int32) int32 {
		return i.xqd_req_body_downstream_get(request_handle_out, body_handle_out)
	}))
	_ = linker.FuncWrap("env", "xqd_resp_send_downstream", safeWrap3("xqd_resp_send_downstream", func(i *Instance, resp_handle int32, body_handle int32, streaming int32) int32 {
		return i.xqd_resp_send_downstream(resp_handle, body_handle, streaming)
	}))
	_ = linker.FuncWrap("env", "xqd_req_downstream_client_ip_addr", safeWrap2("xqd_req_downstream_client_ip_addr", func(i *Instance, octets_out int32, nwritten_out int32) int32 {
		return i.xqd_req_downstream_client_ip_addr(octets_out, nwritten_out)
	}))

	// xqd_request.go
	_ = linker.FuncWrap("env", "xqd_req_new", safeWrap1("xqd_req_new", func(i *Instance, handle_out int32) int32 {
		// Add panic recovery to work around wasmtime-go v37 bug
		return i.xqd_req_new(handle_out)
	}))
	_ = linker.FuncWrap("env", "xqd_req_version_get", safeWrap2("xqd_req_version_get", func(i *Instance, handle int32, version_out int32) int32 {
		return i.xqd_req_version_get(handle, version_out)
	}))
	_ = linker.FuncWrap("env", "xqd_req_version_set", safeWrap2("xqd_req_version_set", func(i *Instance, handle int32, version int32) int32 {
		return i.xqd_req_version_set(handle, version)
	}))
	_ = linker.FuncWrap("env", "xqd_req_method_get", safeWrap4("xqd_req_method_get", func(i *Instance, a int32, b int32, c int32, d int32) int32 { return i.xqd_req_method_get(a, b, c, d) }))
	_ = linker.FuncWrap("env", "xqd_req_method_set", safeWrap3("xqd_req_method_set", func(i *Instance, a int32, b int32, c int32) int32 { return i.xqd_req_method_set(a, b, c) }))
	_ = linker.FuncWrap("env", "xqd_req_uri_get", safeWrap4("xqd_req_uri_get", func(i *Instance, a int32, b int32, c int32, d int32) int32 { return i.xqd_req_uri_get(a, b, c, d) }))
	_ = linker.FuncWrap("env", "xqd_req_uri_set", safeWrap3("xqd_req_uri_set", func(i *Instance, a int32, b int32, c int32) int32 { return i.xqd_req_uri_set(a, b, c) }))
	_ = linker.FuncWrap("env", "xqd_req_header_remove", safeWrap3("xqd_req_header_remove", func(i *Instance, a int32, b int32, c int32) int32 { return i.xqd_req_header_remove(a, b, c) }))
	_ = linker.FuncWrap("env", "xqd_req_header_insert", safeWrap5("xqd_req_header_insert", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_req_header_insert(a, b, c, d, e)
	}))
	_ = linker.FuncWrap("env", "xqd_req_header_append", safeWrap5("xqd_req_header_append", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_req_header_append(a, b, c, d, e)
	}))
	_ = linker.FuncWrap("env", "xqd_req_header_names_get", safeWrap6("xqd_req_header_names_get", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32) int32 {
		return i.xqd_req_header_names_get(a, b, c, d, e, f)
	}))
	_ = linker.FuncWrap("env", "xqd_req_header_value_get", safeWrap6("xqd_req_header_value_get", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32) int32 {
		return i.xqd_req_header_value_get(a, b, c, d, e, f)
	}))
	_ = linker.FuncWrap("env", "xqd_req_header_values_get", safeWrap8("xqd_req_header_values_get", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32, g int32, h int32) int32 {
		return i.xqd_req_header_values_get(a, b, c, d, e, f, g, h)
	}))
	_ = linker.FuncWrap("env", "xqd_req_header_values_set", safeWrap5("xqd_req_header_values_set", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_req_header_values_set(a, b, c, d, e)
	}))
	_ = linker.FuncWrap("env", "xqd_req_send", safeWrap6("xqd_req_send", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32) int32 {
		return i.xqd_req_send(a, b, c, d, e, f)
	}))
	_ = linker.FuncWrap("env", "xqd_req_send_v2", safeWrap7("xqd_req_send_v2", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32, g int32) int32 {
		return i.xqd_req_send_v2(a, b, c, d, e, f, g)
	}))
	_ = linker.FuncWrap("env", "xqd_req_send_v3", safeWrap7("xqd_req_send_v3", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32, g int32) int32 {
		return i.xqd_req_send_v3(a, b, c, d, e, f, g)
	}))
	_ = linker.FuncWrap("env", "xqd_req_send_async", safeWrap5("xqd_req_send_async", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_req_send_async(a, b, c, d, e)
	}))
	_ = linker.FuncWrap("env", "xqd_req_send_async_streaming", safeWrap5("xqd_req_send_async_streaming", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_req_send_async_streaming(a, b, c, d, e)
	}))
	_ = linker.FuncWrap("env", "xqd_req_send_async_v2", safeWrap6("xqd_req_send_async_v2", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32) int32 {
		return i.xqd_req_send_async_v2(a, b, c, d, e, f)
	}))
	_ = linker.FuncWrap("env", "xqd_pending_req_poll", safeWrap4("xqd_pending_req_poll", func(i *Instance, a int32, b int32, c int32, d int32) int32 { return i.xqd_pending_req_poll(a, b, c, d) }))
	_ = linker.FuncWrap("env", "xqd_pending_req_poll_v2", safeWrap5("xqd_pending_req_poll_v2", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_pending_req_poll_v2(a, b, c, d, e)
	}))
	_ = linker.FuncWrap("env", "xqd_pending_req_wait", safeWrap3("xqd_pending_req_wait", func(i *Instance, a int32, b int32, c int32) int32 { return i.xqd_pending_req_wait(a, b, c) }))
	_ = linker.FuncWrap("env", "xqd_pending_req_wait_v2", safeWrap4("xqd_pending_req_wait_v2", func(i *Instance, a int32, b int32, c int32, d int32) int32 {
		return i.xqd_pending_req_wait_v2(a, b, c, d)
	}))
	_ = linker.FuncWrap("env", "xqd_pending_req_select", safeWrap5("xqd_pending_req_select", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_pending_req_select(a, b, c, d, e)
	}))
	_ = linker.FuncWrap("env", "xqd_pending_req_select_v2", safeWrap6("xqd_pending_req_select_v2", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32) int32 {
		return i.xqd_pending_req_select_v2(a, b, c, d, e, f)
	}))
	_ = linker.FuncWrap("env", "xqd_req_cache_override_set", safeWrap4("xqd_req_cache_override_set", func(i *Instance, a int32, b int32, c int32, d int32) int32 {
		return i.xqd_req_cache_override_set(a, b, c, d)
	}))
	_ = linker.FuncWrap("env", "xqd_req_cache_override_v2_set", safeWrap6("xqd_req_cache_override_v2_set", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32) int32 {
		return i.xqd_req_cache_override_v2_set(a, b, c, d, e, f)
	}))
	// The Go http implementation doesn't make it easy to get at the original headers in order, so
	// we just use the same sorted order
	_ = linker.FuncWrap("env", "xqd_req_original_header_names_get", safeWrap6("xqd_req_original_header_names_get", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32) int32 {
		return i.xqd_req_header_names_get(a, b, c, d, e, f)
	}))
	_ = linker.FuncWrap("env", "xqd_req_close", safeWrap1("xqd_req_close", func(i *Instance, a int32) int32 { return i.xqd_req_close(a) }))
	_ = linker.FuncWrap("env", "xqd_req_downstream_client_ddos_detected", safeWrap1("xqd_req_downstream_client_ddos_detected", func(i *Instance, a int32) int32 { return i.xqd_req_downstream_client_ddos_detected(a) }))
	_ = linker.FuncWrap("env", "xqd_req_fastly_key_is_valid", safeWrap1("xqd_req_fastly_key_is_valid", func(i *Instance, a int32) int32 { return i.xqd_req_fastly_key_is_valid(a) }))
	_ = linker.FuncWrap("env", "xqd_req_downstream_compliance_region", safeWrap3("xqd_req_downstream_compliance_region", func(i *Instance, a int32, b int32, c int32) int32 {
		return i.xqd_req_downstream_compliance_region(a, b, c)
	}))
	_ = linker.FuncWrap("env", "xqd_req_on_behalf_of", safeWrap3("xqd_req_on_behalf_of", func(i *Instance, a int32, b int32, c int32) int32 { return i.xqd_req_on_behalf_of(a, b, c) }))
	_ = linker.FuncWrap("env", "xqd_req_framing_headers_mode_set", safeWrap2("xqd_req_framing_headers_mode_set", func(i *Instance, a int32, b int32) int32 { return i.xqd_req_framing_headers_mode_set(a, b) }))
	_ = linker.FuncWrap("env", "xqd_req_auto_decompress_response_set", safeWrap2("xqd_req_auto_decompress_response_set", func(i *Instance, a int32, b int32) int32 { return i.xqd_req_auto_decompress_response_set(a, b) }))
	_ = linker.FuncWrap("env", "xqd_req_register_dynamic_backend", safeWrap6("xqd_req_register_dynamic_backend", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32) int32 {
		return i.xqd_req_register_dynamic_backend(a, b, c, d, e, f)
	}))

	// xqd_response.go
	_ = linker.FuncWrap("env", "xqd_resp_new", safeWrap1("xqd_resp_new", func(i *Instance, a int32) int32 { return i.xqd_resp_new(a) }))
	_ = linker.FuncWrap("env", "xqd_resp_status_get", safeWrap2("xqd_resp_status_get", func(i *Instance, a int32, b int32) int32 { return i.xqd_resp_status_get(a, b) }))
	_ = linker.FuncWrap("env", "xqd_resp_status_set", safeWrap2("xqd_resp_status_set", func(i *Instance, a int32, b int32) int32 { return i.xqd_resp_status_set(a, b) }))
	_ = linker.FuncWrap("env", "xqd_resp_version_get", safeWrap2("xqd_resp_version_get", func(i *Instance, a int32, b int32) int32 { return i.xqd_resp_version_get(a, b) }))
	_ = linker.FuncWrap("env", "xqd_resp_version_set", safeWrap2("xqd_resp_version_set", func(i *Instance, a int32, b int32) int32 { return i.xqd_resp_version_set(a, b) }))
	_ = linker.FuncWrap("env", "xqd_resp_header_remove", safeWrap3("xqd_resp_header_remove", func(i *Instance, a int32, b int32, c int32) int32 { return i.xqd_resp_header_remove(a, b, c) }))
	_ = linker.FuncWrap("env", "xqd_resp_header_insert", safeWrap5("xqd_resp_header_insert", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_resp_header_insert(a, b, c, d, e)
	}))
	_ = linker.FuncWrap("env", "xqd_resp_header_append", safeWrap5("xqd_resp_header_append", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_resp_header_append(a, b, c, d, e)
	}))
	_ = linker.FuncWrap("env", "xqd_resp_header_names_get", safeWrap6("xqd_resp_header_names_get", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32) int32 {
		return i.xqd_resp_header_names_get(a, b, c, d, e, f)
	}))
	_ = linker.FuncWrap("env", "xqd_resp_header_values_get", safeWrap8("xqd_resp_header_values_get", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32, g int32, h int32) int32 {
		return i.xqd_resp_header_values_get(a, b, c, d, e, f, g, h)
	}))
	_ = linker.FuncWrap("env", "xqd_resp_header_values_set", safeWrap5("xqd_resp_header_values_set", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_resp_header_values_set(a, b, c, d, e)
	}))
	_ = linker.FuncWrap("env", "xqd_resp_close", safeWrap1("xqd_resp_close", func(i *Instance, a int32) int32 { return i.xqd_resp_close(a) }))
	_ = linker.FuncWrap("env", "xqd_resp_framing_headers_mode_set", safeWrap2("xqd_resp_framing_headers_mode_set", func(i *Instance, a int32, b int32) int32 { return i.xqd_resp_framing_headers_mode_set(a, b) }))
	_ = linker.FuncWrap("env", "xqd_resp_http_keepalive_mode_set", safeWrap2("xqd_resp_http_keepalive_mode_set", func(i *Instance, a int32, b int32) int32 { return i.xqd_resp_http_keepalive_mode_set(a, b) }))
	_ = linker.FuncWrap("env", "xqd_resp_get_addr_dest_ip", safeWrap3("xqd_resp_get_addr_dest_ip", func(i *Instance, a int32, b int32, c int32) int32 { return i.xqd_resp_get_addr_dest_ip(a, b, c) }))
	_ = linker.FuncWrap("env", "xqd_resp_get_addr_dest_port", safeWrap2("xqd_resp_get_addr_dest_port", func(i *Instance, a int32, b int32) int32 { return i.xqd_resp_get_addr_dest_port(a, b) }))

	// xqd_body.go
	_ = linker.FuncWrap("env", "xqd_body_new", safeWrap1("xqd_body_new", func(i *Instance, a int32) int32 { return i.xqd_body_new(a) }))
	_ = linker.FuncWrap("env", "xqd_body_write", safeWrap5("xqd_body_write", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_body_write(a, b, c, d, e)
	}))
	_ = linker.FuncWrap("env", "xqd_body_read", safeWrap4("xqd_body_read", func(i *Instance, a int32, b int32, c int32, d int32) int32 { return i.xqd_body_read(a, b, c, d) }))
	_ = linker.FuncWrap("env", "xqd_body_append", safeWrap2("xqd_body_append", func(i *Instance, a int32, b int32) int32 { return i.xqd_body_append(a, b) }))
	_ = linker.FuncWrap("env", "xqd_body_abandon", safeWrap1("xqd_body_abandon", func(i *Instance, a int32) int32 { return i.xqd_body_abandon(a) }))
	_ = linker.FuncWrap("env", "xqd_body_known_length", safeWrap2("xqd_body_known_length", func(i *Instance, a int32, b int32) int32 { return i.xqd_body_known_length(a, b) }))
	_ = linker.FuncWrap("env", "xqd_body_trailer_append", safeWrap5("xqd_body_trailer_append", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_body_trailer_append(a, b, c, d, e)
	}))
	_ = linker.FuncWrap("env", "xqd_body_trailer_names_get", safeWrap6("xqd_body_trailer_names_get", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32) int32 {
		return i.xqd_body_trailer_names_get(a, b, c, d, e, f)
	}))
	_ = linker.FuncWrap("env", "xqd_body_trailer_value_get", safeWrap6("xqd_body_trailer_value_get", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32) int32 {
		return i.xqd_body_trailer_value_get(a, b, c, d, e, f)
	}))
	_ = linker.FuncWrap("env", "xqd_body_trailer_values_get", safeWrap8("xqd_body_trailer_values_get", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32, g int32, h int32) int32 {
		return i.xqd_body_trailer_values_get(a, b, c, d, e, f, g, h)
	}))

	// xqd_log.go
	_ = linker.FuncWrap("env", "xqd_log_endpoint_get", safeWrap3("xqd_log_endpoint_get", func(i *Instance, a int32, b int32, c int32) int32 { return i.xqd_log_endpoint_get(a, b, c) }))
	_ = linker.FuncWrap("env", "xqd_log_write", safeWrap4("xqd_log_write", func(i *Instance, a int32, b int32, c int32, d int32) int32 { return i.xqd_log_write(a, b, c, d) }))

	// xqd_image_optimizer.go
	_ = linker.FuncWrap("env", "xqd_image_optimizer_transform_request", safeWrap9("xqd_image_optimizer_transform_request", func(i *Instance, a, b, c, d int32, e int32, f, g, h, j int32) int32 {
		return i.xqd_image_optimizer_transform_request(a, b, c, d, uint32(e), f, g, h, j)
	}))

	// xqd_acl.go
	_ = linker.FuncWrap("env", "xqd_acl_open", safeWrap3("xqd_acl_open", func(i *Instance, a int32, b int32, c int32) int32 { return i.xqd_acl_open(a, b, c) }))
	_ = linker.FuncWrap("env", "xqd_acl_lookup", safeWrap5("xqd_acl_lookup", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_acl_lookup(a, b, c, d, e)
	}))

	// xqd_erl.go
	_ = linker.FuncWrap("env", "xqd_erl_check_rate", safeWrap11("xqd_erl_check_rate", func(i *Instance, rc_addr, rc_size, entry_addr, entry_size int32, delta, window, limit int32, pb_addr, pb_size int32, ttl int32, blocked_out int32) int32 {
		return i.xqd_erl_check_rate(rc_addr, rc_size, entry_addr, entry_size, uint32(delta), uint32(window), uint32(limit), pb_addr, pb_size, uint32(ttl), blocked_out)
	}))
	_ = linker.FuncWrap("env", "xqd_erl_ratecounter_increment", safeWrap5("xqd_erl_ratecounter_increment", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_erl_ratecounter_increment(a, b, c, d, uint32(e))
	}))
	_ = linker.FuncWrap("env", "xqd_erl_ratecounter_lookup_rate", safeWrap6("xqd_erl_ratecounter_lookup_rate", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32) int32 {
		return i.xqd_erl_ratecounter_lookup_rate(a, b, c, d, uint32(e), f)
	}))
	_ = linker.FuncWrap("env", "xqd_erl_ratecounter_lookup_count", safeWrap6("xqd_erl_ratecounter_lookup_count", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32) int32 {
		return i.xqd_erl_ratecounter_lookup_count(a, b, c, d, uint32(e), f)
	}))
	_ = linker.FuncWrap("env", "xqd_erl_penaltybox_add", safeWrap5("xqd_erl_penaltybox_add", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_erl_penaltybox_add(a, b, c, d, uint32(e))
	}))
	_ = linker.FuncWrap("env", "xqd_erl_penaltybox_has", safeWrap5("xqd_erl_penaltybox_has", func(i *Instance, a int32, b int32, c int32, d int32, e int32) int32 {
		return i.xqd_erl_penaltybox_has(a, b, c, d, e)
	}))

	// xqd_compute_runtime.go
	_ = linker.FuncWrap("env", "xqd_compute_runtime_get_vcpu_ms", safeWrap1("xqd_compute_runtime_get_vcpu_ms", func(i *Instance, a int32) int32 { return i.xqd_compute_runtime_get_vcpu_ms(a) }))
	_ = linker.FuncWrap("env", "xqd_compute_runtime_get_heap_mib", safeWrap1("xqd_compute_runtime_get_heap_mib", func(i *Instance, a int32) int32 { return i.xqd_compute_runtime_get_heap_mib(a) }))

	// xqd_async_io.go
	_ = linker.FuncWrap("env", "xqd_async_io_select", safeWrap4("xqd_async_io_select", func(i *Instance, a int32, b int32, c int32, d int32) int32 { return i.xqd_async_io_select(a, b, c, d) }))
	_ = linker.FuncWrap("env", "xqd_async_io_is_ready", safeWrap2("xqd_async_io_is_ready", func(i *Instance, a int32, b int32) int32 { return i.xqd_async_io_is_ready(a, b) }))

	// xqd_http_downstream.go
	_ = linker.FuncWrap("env", "xqd_http_downstream_next_request", safeWrap3("xqd_http_downstream_next_request", func(i *Instance, a int32, b int32, c int32) int32 { return i.xqd_http_downstream_next_request(a, b, c) }))
	_ = linker.FuncWrap("env", "xqd_http_downstream_next_request_wait", safeWrap3("xqd_http_downstream_next_request_wait", func(i *Instance, a int32, b int32, c int32) int32 {
		return i.xqd_http_downstream_next_request_wait(a, b, c)
	}))
	_ = linker.FuncWrap("env", "xqd_http_downstream_next_request_abandon", safeWrap1("xqd_http_downstream_next_request_abandon", func(i *Instance, a int32) int32 { return i.xqd_http_downstream_next_request_abandon(a) }))
	_ = linker.FuncWrap("env", "xqd_http_downstream_original_header_names", safeWrap6("xqd_http_downstream_original_header_names", func(i *Instance, a int32, b int32, c int32, d int32, e int32, f int32) int32 {
		return i.xqd_http_downstream_original_header_names(a, b, c, d, e, f)
	}))
	_ = linker.FuncWrap("env", "xqd_http_downstream_original_header_count", safeWrap2("xqd_http_downstream_original_header_count", func(i *Instance, a int32, b int32) int32 { return i.xqd_http_downstream_original_header_count(a, b) }))
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_cipher_openssl_name", safeWrap4("xqd_req_downstream_tls_cipher_openssl_name", func(i *Instance, a int32, b int32, c int32, d int32) int32 {
		return i.xqd_http_downstream_tls_cipher_openssl_name(a, b, c, d)
	}))
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_protocol", safeWrap4("xqd_req_downstream_tls_protocol", func(i *Instance, a int32, b int32, c int32, d int32) int32 {
		return i.xqd_http_downstream_tls_protocol(a, b, c, d)
	}))
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_client_servername", safeWrap4("xqd_req_downstream_tls_client_servername", func(i *Instance, a int32, b int32, c int32, d int32) int32 {
		return i.xqd_http_downstream_tls_client_servername(a, b, c, d)
	}))
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_client_hello", safeWrap4("xqd_req_downstream_tls_client_hello", func(i *Instance, a int32, b int32, c int32, d int32) int32 {
		return i.xqd_http_downstream_tls_client_hello(a, b, c, d)
	}))
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_raw_client_certificate", safeWrap4("xqd_req_downstream_tls_raw_client_certificate", func(i *Instance, a int32, b int32, c int32, d int32) int32 {
		return i.xqd_http_downstream_tls_raw_client_certificate(a, b, c, d)
	}))
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_client_cert_verify_result", safeWrap2("xqd_req_downstream_tls_client_cert_verify_result", func(i *Instance, a int32, b int32) int32 {
		return i.xqd_http_downstream_tls_client_cert_verify_result(a, b)
	}))
	_ = linker.FuncWrap("env", "xqd_req_downstream_client_h2_fingerprint", safeWrap4("xqd_req_downstream_client_h2_fingerprint", func(i *Instance, a int32, b int32, c int32, d int32) int32 {
		return i.xqd_http_downstream_client_h2_fingerprint(a, b, c, d)
	}))
	_ = linker.FuncWrap("env", "xqd_req_downstream_client_oh_fingerprint", safeWrap4("xqd_req_downstream_client_oh_fingerprint", func(i *Instance, a int32, b int32, c int32, d int32) int32 {
		return i.xqd_http_downstream_client_oh_fingerprint(a, b, c, d)
	}))
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_ja3_md5", safeWrap3("xqd_req_downstream_tls_ja3_md5", func(i *Instance, a int32, b int32, c int32) int32 { return i.xqd_http_downstream_tls_ja3_md5(a, b, c) }))
	_ = linker.FuncWrap("env", "xqd_req_downstream_tls_ja4", safeWrap4("xqd_req_downstream_tls_ja4", func(i *Instance, a int32, b int32, c int32, d int32) int32 {
		return i.xqd_http_downstream_tls_ja4(a, b, c, d)
	}))
}

// Package fastlike is a Go implementation of the Fastly Compute XQD ABI.
//
// It implements an http.Handler that executes WebAssembly programs compatible with Fastly's
// Compute platform. Each incoming HTTP request is handled by a fresh wasm instance,
// as the XQD ABI is designed around a single request/response pair per instance.
//
// The public API is intentionally minimal to prevent misuse. The main entry points are:
//   - New(): Creates a Fastlike instance from a wasm file
//   - Fastlike.ServeHTTP(): Handles HTTP requests using the wasm program
//
// Instances are pooled and reused to amortize compilation costs, but each instance
// handles only one request at a time.
//
// XQD ABI
//
// The XQD ABI is the interface between a Compute wasm program and the host runtime.
// In production, the host is Fastly's Compute platform. Fastlike is an alternative
// implementation for local development and testing.
//
// The ABI is not publicly documented. This implementation was reverse-engineered from
// the Fastly Rust crate source code (particularly abi.rs and lib.rs).
//
// Implementation:
//   - ABI functions are implemented in xqd*.go files
//   - Functions are linked to the wasm program via wasmContext.link() and linklegacy()
//   - Each function intentionally follows C-style signatures for easier comparison
//     with the Fastly Rust crate (not idiomatic Go by design)
//
// BACKENDS
//
// Fastly Compute programs must send all subrequests to named backends (origins).
// Requests cannot be sent to arbitrary URLs without a backend configured.
//
// In Fastlike, backends are configured using WithBackend() options:
//   - WithBackend(name, handler): Register a simple backend
//   - WithBackendConfig(config): Register with full configuration (timeouts, SSL, etc.)
//   - WithDefaultBackend(fn): Fallback for undefined backends (default returns 502)
//   - WithUnreliableBackend(name, handler, uptime): Register a backend whose
//     reliability is simulated. Each request has an uptime/100 chance of being
//     forwarded normally; the rest receive a synthetic 502 identical to a real
//     upstream failure. Use this to exercise error paths in guest code without
//     having to actually take a backend down.
//   - WithUnreliableDefaultBackend(fn, uptime): Same simulation applied to the
//     catch-all factory installed via WithDefaultBackend.
//
// On the CLI the same idea is exposed by appending @N to the address, where N
// is a percentage in 0..100. For example, -backend api=localhost:8080@50 makes
// the api backend drop roughly half of its requests with a 502.
//
// When the guest program sends a request to a backend, Fastlike looks up the
// corresponding http.Handler and forwards the request.
//
// [abi.rs]: https://docs.rs/crate/fastly/0.3.2/source/src/abi.rs
// [lib.rs]: https://docs.rs/crate/fastly-shared/0.3.2/source/src/lib.rs

package fastlike

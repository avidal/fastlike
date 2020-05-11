# Fastlike

Fastlike is a Go project that implements the Fastly Compute@Edge ABI using `wasmtime` and exposes
a `http.Handler` for you to use.

See `main.go` for an example. We also have the original Rust source in `src/` and a pre-built wasm
binary in `bin/main.wasm`.

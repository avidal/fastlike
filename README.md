# Fastlike

Fastlike is a Go project that implements the Fastly Compute@Edge ABI using `wasmtime` and exposes
a `http.Handler` for you to use.

See `main.go` for an example. We also have the original Rust source in `src/`.

You can run it with:

```
$ fastly compute build
$ go run main.go
```

It'll start an http server on `localhost:5001`. All subrequests issued by the wasm binary will be
sent to `localhost:8000`.

Try a full example with:

```
# in one terminal:
$ fastly compute build
$ go run main.go

# in another
$ python3 -m http.server

# in a third
$ curl localhost:5001/Cargo.toml
```

Assuming you're using the same rust code, which has a specific path check for `/Cargo.toml`, you
should see the `Cargo.toml` from this repository come back from curl.

Go, running Rust, calling Go, proxying to Python.

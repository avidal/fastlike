# Fastlike

Fastlike is a Go project that implements the Fastly Compute@Edge ABI using `wasmtime` and exposes
a `http.Handler` for you to use.

There's a proxy implementation in `cmd/fastlike` which you can run with:

```
$ go run ./cmd/fastlike -wasm <wasmfile> -proxy-to <proxy address>
```

You can use a provided wasm binary by building the testdata:

```
$ cd testdata; fastly compute build; cd ..
$ go run ./cmd/fastlike -wasm ./testdata/bin/main.wasm -proxy-to <proxy address>
```

For a more full-featured example:

```
# in one terminal:
$ cd testdata; fastly compute build; cd ..
$ go run ./cmd/fastlike -wasm ./testdata/bin/main.wasm -proxy-to localhost:8000 -bind localhost:5000

# in another
$ python3 -m http.server

# in a third
$ curl localhost:5000/testdata/src/main.rs
```

Go, running Rust, calling Go, proxying to Python.

## TODO

- How to handle Go errors? We just panic.
- How to handle errors over the ABI? Just return the proper XQD status?
    - Maybe have `Fastlike` take a writer to send logs to, and abi methods can write
      warnings/errors there
- Implement the rest of the ABI

# fastlike

fastlike is a Go project that implements the Fastly Compute@Edge ABI using `wasmtime` and exposes
a `http.Handler` for you to use.

There's an example proxy implementation in `cmd/fastlike` which you can run with:

```
$ go run ./cmd/fastlike -wasm <wasmfile> -backend <proxy address>
```

You'll need a Fastly Compute@Edge compatible wasm program to run the example proxy. The simplest
way to do that is via the [fastly cli](https://github.com/fastly/cli) and using one of the [starter
kits](https://developer.fastly.com/solutions/starters/).

After scaffolding your wasm program using a starter kit and modifying it to your liking, you'll need
to build the wasm binary:

```
$ fastly compute init my-compute-project
# answer the prompts, creating a rust or assemblyscript project
$ fastly compute build
```

And then use the resulting wasm binary in fastlike:

```
$ go run ./cmd/fastlike -wasm my-compute-project/bin/main.wasm -backend <proxy address>
```

You don't need the fastly CLI to build the test program either, as long as you have rust installed
and the wasm32-wasip1 target available:

```
# This example is using one of the guest implementations of the spec tests
$ cargo target add wasm32-wasip1 # ensure we have the wasm32-wasip1 for the current toolchain
# The wasm32-wasip1 target is configured as the default target via `specs/testdata/rust/.cargo/config`
$ cd specs/testdata/rust; cargo build; cd ../../..
$ go run ./cmd/fastlike -wasm ./specs/testdata/rust/target/wasm32-wasip1/debug/example.wasm -backend <proxy address>
```

However, using the [fastly cli](https://github.com/fastly/cli) will help ensure your toolchains are
properly up to date and your dependencies are in-order.

For a more full-featured example, using the default rust starter kit:

```
# in one terminal:
$ go run ./cmd/fastlike -wasm ./my-compute-project/bin/main.wasm -backend localhost:8000 -bind localhost:5000

# in another
$ python3 -m http.server

# in a third
$ curl localhost:5000/backend
```

Go, running Rust, calling Go, proxying to Python.

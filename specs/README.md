## Fastlike spec tests

This directory implements "spec" tests for fastlike guest programs, useful for verifying guest
implementations. Currently, these spec tests are also used as the primary public testing interface
for fastlike itself.

The `testdata` directory contains data used during tests, as well as a selection of guest
implementations, both official (rust and assemblyscript) and third-party (zig).

The spec test is implemented so that it can target an arbitrary guest program, which allows authors
of guest bindings to verify their implementations using the same test suite used for the official
guest implementations.

### Guest program API contract

Guest programs should set their default response (for unhandled requests) to return a 502 Not
Implemented. This ensures that the spec runner marks those tests as skipped, instead of failing the
test.

TODO: Better document expectations for guest programs (should we list every endpoint and what it's
supposed to do? Or just refer to a particular guest program?)

### Running specs against a guest program

You can run the spec test suite against your guest program via `go test`:

```
# build your guest program through whatever means
$ go test -tags=spec . -wasm app.wasm
```

Where `app.wasm` is the wasm binary you built for your guest program.

As an example, the "official" rust guest is tested via:

```
$ cd testdata/rust
$ cargo build
$ cd ../..
$ go test . -wasm testdata/rust/target/wasm32-wasi/debug/example.wasm
```

You could also build the spec runner once and reuse it:

```
$ go test -c . -o spec-runner
# Run the spec runner in verbose mode (`-test.v`) against `app.wasm`
$ ./spec-runner -test.v -wasm app.wasm
```

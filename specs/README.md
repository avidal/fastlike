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

#### Expected endpoints

The spec test suite expects guest programs to implement the following endpoints:

**Basic functionality:**
- `GET /simple-response` - Return 200 OK with body "Hello, world!"
- `GET /no-body` - Return 204 No Content with empty body
- `GET /append-body` - Return 200 OK with body "original\nappended" (tests body append operations)

**Request proxying:**
- `GET /proxy` - Forward request to a backend and return the backend's response as-is
- `GET /append-header` - Add a header "test-header: test-value" to a backend request

**XQD ABI features:**
- `GET /user-agent` - Parse User-Agent header and return formatted string like "Firefox 76.1.15"
- `GET /geo` - Perform geolocation lookup on client IP and return JSON with geo data (at minimum `as_name` field)
- `GET /log` - Write "Hello from fastlike!" to the "default" log endpoint, return 204
- `GET /dictionary/testdict/testkey` - Look up "testkey" in "testdict" dictionary and return the value

**Error handling:**
- `GET /panic!` - Trigger a wasm panic/trap to test error handling

**Default behavior:**
- All other paths should return 502 Not Implemented

See `testdata/rust/src/main.rs` for a reference implementation that passes all spec tests.

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
$ go test . -wasm testdata/rust/target/wasm32-wasip1/debug/example.wasm
```

You could also build the spec runner once and reuse it:

```
$ go test -c . -o spec-runner
# Run the spec runner in verbose mode (`-test.v`) against `app.wasm`
$ ./spec-runner -test.v -wasm app.wasm
```

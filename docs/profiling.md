# Profiling fastlike guests

fastlike has a built-in profiler that captures hostcall timings, backend
waterfalls, optional native CPU samples, and (in deep mode) body byte
counters, cache hit/miss rates, named-store access counts, request and
response header summaries, and a wasm linear memory size curve. Traces
are kept in a per-`Fastlike` LRU and served through a separate read-only
HTTP listener you opt into with one flag.

This document covers the operator-facing surface: which flags do what,
how the security gates work, what deep mode does and does not capture,
and how the native sampling integration works on Linux and macOS.

## Quick start

```bash
fastlike -wasm program.wasm -backend api=localhost:8080 -profile-ui localhost:6060
```

That gives you the default `trace` mode (hostcall + backend timeline)
plus a viewer at `http://localhost:6060/`. Visit the URL in your
browser, send a request to your guest, and the new trace shows up at
the top of the index.

`-profile-ui` is the only flag you need for the common case. The other
profile flags exist for non-default deployments (deep mode, remote
binds, archival).

## Profile modes

```text
-profile {off|trace|native|combined|deep}   (default: trace)
```

- **off** — no trace collection. The store is allocated if any other
  profile option is set so the surface is consistent for embedders,
  but `RequestTrace` allocation is skipped per request.
- **trace** — the always-on layer: hostcall spans, backend waterfall,
  header-flush and hijack markers, ctx-cancel / panic / trap outcome
  classification. Cheap enough to leave on in development.
- **native** — `trace` plus wasmtime native profiling configured for
  `perf` consumption via jitdump on Linux. On non-Linux a one-line
  startup notice prints and the engine runs without `SetProfiler`.
- **combined** — same as `native` today; reserved for a future split
  if we add additional sampling integrations.
- **deep** — `trace` plus body byte counters, cache outcomes,
  per-named-store access counts, request/response header summaries
  with deny-list redaction, and a wasm linear memory size curve. A
  two-line startup notice prints exactly what deep captures and what
  it never captures so operators can audit from the log alone.

## The viewer

The UI lives on a separate listener from the wasm program. The two
sockets are always distinct — `-profile-ui` cannot collide with `-bind`.

Endpoints:

- `GET /` — index of recent traces (newest first) with method, URL,
  status, outcome, wall time, hostcall time, span / backend counts,
  drop annotations, module id.
- `GET /r/{req_id}` — per-request HTML page with summary, server-side
  CSS waterfall, span table, deep-mode tables (when applicable),
  and an interactive canvas timeline rendered by the embedded
  `/assets/timeline.js`.
- `GET /r/{req_id}.json` — native JSON trace.
- `GET /r/{req_id}.chrome.json` — Chrome Tracing / Perfetto export.
- `GET /r/{req_id}.firefox.json` — Firefox profiler Gecko export.
- `GET /r/{req_id}.pprof` — gzip-compressed `profile.proto`.
- `GET /assets/timeline.js` — vendored canvas viewer (no JS framework).

The server-rendered HTML works without JavaScript; the canvas adds an
interactive overlay above the tables.

## Security: where the UI lives

The profile UI must not be reachable from anywhere the main wasm
listener is not already exposing. The CLI enforces a hard policy at
startup, before either listener binds:

- **No UI listener is started unless `-profile-ui ADDR` is set.** The
  flag is empty by default; fastlike prints no profiler URL until you
  opt in.
- The UI is never auto-mounted on the wasm listener regardless of
  `-profile-ui`. The two listeners are always separate sockets.
- If `ADDR` resolves to a loopback host (`127.0.0.0/8`, `::1`,
  `localhost`, or a unix socket path), the UI starts with no required
  auth.
- If `ADDR` resolves to any other address, fastlike refuses to start
  unless **either** `-profile-auth TOKEN` is set **or** the user has
  explicitly passed `-profile-insecure-ui`. The error message names
  which flag to add. This is a hard gate, not a warning.
- `-profile-auth TOKEN` enforces `Authorization: Bearer TOKEN` on
  every UI request, including the index, JSON endpoints, and the
  static asset.
- `-profile-insecure-ui` is for environments with externalized auth
  (mTLS, authenticating reverse proxy). It logs a prominent startup
  warning so the choice is visible in any log scan.

Examples:

```bash
# loopback bind, no auth needed
fastlike -wasm prog.wasm -backend api=localhost:8080 -profile-ui localhost:6060

# remote dev box over SSH tunnel; bearer auth required
fastlike -wasm prog.wasm -backend api=localhost:8080 \
    -profile-ui 0.0.0.0:6060 -profile-auth $(openssl rand -hex 16)

# behind an authenticating proxy — opt in explicitly
fastlike -wasm prog.wasm -backend api=localhost:8080 \
    -profile-ui 0.0.0.0:6060 -profile-insecure-ui
```

The listener also binds synchronously: a port-in-use error appears at
startup, before any "profiler UI at..." log line is printed, so you
never think the UI is up when it isn't.

## Deep mode

Deep mode is strictly opt-in. The startup notice lists exactly what it
captures and what it never captures, so an operator auditing the log
does not have to read the source:

```text
[fastlike] -profile=deep captures: body read/write byte totals; cache
  lookup/insert/hit/miss/stale counts; per-named-store access counts
  (kv/config/secret/dictionary); request/response header names + sizes
  (deny-listed names like Cookie/Authorization redacted to <redacted>);
  wasm linear memory size curve sampled at request start, finalize, and
  hostcall boundaries.
[fastlike] -profile=deep NEVER captures: header values, body bytes,
  secret values, KV values, cache keys, surrogate keys, URL userinfo,
  URL query strings, Go runtime heap (the memory curve is wasm guest
  memory only).
```

### Header redaction

These header names are case-insensitively collapsed to `<redacted>`
even in deep mode. Their byte sizes still count toward the per-direction
totals so an operator can spot "this request had a huge Cookie header"
without learning the cookie's value:

- `Cookie`
- `Set-Cookie`
- `Authorization`
- `Proxy-Authorization`
- `X-Api-Key`
- `Proxy-Authenticate`
- `WWW-Authenticate`

Two distinct deny-listed headers in the same direction produce two
distinct `<redacted>` rows, so the byte counts don't get merged.

### What deep doesn't capture, ever

The fields below have no place in the in-memory `RequestTrace` or any
encoder. The privacy contract is enforced by the data model — the
encoders physically cannot reach them.

- Header values
- Request or response body bytes
- Secret values, KV values, dictionary values, config values
- Cache keys and surrogate keys
- URL userinfo and query strings (the trace records the redacted URL
  via `redactURL` on every backend call)
- Go runtime heap (the wasm memory curve is wasm guest memory only)

### Wasm linear memory curve

The "heap" curve sampled in deep mode is wasm guest linear memory
size, captured at request start, finalize, and after every hostcall
boundary. Consecutive samples whose memory size matches the previous
one are dropped at recording time (wasm memory only grows
monotonically, so once it stabilises further samples add no
information). The per-request cap is 1024 distinct samples; over-cap
samples increment `heap_samples_dropped` in the native JSON.

The Chrome and Firefox encoder surfaces carry aggregate
min/max/final/count/dropped values only; the full curve stays in the
native JSON to keep the visualization formats readable. The field name
is `wasm_heap_*` in every wire form to head off confusion with the Go
host runtime heap.

## Native sampling integration

When `-profile native` or `-profile combined` is active and the host
is Linux, fastlike configures the wasmtime engine with
`ProfilingStrategyJitdump` and `perf record` can attribute samples
directly to wasm functions. On macOS, route the same jitdump through
[`samply`](https://github.com/mstange/samply).

`wasmtime-go` v38 only wraps the jitdump strategy from the upstream
wasmtime C API; perfmap and vtune are deferred until the bindings
expose them.

When native sampling is configured, fastlike writes
`wasm-symbols-{pid}.json` to `-profile-dir` (or the working directory)
at startup. The file maps wasm module exports to their names so an
external sampler's output can be joined back to the trace recorder's
hostcall timeline. The format:

```json
{
  "pid": 12345,
  "module_id": "abc123def456",
  "mode": "combined",
  "exports": [
    {"name": "memory", "kind": "memory"},
    {"name": "_start", "kind": "func"}
  ]
}
```

### Joining external samples back

The library exposes `NativeSampleImporter` and `MergeNativeSamples`
for embedders who want to fold `perf script` output into the in-process
trace store. The pattern:

```go
importer := fastlike.NewPerfScriptImporter()
events, err := importer.Import(perfScriptOutput)
if errors.Is(err, fastlike.ErrNoNativeSamples) {
    // input was structurally valid but had no samples
}
attached := fastlike.MergeNativeSamples(fl.ProfileStore(), events, os.Getpid(), fl.ModuleID())
```

The merge filters by PID + time window + module ID. Samples that
don't match any trace are dropped silently; the function returns the
count of samples actually attached so the caller can summarize.

A sample's timestamp comes from `perf script` output in
seconds.fraction form. For the join to work the perf timestamps must be
comparable with `time.Now()`; record with
`perf record -k CLOCK_REALTIME` and the joins happen automatically.
Without `-k`, perf uses CLOCK_MONOTONIC which has a different epoch
and every sample drops at the time-window gate.

## CLI reference

```text
-profile MODE
    profiling mode: off, trace, native, combined, deep. Defaults to trace.
    Collection runs even without a UI.

-profile-ui ADDR
    bind the profile UI on ADDR. Empty (default) disables the UI listener.
    Loopback binds skip auth; non-loopback binds require -profile-auth or
    -profile-insecure-ui.

-profile-auth TOKEN
    bearer token required on every UI request when set. Required for
    non-loopback -profile-ui unless -profile-insecure-ui is set.

-profile-insecure-ui
    permit a non-loopback -profile-ui without -profile-auth. Intended for
    environments with externalized auth (mTLS, authenticating reverse
    proxy). Logs a prominent startup warning.

-profile-dir PATH
    directory for per-process profile artifacts (wasm-symbols-{pid}.json).
    Empty (default) writes to the working directory.

-profile-retain N
    per-Fastlike LRU size for completed traces. Defaults to 256.

-profile-backend-cap N
    per-request cap on recorded backend calls. Defaults to 512. Calls
    past the cap still execute; their phase / outcome data is dropped
    and counted in dropped_backend_calls.

-profile-async-grace DUR
    max time finalizeTrace waits for in-flight async backend
    goroutines before snapshotting. Defaults to 100ms. Pass 0 to
    disable the grace period entirely.
```

## Embedder API

The CLI surfaces above are functional options on the `*Fastlike` value:

```go
fl := fastlike.New("program.wasm",
    fastlike.WithProfileMode(fastlike.ProfileModeDeep),
    fastlike.WithProfileUI("localhost:6060"),
    fastlike.WithProfileRetain(512),
    fastlike.WithInstanceOptions(
        fastlike.WithBackendTraced("api", apiProxy, sharedTransport),
    ),
)
```

`WithBackendTraced(name, handler, transport)` is the way to opt a
backend into per-phase timing capture (DNS, connect, TLS, TTFB). The
supplied `*http.Transport` is embedder-owned: fastlike never clones,
mutates, or closes it. Plain `WithBackend(name, handler)` keeps its
existing meaning — opaque handler, total span timing only, phase
fields stay nil.

`fl.ProfileStore()` exposes the per-Fastlike trace store. Use it for
programmatic inspection, custom archival, or to merge native samples in:

```go
store := fl.ProfileStore()
recent := store.Recent(10)
for _, tr := range recent {
    fmt.Printf("req=%d outcome=%s wall=%dns\n", tr.ReqID, tr.Outcome, tr.WallNanos)
}
```

## Output formats

The native JSON at `/r/{id}.json` is canonical. The third-party
exports are pure derived views — each encoder is a stateless
`*RequestTrace → []byte` function with no store or UI dependency. You
can call them directly:

```go
chromeBytes, _ := fastlike.EncodeChromeTrace(trace)
firefoxBytes, _ := fastlike.EncodeFirefoxGecko(trace)
pprofBytes, _ := fastlike.EncodePprof(trace)
```

Each encoder respects the privacy contract: the in-memory trace has
no field for header values, secrets, or body bytes, so the encoders
physically cannot include them. The privacy fixture grep tests pin
this contract against any future schema widening.

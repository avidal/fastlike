package fastlike

import (
	"runtime"

	"github.com/bytecodealliance/wasmtime-go/v45"
)

// nativeProfilerStrategy maps a ProfileMode to the wasmtime profiling
// strategy fastlike should configure on the engine. Returns
// ProfilingStrategyNone unless mode is native or combined, and unless the
// host platform actually supports jitdump (today: Linux).
//
// This helper is the single source of truth for the platform gate so
// tests can pin the mapping without standing up a real wasmtime engine
// or requiring perf/samply to be installed. Callers separately decide
// whether to log a notice when the configured mode would have enabled
// sampling but the platform does not support it.
func nativeProfilerStrategy(mode ProfileMode) (wasmtime.ProfilingStrategy, bool) {
	switch mode {
	case ProfileModeNative, ProfileModeCombined:
		// wasmtime-go v38 only wraps ProfilingStrategyJitdump from the
		// upstream wasmtime API; perfmap and vtune exist in the C API
		// but are not exposed in the Go bindings. The plan section on
		// "Constraints from the runtime" tracks this; if we want
		// perfmap we contribute upstream rather than working around it
		// here.
		if runtime.GOOS != "linux" {
			return wasmtime.ProfilingStrategyNone, false
		}
		return wasmtime.ProfilingStrategyJitdump, true
	}
	return wasmtime.ProfilingStrategyNone, false
}

// nativeSamplingRequested reports whether the configured mode asked for
// native sampling, regardless of whether the platform supports it. The
// CLI uses this to print a one-line notice when a native/combined mode
// is configured on a host with no supported strategy, so the operator
// understands why their sampler will see no jitdump output.
func nativeSamplingRequested(mode ProfileMode) bool {
	return mode == ProfileModeNative || mode == ProfileModeCombined
}

// wasmSymbolEntry is one row in the sidecar manifest. The schema is
// intentionally narrow for v1: external samplers need a stable name to
// hang on a sampled wasm function index, and the export list is what
// we have access to via the wasmtime-go bindings. Internal (non-exported)
// function names live in the wasm name section which the current bindings
// do not surface; expanding the schema later is additive.
type wasmSymbolEntry struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// wasmSymbolManifest is the JSON document written next to the running
// fastlike process as wasm-symbols-{pid}.json when native sampling is
// active. Tools join their samples back to wasm-level names by reading
// the manifest indexed by the same pid that wasmtime's jitdump records.
type wasmSymbolManifest struct {
	PID      int               `json:"pid"`
	ModuleID string            `json:"module_id"`
	Mode     string            `json:"mode"`
	Exports  []wasmSymbolEntry `json:"exports"`
}

package fastlike

import (
	"testing"

	"github.com/bytecodealliance/wasmtime-go/v46"
)

const wideArithmeticWat = `(module
  (func (export "add128") (param i64 i64 i64 i64) (result i64 i64)
    local.get 0
    local.get 1
    local.get 2
    local.get 3
    i64.add128))`

// Relaxed SIMD lowerings differ per host architecture unless pinned. These are the
// deterministic results production computes; on x86 the unpinned lowerings differ.
const relaxedSIMDWat = `(module
  (func (export "swizzle") (result i32)
    (i8x16.extract_lane_u 0
      (i8x16.relaxed_swizzle
        (v128.const i8x16 0 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15)
        (v128.const i8x16 17 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0))))

  (func (export "q15mulr") (result i32)
    (i16x8.extract_lane_s 0
      (i16x8.relaxed_q15mulr_s
        (v128.const i16x8 0x8000 0 0 0 0 0 0 0)
        (v128.const i16x8 0x8000 0 0 0 0 0 0 0))))

  (func (export "laneselect") (result i32)
    (i8x16.extract_lane_u 0
      (i8x16.relaxed_laneselect
        (v128.const i8x16 0xAA 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0)
        (v128.const i8x16 0x55 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0)
        (v128.const i8x16 0x01 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0)))))`

func wat2wasm(t *testing.T, wat string) []byte {
	t.Helper()
	wasmbytes, err := wasmtime.Wat2Wasm(wat)
	if err != nil {
		t.Fatalf("wat2wasm: %v", err)
	}
	return wasmbytes
}

// instantiateWat returns a callable instance of wat, compiled with config.
func instantiateWat(t *testing.T, config *wasmtime.Config, wat string) (*wasmtime.Store, *wasmtime.Instance) {
	t.Helper()
	engine := wasmtime.NewEngineWithConfig(config)
	module, err := wasmtime.NewModule(engine, wat2wasm(t, wat))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	store := wasmtime.NewStore(engine)
	// The default deadline would interrupt the first call.
	store.SetEpochDeadline(1)
	instance, err := wasmtime.NewInstance(store, module, nil)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	return store, instance
}

func TestEngineConfigPinsRelaxedSIMD(t *testing.T) {
	store, instance := instantiateWat(t, newEngineConfig(nil), relaxedSIMDWat)

	for _, tt := range []struct {
		name string
		want int32
	}{
		{"swizzle", 0},     // out of range index, not index modulo 16
		{"q15mulr", 32767}, // saturates, does not wrap to -32768
		{"laneselect", 84}, // bitwise select, not high bit of the mask lane
	} {
		got, err := instance.GetFunc(store, tt.name).Call(store)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestEngineConfigMatchesProductionFeatures(t *testing.T) {
	engine := wasmtime.NewEngineWithConfig(newEngineConfig(nil))
	for _, tt := range []struct {
		name     string
		wat      string
		accepted bool
	}{
		{"wide arithmetic", wideArithmeticWat, true},
		{"funcref table", `(module (table 1 funcref) (func (result funcref) (table.get 0 (i32.const 0))))`, true},
		{"multi-memory", `(module (memory 1) (memory 1))`, true},
		{"memory64", `(module (memory i64 1))`, true},
		{"tail calls", `(module (func $f (return_call $f)))`, true},
		{"extended const", `(module (global i32 (i32.add (i32.const 1) (i32.const 2))))`, true},
		{"shared memory", `(module (memory 1 1 shared))`, false},
		{"atomics", `(module (memory 1) (func (result i32) (i32.atomic.load (i32.const 0))))`, false},
		{"atomic fence", `(module (func atomic.fence))`, false},
		{"externref", `(module (func (param externref)))`, false},
		{"exceptions", `(module (tag $e) (func (throw $e)))`, false},
		{"function references", `(module (type $t (func)) (func (param (ref null $t))))`, false},
		{"gc", `(module (type (struct (field i32))))`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := wasmtime.NewModule(engine, wat2wasm(t, tt.wat))
			if tt.accepted && err != nil {
				t.Errorf("rejected: %v", err)
			}
			if !tt.accepted && err == nil {
				t.Error("accepted, but production rejects it")
			}
		})
	}
}

const recursionWat = `(module
  (func $depth (export "depth") (param i32) (result i32)
    (if (result i32) (local.get 0)
      (then (i32.add (call $depth (i32.sub (local.get 0) (i32.const 1))) (i32.const 1)))
      (else (i32.const 0)))))`

// maxRecursion returns how deep recursionWat gets before exhausting the stack.
func maxRecursion(t *testing.T, config *wasmtime.Config) int32 {
	t.Helper()
	store, instance := instantiateWat(t, config, recursionWat)
	depth := instance.GetFunc(store, "depth")

	lo, hi := int32(0), int32(1<<24)
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		if _, err := depth.Call(store, mid); err != nil {
			hi = mid - 1
		} else {
			lo = mid
		}
	}
	return lo
}

func TestEngineConfigWasmStackMatchesProduction(t *testing.T) {
	const referenceStack = 512 << 10
	production := maxRecursion(t, newEngineConfig(nil))

	config := newEngineConfig(nil)
	config.SetMaxWasmStack(referenceStack)
	reference := maxRecursion(t, config)

	// Frames have the same size in both engines, so the depth scales with the stack.
	ratio := float64(production) / float64(reference)
	want := float64(maxWasmStack) / referenceStack
	if ratio < want*0.95 || ratio > want*1.05 {
		t.Errorf("recursion depth %d with the production stack, %d with 512 KiB: ratio %.3f, want %.3f",
			production, reference, ratio, want)
	}
}

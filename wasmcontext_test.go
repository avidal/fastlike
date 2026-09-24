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

func TestEngineConfigPinsRelaxedSIMD(t *testing.T) {
	wasmbytes, err := wasmtime.Wat2Wasm(relaxedSIMDWat)
	if err != nil {
		t.Fatalf("wat2wasm: %v", err)
	}

	engine := wasmtime.NewEngineWithConfig(newEngineConfig(nil))
	module, err := wasmtime.NewModule(engine, wasmbytes)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	store := wasmtime.NewStore(engine)
	instance, err := wasmtime.NewInstance(store, module, nil)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}

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

func TestEngineConfigAcceptsWideArithmetic(t *testing.T) {
	wasmbytes, err := wasmtime.Wat2Wasm(wideArithmeticWat)
	if err != nil {
		t.Fatalf("wat2wasm: %v", err)
	}

	engine := wasmtime.NewEngineWithConfig(newEngineConfig(nil))
	if _, err := wasmtime.NewModule(engine, wasmbytes); err != nil {
		t.Fatalf("wide arithmetic module rejected: %v", err)
	}
}

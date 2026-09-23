package profile

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

func TestExtractWasmExportsAcceptsWideArithmetic(t *testing.T) {
	wasmbytes, err := wasmtime.Wat2Wasm(wideArithmeticWat)
	if err != nil {
		t.Fatalf("wat2wasm: %v", err)
	}

	exports, err := extractWasmExports(wasmbytes)
	if err != nil {
		t.Fatalf("extractWasmExports rejected a wide arithmetic module: %v", err)
	}
	if len(exports) != 1 || exports[0].Name != "add128" {
		t.Fatalf("unexpected exports: %+v", exports)
	}
}

package fastlike

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bytecodealliance/wasmtime-go/v45"
)

func TestNativeProfilerStrategyMapping(t *testing.T) {
	// trace/off must never enable a native strategy.
	for _, mode := range []ProfileMode{ProfileModeOff, ProfileModeTrace, ProfileModeDeep} {
		strat, supported := nativeProfilerStrategy(mode)
		if supported {
			t.Errorf("mode %q: supported=true but should be false", mode)
		}
		if strat != wasmtime.ProfilingStrategyNone {
			t.Errorf("mode %q: strategy %v, want None", mode, strat)
		}
	}

	// native/combined map to jitdump on Linux, none elsewhere.
	for _, mode := range []ProfileMode{ProfileModeNative, ProfileModeCombined} {
		strat, supported := nativeProfilerStrategy(mode)
		if runtime.GOOS == "linux" {
			if !supported {
				t.Errorf("linux mode %q: supported=false", mode)
			}
			if strat != wasmtime.ProfilingStrategyJitdump {
				t.Errorf("linux mode %q: strategy %v, want Jitdump", mode, strat)
			}
		} else {
			if supported {
				t.Errorf("non-linux mode %q: supported=true", mode)
			}
			if strat != wasmtime.ProfilingStrategyNone {
				t.Errorf("non-linux mode %q: strategy %v, want None", mode, strat)
			}
		}
	}
}

func TestNativeSamplingRequested(t *testing.T) {
	cases := map[ProfileMode]bool{
		ProfileModeOff:      false,
		ProfileModeTrace:    false,
		ProfileModeNative:   true,
		ProfileModeCombined: true,
		ProfileModeDeep:     false,
	}
	for mode, want := range cases {
		if got := nativeSamplingRequested(mode); got != want {
			t.Errorf("mode %q: got %v, want %v", mode, got, want)
		}
	}
}

func TestWriteWasmSymbolSidecar(t *testing.T) {
	// A minimal valid wasm module: magic + version + an empty export
	// section is enough for wasmtime to compile and report zero exports.
	// We use a small hand-rolled module that has one exported function.
	wasmbytes := minimalWasmWithExport(t, "_start")

	dir := t.TempDir()
	path, err := writeWasmSymbolSidecar(wasmbytes, dir, "modAbc", ProfileModeNative)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(filepath.Base(path), "wasm-symbols-") {
		t.Errorf("filename: %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	var manifest wasmSymbolManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if manifest.ModuleID != "modAbc" {
		t.Errorf("module id: %q", manifest.ModuleID)
	}
	if manifest.Mode != string(ProfileModeNative) {
		t.Errorf("mode: %q", manifest.Mode)
	}
	if manifest.PID != os.Getpid() {
		t.Errorf("pid: %d, want %d", manifest.PID, os.Getpid())
	}
	if len(manifest.Exports) == 0 {
		t.Fatal("expected at least one export, got 0")
	}
	found := false
	for _, e := range manifest.Exports {
		if e.Name == "_start" {
			found = true
			if e.Kind != "func" {
				t.Errorf("_start export kind: %q, want func", e.Kind)
			}
		}
	}
	if !found {
		t.Errorf("expected _start export in manifest, got %+v", manifest.Exports)
	}
}

func TestWriteWasmSymbolSidecarCreatesDir(t *testing.T) {
	wasmbytes := minimalWasmWithExport(t, "_start")
	root := t.TempDir()
	nested := filepath.Join(root, "subdir", "deeper")
	if _, err := writeWasmSymbolSidecar(wasmbytes, nested, "x", ProfileModeNative); err != nil {
		t.Fatalf("expected nested dir to be created: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("nested dir not created: %v", err)
	}
}

func TestMaybeEmitWasmSymbolSidecarSkipsTrace(t *testing.T) {
	// trace and off must not produce a sidecar.
	for _, mode := range []ProfileMode{ProfileModeOff, ProfileModeTrace, ProfileModeDeep} {
		dir := t.TempDir()
		f := &Fastlike{
			moduleID:       "test",
			profileCompile: &profileCompileConfig{mode: mode},
			profileStore:   NewProfileStore(),
		}
		f.profileStore.dir = dir
		f.maybeEmitWasmSymbolSidecar(minimalWasmWithExport(t, "_start"))
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("readdir: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("mode %q produced sidecar files: %v", mode, entries)
		}
	}
}

func TestMaybeEmitWasmSymbolSidecarWritesForNative(t *testing.T) {
	dir := t.TempDir()
	f := &Fastlike{
		moduleID:       "test",
		profileCompile: &profileCompileConfig{mode: ProfileModeCombined},
		profileStore:   NewProfileStore(),
	}
	f.profileStore.dir = dir
	f.maybeEmitWasmSymbolSidecar(minimalWasmWithExport(t, "_start"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one sidecar file, got %d: %v", len(entries), entries)
	}
}

// minimalWasmWithExport returns a tiny but valid wasm module that
// exports the named function. Hand-assembled to avoid bringing in a
// wasm builder dependency for one test fixture. The function is a
// no-op (empty body) so wasmtime can compile and instantiate it.
func minimalWasmWithExport(t *testing.T, exportName string) []byte {
	t.Helper()

	// We assemble the smallest possible module that wasmtime will accept:
	// magic + version + type section ((func)) + function section + export
	// + code section ((end)). Hand-encoded so we don't need a builder.
	// Adapted from the WebAssembly spec binary encoding.
	header := []byte{
		0x00, 0x61, 0x73, 0x6d, // magic
		0x01, 0x00, 0x00, 0x00, // version
	}

	// type section: one type, () -> ()
	typeSec := []byte{
		0x01, // section id: Type
		0x04, // section length
		0x01, // num types
		0x60, // func
		0x00, // num params
		0x00, // num results
	}

	// function section: one function with type index 0
	funcSec := []byte{
		0x03, // section id: Function
		0x02, // section length
		0x01, // num functions
		0x00, // type index 0
	}

	// export section: export function 0 as exportName
	nameBytes := []byte(exportName)
	exportEntry := []byte{}
	exportEntry = append(exportEntry, byte(len(nameBytes))) // name length
	exportEntry = append(exportEntry, nameBytes...)
	exportEntry = append(exportEntry, 0x00)                 // export kind: func
	exportEntry = append(exportEntry, 0x00)                 // export index
	exportSec := []byte{0x07}                               // section id: Export
	exportSec = append(exportSec, byte(len(exportEntry)+1)) // section length (entry + count byte)
	exportSec = append(exportSec, 0x01)                     // num exports
	exportSec = append(exportSec, exportEntry...)

	// code section: one function body, empty (just `end`)
	codeSec := []byte{
		0x0a, // section id: Code
		0x04, // section length
		0x01, // num bodies
		0x02, // body size
		0x00, // local count
		0x0b, // end opcode
	}

	out := append([]byte{}, header...)
	out = append(out, typeSec...)
	out = append(out, funcSec...)
	out = append(out, exportSec...)
	out = append(out, codeSec...)
	return out
}

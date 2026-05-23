package fastlike

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bytecodealliance/wasmtime-go/v38"
)

// writeWasmSymbolSidecar writes wasm-symbols-{pid}.json into dir (or the
// current working directory when dir is empty) containing the export
// list of the module compiled from wasmbytes. Returns the file path on
// success.
//
// The emission is best-effort: a failure to extract exports or write
// the file logs and returns the error, but does not abort startup. The
// sidecar is a debugging aid for external samplers, not load-bearing
// for the in-process trace, so a missing sidecar must never break the
// rest of the profiler.
//
// Callers (New, Reload) gate emission on nativeSamplingRequested(mode)
// so trace-only / off configurations skip this entirely.
func writeWasmSymbolSidecar(wasmbytes []byte, dir, moduleID string, mode ProfileMode) (string, error) {
	exports, err := extractWasmExports(wasmbytes)
	if err != nil {
		return "", fmt.Errorf("extract exports: %w", err)
	}

	manifest := wasmSymbolManifest{
		PID:      os.Getpid(),
		ModuleID: moduleID,
		Mode:     string(mode),
		Exports:  exports,
	}

	target := dir
	if target == "" {
		target = "."
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("create dir %q: %w", target, err)
	}

	name := fmt.Sprintf("wasm-symbols-%d.json", os.Getpid())
	path := filepath.Join(target, name)

	// Write through a tempfile + atomic rename so a pre-existing symlink
	// at path (planted by another user when target is world-writable
	// like /tmp) is replaced rather than followed. os.CreateTemp uses
	// O_CREATE|O_EXCL so the temp itself can never collide with a
	// symlink, and rename(2) replaces the destination path entry
	// atomically without following a destination symlink.
	tmp, err := os.CreateTemp(target, "wasm-symbols-*.json.tmp")
	if err != nil {
		return "", fmt.Errorf("create tempfile in %q: %w", target, err)
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		if _, err := os.Stat(tmpPath); err == nil {
			_ = os.Remove(tmpPath)
		}
	}()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		return "", fmt.Errorf("encode manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close tempfile: %w", err)
	}
	closed = true
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("rename %q -> %q: %w", tmpPath, path, err)
	}
	return path, nil
}

// extractWasmExports builds a throwaway wasmtime.Module just to walk
// its export list. The cost is one module construction at startup; it
// is not on any request path. We do not retain the module — the actual
// per-request engine/module is built by Instance.compile.
//
// Internal (non-exported) function names live in the wasm name section,
// which wasmtime-go v38 does not surface through a Go API. If that
// changes, this function grows; the manifest schema is additive.
func extractWasmExports(wasmbytes []byte) ([]wasmSymbolEntry, error) {
	engine := wasmtime.NewEngine()
	module, err := wasmtime.NewModule(engine, wasmbytes)
	if err != nil {
		return nil, err
	}
	exports := module.Exports()
	out := make([]wasmSymbolEntry, 0, len(exports))
	for _, e := range exports {
		out = append(out, wasmSymbolEntry{
			Name: e.Name(),
			Kind: exportKind(e),
		})
	}
	return out, nil
}

// exportKind reduces wasmtime's ExternType into a short kind tag. The
// strings are stable wire values so external tools can match on them.
func exportKind(e *wasmtime.ExportType) string {
	if e == nil {
		return "unknown"
	}
	ty := e.Type()
	if ty == nil {
		return "unknown"
	}
	switch {
	case ty.FuncType() != nil:
		return "func"
	case ty.GlobalType() != nil:
		return "global"
	case ty.TableType() != nil:
		return "table"
	case ty.MemoryType() != nil:
		return "memory"
	}
	return "unknown"
}

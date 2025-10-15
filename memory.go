package fastlike

import (
	"bytes"
	"encoding/binary"

	"github.com/bytecodealliance/wasmtime-go/v37"
)

// MemorySlice represents the linear memory of a wasm program.
// This interface abstracts the underlying memory representation, allowing for both
// real wasm memory (wasmMemory) and test memory (ByteMemory).
//
// The Memory type wraps MemorySlice to provide typed read/write operations.
type MemorySlice interface {
	Data() []byte // Returns the underlying byte slice
	Len() int     // Current length of the memory
	Cap() int     // Total capacity of the memory
}

// ByteMemory is a MemorySlice mostly used for tests, where you want to be able to write directly into
// the memory slice and read it out
type ByteMemory []byte

// Data returns the underlying byte slice
func (m ByteMemory) Data() []byte {
	return m
}

// Len is the current length of the memory slice
func (m ByteMemory) Len() int {
	return len(m)
}

// Cap is the total capacity of the memory slice
func (m ByteMemory) Cap() int {
	return cap(m)
}

// wasmMemory is a MemorySlice implementation that wraps a wasmtime.Memory.
// It provides access to the wasm linear memory via a byte slice interface.
// The slice is cached and rebuilt only when the memory grows.
type wasmMemory struct {
	store *wasmtime.Store   // The store that owns the memory
	mem   *wasmtime.Memory  // The actual wasm memory object
	slice []byte            // Cached slice (rebuilt when memory grows)
}

// Len returns the current length of the wasm memory.
func (m *wasmMemory) Len() int {
	return len(m.Data())
}

// Cap returns the capacity of the wasm memory.
func (m *wasmMemory) Cap() int {
	return cap(m.Data())
}

// Data returns the underlying byte slice for the wasm memory.
// It rebuilds the slice if the memory has grown since the last call.
//
// Note: The returned slice is unsafe and shares memory with the wasm instance.
// It must not be retained beyond the lifetime of the wasm instance.
func (m *wasmMemory) Data() []byte {
	// Check if cached slice is still valid
	// If memory has grown, the slice needs to be rebuilt
	if m.slice != nil && cap(m.slice) == int(m.mem.Size(m.store)) {
		return m.slice
	}

	// Rebuild the slice from the wasm memory
	m.slice = m.mem.UnsafeData(m.store)
	return m.slice
}

// Memory is a wrapper around a MemorySlice that provides typed read/write operations.
// It handles the conversion between wasm pointers (int32) and Go types, using little-endian
// byte order as required by the WebAssembly spec.
type Memory struct {
	MemorySlice
}

// ReadUint8 reads a uint8 (single byte) from the given offset in memory.
func (m *Memory) ReadUint8(offset int64) uint8 {
	return m.Data()[offset]
}

// Uint16 reads a uint16 from memory at the given offset using little-endian byte order.
func (m *Memory) Uint16(offset int64) uint16 {
	return binary.LittleEndian.Uint16(m.Data()[offset:])
}

// Uint32 reads a uint32 from memory at the given offset using little-endian byte order.
func (m *Memory) Uint32(offset int64) uint32 {
	return binary.LittleEndian.Uint32(m.Data()[offset:])
}

// Uint64 reads a uint64 from memory at the given offset using little-endian byte order.
func (m *Memory) Uint64(offset int64) uint64 {
	return binary.LittleEndian.Uint64(m.Data()[offset:])
}

// PutUint8 writes a uint8 (single byte) to the given offset in memory.
func (m *Memory) PutUint8(v uint8, offset int64) {
	m.Data()[offset] = v
}

// PutUint16 writes a uint16 to memory at the given offset using little-endian byte order.
func (m *Memory) PutUint16(v uint16, offset int64) {
	binary.LittleEndian.PutUint16(m.Data()[offset:], v)
}

// PutUint32 writes a uint32 to memory at the given offset using little-endian byte order.
func (m *Memory) PutUint32(v uint32, offset int64) {
	binary.LittleEndian.PutUint32(m.Data()[offset:], v)
}

// PutInt32 writes an int32 in little-endian byte order to the given offset in memory.
func (m *Memory) PutInt32(v int32, offset int64) {
	b := new(bytes.Buffer)
	_ = binary.Write(b, binary.LittleEndian, v)
	_, _ = m.WriteAt(b.Bytes(), offset)
}

// PutInt64 writes an int64 in little-endian byte order to the given offset in memory.
func (m *Memory) PutInt64(v int64, offset int64) {
	b := new(bytes.Buffer)
	_ = binary.Write(b, binary.LittleEndian, v)
	_, _ = m.WriteAt(b.Bytes(), offset)
}

// PutUint64 writes a uint64 in little-endian byte order to the given offset in memory.
func (m *Memory) PutUint64(v uint64, offset int64) {
	binary.LittleEndian.PutUint64(m.Data()[offset:], v)
}

// ReadAt reads len(p) bytes from memory starting at the given offset.
// It implements io.ReaderAt.
func (m *Memory) ReadAt(p []byte, offset int64) (int, error) {
	n := copy(p, m.Data()[offset:])
	return n, nil
}

// WriteAt writes len(p) bytes to memory starting at the given offset.
// It implements io.WriterAt.
func (m *Memory) WriteAt(p []byte, offset int64) (int, error) {
	n := copy(m.Data()[offset:], p)
	return n, nil
}

// ReadUint32 reads a uint32 from the given offset (convenience wrapper for int32 offsets).
func (m *Memory) ReadUint32(offset int32) uint32 {
	return m.Uint32(int64(offset))
}

// ReadUint64 reads a uint64 from the given offset (convenience wrapper for int32 offsets).
func (m *Memory) ReadUint64(offset int32) uint64 {
	return m.Uint64(int64(offset))
}

// WriteUint32 writes a uint32 to the given offset (convenience wrapper for int32 offsets).
func (m *Memory) WriteUint32(offset int32, value uint32) {
	m.PutUint32(value, int64(offset))
}

// WriteUint64 writes a uint64 to the given offset (convenience wrapper for int32 offsets).
func (m *Memory) WriteUint64(offset int32, value uint64) {
	m.PutUint64(value, int64(offset))
}

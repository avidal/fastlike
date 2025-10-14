package fastlike

import (
	"bytes"
	"encoding/binary"

	"github.com/bytecodealliance/wasmtime-go"
)

// MemorySlice represents an underlying slice of memory from a wasm program.
// An implementation of MemorySlice is most often wrapped with a Memory, which provides convenience
// functions to read and write different values.
type MemorySlice interface {
	Data() []byte
	Len() int
	Cap() int
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

// wasmMemory is a MemorySlice implementation that wraps a wasmtime.Memory
type wasmMemory struct {
	store *wasmtime.Store
	mem   *wasmtime.Memory
	slice []byte
}

func (m *wasmMemory) Len() int {
	return len(m.Data())
}

func (m *wasmMemory) Cap() int {
	return cap(m.Data())
}

func (m *wasmMemory) Data() []byte {
	// If we have a pre-built slice and that slice capacity is the same as the current data size,
	// return it. Otherwise, rebuild the slice.
	if m.slice != nil && cap(m.slice) == int(m.mem.Size(m.store)) {
		return m.slice
	}

	m.slice = m.mem.UnsafeData(m.store)
	return m.slice
}

// Memory is a wrapper around a MemorySlice that adds convenience functions for reading and writing
type Memory struct {
	MemorySlice
}

func (m *Memory) ReadUint8(offset int64) uint8 {
	return m.Data()[offset]
}

func (m *Memory) Uint16(offset int64) uint16 {
	return binary.LittleEndian.Uint16(m.Data()[offset:])
}

func (m *Memory) Uint32(offset int64) uint32 {
	return binary.LittleEndian.Uint32(m.Data()[offset:])
}

func (m *Memory) Uint64(offset int64) uint64 {
	return binary.LittleEndian.Uint64(m.Data()[offset:])
}

func (m *Memory) PutUint8(v uint8, offset int64) {
	m.Data()[offset] = v
}

func (m *Memory) PutUint16(v uint16, offset int64) {
	binary.LittleEndian.PutUint16(m.Data()[offset:], v)
}

func (m *Memory) PutUint32(v uint32, offset int64) {
	binary.LittleEndian.PutUint32(m.Data()[offset:], v)
}

func (m *Memory) PutInt32(v int32, offset int64) {
	b := new(bytes.Buffer)
	_ = binary.Write(b, binary.LittleEndian, v)
	_, _ = m.WriteAt(b.Bytes(), offset)
}

func (m *Memory) PutInt64(v int64, offset int64) {
	b := new(bytes.Buffer)
	_ = binary.Write(b, binary.LittleEndian, v)
	_, _ = m.WriteAt(b.Bytes(), offset)
}

func (m *Memory) PutUint64(v uint64, offset int64) {
	binary.LittleEndian.PutUint64(m.Data()[offset:], v)
}

func (m *Memory) ReadAt(p []byte, offset int64) (int, error) {
	n := copy(p, m.Data()[offset:])
	return n, nil
}

func (m *Memory) WriteAt(p []byte, offset int64) (int, error) {
	n := copy(m.Data()[offset:], p)
	return n, nil
}

// ReadUint32 reads a uint32 from the given offset
func (m *Memory) ReadUint32(offset int32) uint32 {
	return m.Uint32(int64(offset))
}

// ReadUint64 reads a uint64 from the given offset
func (m *Memory) ReadUint64(offset int32) uint64 {
	return m.Uint64(int64(offset))
}

// WriteUint32 writes a uint32 to the given offset
func (m *Memory) WriteUint32(offset int32, value uint32) {
	m.PutUint32(value, int64(offset))
}

// WriteUint64 writes a uint64 to the given offset
func (m *Memory) WriteUint64(offset int32, value uint64) {
	m.PutUint64(value, int64(offset))
}

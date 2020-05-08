package fastlike

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"unsafe"

	"github.com/bytecodealliance/wasmtime-go"
)

type Memory struct{ *wasmtime.Memory }

func (m Memory) Size() int64 {
	return int64(m.DataSize())
}

func (m *Memory) Bytes() []byte {
	var length = m.Size()
	var data = (*uint8)(m.Data())

	var header reflect.SliceHeader
	header = *(*reflect.SliceHeader)(unsafe.Pointer(&header))

	header.Data = uintptr(unsafe.Pointer(data))
	header.Len = int(length)
	header.Cap = int(length)

	var buf = *(*[]byte)(unsafe.Pointer(&header))
	return buf
}

func (m *Memory) Uint8(offset int64) uint8 {
	return m.Bytes()[offset]
}

func (m *Memory) Uint16(offset int64) uint16 {
	return binary.LittleEndian.Uint16(m.Bytes()[offset:])
}

func (m *Memory) Uint32(offset int64) uint32 {
	return binary.LittleEndian.Uint32(m.Bytes()[offset:])
}

func (m *Memory) Uint64(offset int64) uint64 {
	return binary.LittleEndian.Uint64(m.Bytes()[offset:])
}

func (m *Memory) PutUint8(v uint8, offset int64) {
	m.Bytes()[offset] = v
}

func (m *Memory) PutUint16(v uint16, offset int64) {
	binary.LittleEndian.PutUint16(m.Bytes()[offset:], v)
}

func (m *Memory) PutUint32(v uint32, offset int64) {
	binary.LittleEndian.PutUint32(m.Bytes()[offset:], v)
}

func (m *Memory) PutInt32(v int32, offset int64) {
	var b = new(bytes.Buffer)
	binary.Write(b, binary.LittleEndian, v)
	m.WriteAt(b.Bytes(), offset)
}

func (m *Memory) PutInt64(v int64, offset int64) {
	var b = new(bytes.Buffer)
	binary.Write(b, binary.LittleEndian, v)
	m.WriteAt(b.Bytes(), offset)
}

func (m *Memory) PutUint64(v uint64, offset int64) {
	binary.LittleEndian.PutUint64(m.Bytes()[offset:], v)
}

func (m *Memory) ReadAt(p []byte, offset int64) (int, error) {
	n := copy(p, m.Bytes()[offset:])
	return n, nil
}

func (m *Memory) WriteAt(p []byte, offset int64) (int, error) {
	n := copy(m.Bytes()[offset:], p)
	return n, nil
}

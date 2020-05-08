package fastlike

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"strings"
	"unsafe"

	"github.com/bytecodealliance/wasmtime-go"
)

// Each ABI method will need to read/write memory. Additionally, they'll need access to the request
// and response pools. The ABI operates on "handles", which are uint32 tokens that indicate which
// request / response / body, etc is being acted on.
// When creating a new request, the program will ask for a new request handle, where we'll allocate
// a request, stick it in a list, and then return a uint32 handle to it.
// ABI methods *can* access memory via the optional *Caller first argument, which means we *could*
// crete an ABI instance with access to request/response pools and have it pull memory from inside
// the method.
// However, ABI methods are wrapped per wasm store, and those stores are not sharable. Which gives
// us a 1:1 mapping of ABI to wasm instance.
type (
	requestHandle  int32
	responseHandle int32
	bodyHandle     int32

	// *Ptr variants are used on ABI calls to store handles
	// they point to an address in memory to write a handle
	requestHandlePtr  int32
	responseHandlePtr int32
	bodyHandlePtr     int32
)

func (i *Instance) wasm0() int32 {
	fmt.Println("wasm0")
	return 0
}

func (i *Instance) wasm1(a int32) int32 {
	p("wasm2", a)
	return 0
}

func (i *Instance) wasm2(a, b int32) int32 {
	p("wasm2", a, b)
	return 0
}

func (i *Instance) wasm3(a, b, c int32) int32 {
	p("wasm3", a, b, c)
	return 0
}

func (i *Instance) wasm4(a, b, c, d int32) int32 {
	p("wasm3", a, b, c, d)
	return 0
}

func (i *Instance) wasm5(a, b, c, d, e int32) int32 {
	p("wasm3", a, b, c, d, e)
	return 0
}

func (i *Instance) wasm6(a, b, c, d, e, f int32) int32 {
	p("wasm3", a, b, c, d, e, f)
	return 0
}

func (i *Instance) wasm7(a, b, c, d, e, f, g int32) int32 {
	p("wasm3", a, b, c, d, e, f, g)
	return 0
}

func (i *Instance) wasm8(a, b, c, d, e, f, g, h int32) int32 {
	p("wasm3", a, b, c, d, e, f, g, h)
	return 0
}

func (i *Instance) readmem(offset uintptr) uint8 {
	return i.memory.Data()[offset]
}

func (i *Instance) writemem(offset uintptr, val uint8) {
	i.memory.Data()[offset] = val
}

func (i *Instance) writeu32(offset uintptr, val uint32) {
	d := i.memory.Data()
	binary.LittleEndian.PutUint32(d[offset:], val)
}

func (i *Instance) writestr(offset uintptr, s []byte) {
	i.memory.WriteAt(s, int64(offset))
}

func (i *Instance) xqd_req_body_downstream_get(reqH int32, bodyH int32) int32 {
	fmt.Printf("xqd_req_body_downstream_get, rh=%d, bh=%d\n", reqH, bodyH)
	i.writeu32(uintptr(reqH), uint32(16))
	i.writeu32(uintptr(bodyH), uint32(17))
	fmt.Printf("readmem(reqH)=%+v\n", i.readmem(uintptr(reqH)))
	return 0
}

func (i *Instance) xqd_req_version_get(reqH int32, vers int32) int32 {
	fmt.Printf("xqd_req_version_get, rh=%d, vers=%d\n", reqH, vers)
	i.writeu32(uintptr(vers), uint32(2)) // http 1.1
	fmt.Printf("readmem(vers)=%+v\n", i.readmem(uintptr(vers)))
	return 0
}

func (i *Instance) xqd_req_version_set(reqH int32, vers int32) int32 {
	fmt.Printf("xqd_req_version_set, rh=%d, vers=%d\n", reqH, vers)
	return 0
}

func (i *Instance) xqd_req_method_get(reqH int32, methodptr int32, maxsz, writtensz int32) int32 {
	fmt.Printf("xqd_req_method_get, rh=%d, maxsz=%d, wsz=%d\n", reqH, maxsz, writtensz)
	i.writestr(uintptr(methodptr), []byte("GET"))
	i.writeu32(uintptr(writtensz), 3)
	return 0
}

func (i *Instance) xqd_req_uri_get(reqH int32, uriptr int32, maxsz, writtensz int32) int32 {
	fmt.Printf("xqd_req_uri_get, rh=%d, maxsz=%d, wsz=%d\n", reqH, maxsz, writtensz)
	uri := "https://google.com/lol"
	i.writestr(uintptr(uriptr), []byte(uri))
	i.writeu32(uintptr(writtensz), uint32(len(uri)))
	return 0
}

func (i *Instance) xqd_req_new(reqH int32) int32 {
	fmt.Printf("xqd_req_new, rh=%d\n", reqH)
	i.writeu32(uintptr(reqH), 8)
	return 0
}

type WasmMemory struct {
	memory *wasmtime.Memory
}

func (m *WasmMemory) ReadAt(p []byte, offset int64) (int, error) {
	n := copy(p, m.Data()[offset:])
	return n, nil
}

func (m *WasmMemory) WriteAt(p []byte, offset int64) (int, error) {
	n := copy(m.Data()[offset:], p)
	return n, nil
}

func (m *WasmMemory) Size() int64 {
	return int64(m.memory.DataSize())
}

func (m *WasmMemory) Data() []byte {
	var length = m.memory.DataSize()
	var data = (*uint8)(m.memory.Data())

	var header reflect.SliceHeader
	header = *(*reflect.SliceHeader)(unsafe.Pointer(&header))

	header.Data = uintptr(unsafe.Pointer(data))
	header.Len = int(length)
	header.Cap = int(length)

	var buf = *(*[]byte)(unsafe.Pointer(&header))
	return buf
}

func p(name string, args ...int32) {
	xs := []string{}
	for _, x := range args {
		xs = append(xs, fmt.Sprintf("%d", x))
	}

	fmt.Printf("called %s with %s\n", name, strings.Join(xs, ", "))
}

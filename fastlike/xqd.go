package fastlike

import (
	"fmt"
	"strings"
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

func (i *Instance) xqd_req_body_downstream_get(reqH int32, bodyH int32) int32 {
	fmt.Printf("xqd_req_body_downstream_get, rh=%d, bh=%d\n", reqH, bodyH)
	i.memory.PutUint32(16, int64(reqH))
	i.memory.PutUint32(16, int64(bodyH))
	fmt.Printf("readmem(reqH)=%+v\n", i.memory.Uint32(int64(reqH)))
	return 0
}

func (i *Instance) xqd_req_version_get(reqH int32, vers int32) int32 {
	fmt.Printf("xqd_req_version_get, rh=%d, vers=%d\n", reqH, vers)
	i.memory.PutUint32(2, int64(vers))
	fmt.Printf("readmem(vers)=%+v\n", i.memory.Uint32(int64(vers)))
	return 0
}

func (i *Instance) xqd_req_version_set(reqH int32, vers int32) int32 {
	fmt.Printf("xqd_req_version_set, rh=%d, vers=%d\n", reqH, vers)
	return 0
}

func (i *Instance) xqd_req_method_get(reqH int32, methodptr int32, maxsz, writtensz int32) int32 {
	fmt.Printf("xqd_req_method_get, rh=%d, maxsz=%d, wsz=%d\n", reqH, maxsz, writtensz)
	nw, err := i.memory.WriteAt([]byte("GET"), int64(methodptr))
	check(err)
	i.memory.PutUint32(uint32(nw), int64(writtensz))
	return 0
}

func (i *Instance) xqd_req_uri_get(reqH int32, uriptr int32, maxsz, writtensz int32) int32 {
	fmt.Printf("xqd_req_uri_get, rh=%d, maxsz=%d, wsz=%d\n", reqH, maxsz, writtensz)
	uri := "https://google.com/lol"
	nw, err := i.memory.WriteAt([]byte(uri), int64(uriptr))
	check(err)
	i.memory.PutUint32(uint32(nw), int64(writtensz))
	return 0
}

func (i *Instance) xqd_req_new(reqH int32) int32 {
	fmt.Printf("xqd_req_new, rh=%d\n", reqH)
	i.memory.PutUint32(8, int64(reqH))
	return 0
}

func (i *Instance) xqd_resp_new(wh int32) int32 {
	fmt.Printf("xqd_resp_new, wh=%d\n", wh)
	i.memory.PutUint32(8, int64(wh))
	return 0
}

func (i *Instance) xqd_init(abiv int32) int32 {
	fmt.Printf("xqd_init, abiv=%d\n", abiv)
	return 0
}

func (i *Instance) xqd_body_new(bh int32) int32 {
	fmt.Printf("xqd_body_new, bh=%d\n", bh)
	i.memory.PutUint32(8, int64(bh))
	return 0
}

func p(name string, args ...int32) {
	xs := []string{}
	for _, x := range args {
		xs = append(xs, fmt.Sprintf("%d", x))
	}

	fmt.Printf("called %s with %s\n", name, strings.Join(xs, ", "))
}

func (i *Instance) wasm0(name string) func() int32 {
	return func() int32 {
		p(name)
		return 0
	}
}

func (i *Instance) wasm1(name string) func(a int32) int32 {
	return func(a int32) int32 {
		p(name, a)
		return 0
	}
}

func (i *Instance) wasm2(name string) func(a, b int32) int32 {
	return func(a, b int32) int32 {
		p(name, a, b)
		return 0
	}
}

func (i *Instance) wasm3(name string) func(a, b, c int32) int32 {
	return func(a, b, c int32) int32 {
		p(name, a, b, c)
		return 0
	}
}

func (i *Instance) wasm4(name string) func(a, b, c, d int32) int32 {
	return func(a, b, c, d int32) int32 {
		p(name, a, b, c, d)
		return 0
	}
}

func (i *Instance) wasm5(name string) func(a, b, c, d, e int32) int32 {
	return func(a, b, c, d, e int32) int32 {
		p(name, a, b, c, d, e)
		return 0
	}
}

func (i *Instance) wasm6(name string) func(a, b, c, d, e, f int32) int32 {
	return func(a, b, c, d, e, f int32) int32 {
		p(name, a, b, c, d, e, f)
		return 0
	}
}

func (i *Instance) wasm7(name string) func(a, b, c, d, e, f, g int32) int32 {
	return func(a, b, c, d, e, f, g int32) int32 {
		p(name, a, b, c, d, e, f, g)
		return 0
	}
}

func (i *Instance) wasm8(name string) func(a, b, c, d, e, f, g, h int32) int32 {
	return func(a, b, c, d, e, f, g, h int32) int32 {
		p(name, a, b, c, d, e, f, g, h)
		return 0
	}
}

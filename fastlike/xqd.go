package fastlike

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

func (i *Instance) xqd_req_body_downstream_get(rh int32, bh int32) int32 {
	fmt.Printf("xqd_req_body_downstream_get, rh=%d, bh=%d\n", rh, bh)

	// The downstream request is always in handle 0
	i.memory.PutUint32(0, int64(rh))
	i.memory.PutUint32(0, int64(bh))
	return 0
}

func (i *Instance) xqd_body_write(bh int32, offset int32, bufsz int32, end int32, nwrittenptr int32) int32 {
	fmt.Printf("xqd_body_write, bh=%d, off=%d, bsz=%d\n", bh, offset, bufsz)

	// read the body from `off` until `bsz`, store the amount of data we read at `nwrittenptr`
	var buf = make([]byte, bufsz)
	nw, err := i.memory.ReadAt(buf, int64(offset))
	check(err)
	i.memory.PutUint32(uint32(nw), int64(nwrittenptr))
	fmt.Printf("read body:%s\n", string(buf))
	return 0
}

func (i *Instance) xqd_req_version_get(rh int32, addr int32) int32 {
	fmt.Printf("xqd_req_version_get, rh=%d, addr=%d\n", rh, addr)

	var r = i.requests[rh]
	var httpversion uint32
	switch r.Proto {
	case "HTTP/1.0":
		httpversion = 1
	case "HTTP/1.1":
		httpversion = 2
	case "HTTP/2":
		httpversion = 3
	case "HTTP/3":
		httpversion = 4
	}

	i.memory.PutUint32(httpversion, int64(addr))
	return 0
}

func (i *Instance) xqd_req_version_set(reqH int32, vers int32) int32 {
	fmt.Printf("xqd_req_version_set, rh=%d, vers=%d\n", reqH, vers)
	return 0
}

func (i *Instance) xqd_req_method_get(rh int32, addr int32, maxlen, nwrittenaddr int32) int32 {
	fmt.Printf("xqd_req_method_get, rh=%d, addr=%d\n", rh, addr)

	var r = i.requests[rh]
	nwritten, err := i.memory.WriteAt([]byte(r.Method), int64(addr))
	check(err)
	i.memory.PutUint32(uint32(nwritten), int64(nwrittenaddr))
	return 0
}

func (i *Instance) xqd_req_header_names_get(rh int32, addr int32, maxlen int32, cursor int32, ending_cursor_addr int32, nwrittenaddr int32) int32 {
	fmt.Printf("xqd_req_header_names_get, rh=%d, addr=%d, cursor=%d\n", rh, addr, cursor)
	// this abi method is used as party of "MultiValueHostCall"
	// the expectation is that each call writes header names to `addr`, seperated by a nul byte \0
	var r = i.requests[rh]
	var names = []string{"Host"}
	for n, _ := range r.Header {
		names = append(names, n)
	}

	// these names are explicitly unsorted, so let's sort them ourselves
	sort.Strings(names[1:])

	// slap them into a single string to determine cursor position
	namelist := strings.Join(names, "\x00")

	// append another null byte for the final terminator
	namelist += "\x00"

	fmt.Printf("header name length: %d\n", len(namelist))

	// determine where in the namelist we read from and where we stop
	var start = cursor
	var end = int(cursor + maxlen)
	if end > len(namelist) {
		end = len(namelist)
	}

	for n, b := range namelist[start:end] {
		i.memory.PutUint8(uint8(b), int64(addr+int32(n)))
	}

	nwritten := len(namelist[start:end])
	i.memory.PutUint32(uint32(nwritten), int64(nwrittenaddr))
	fmt.Printf("wrote %d bytes\n", nwritten)
	fmt.Printf("header list: %s\n", string(namelist[start:end]))

	// advance the cursor if there's anything left
	// it should advance by the number of bytes written
	var ec int32
	if end < len(namelist) {
		ec = int32(nwritten + 1)
	} else {
		ec = -1
	}

	fmt.Printf("set ending cursor to %d\n", ec)
	i.memory.PutInt32(ec, int64(ending_cursor_addr))

	return 0
}

func (i *Instance) xqd_req_header_values_get(rh int32, nameaddr int32, namelen int32, addr int32, maxlen int32, cursor int32, ending_cursor_addr int32, nwrittenaddr int32) int32 {
	fmt.Printf("xqd_req_header_values_get, rh=%d, nameaddr=%d, cursor=%d\n", rh, nameaddr, cursor)
	var r = i.requests[rh]

	// read namelen bytes at nameaddr for the name of the header that the caller wants
	var hdrb = make([]byte, namelen)
	i.memory.ReadAt(hdrb, int64(nameaddr))

	var hdr = http.CanonicalHeaderKey(string(hdrb))

	fmt.Printf("looking for header %s\n", hdr)

	var names = r.Header[hdr]
	if hdr == "Host" {
		names = []string{r.Host}
	}

	// these names are explicitly unsorted, so let's sort them ourselves
	sort.Strings(names[:])
	fmt.Printf("  we have %d values to send\n", len(names))

	if int(cursor+1) > len(names) {
		fmt.Printf("  asking for item %d, but we have a max of %d\n", cursor, len(names)-1)
		return 0
	} else if cursor == -1 {
		fmt.Printf("  asking for cursor -1...\n")
		return 0
	}

	// the cursor will be an index into the sorted slice of values
	v := names[cursor]
	v += "\x00"

	// write that value plus a terminator byte
	nwritten, err := i.memory.WriteAt([]byte(v), int64(addr))
	check(err)

	// if there's another value, increase the cursor
	if int32(len(names)) > cursor+1 {
		i.memory.PutInt32(cursor+1, int64(ending_cursor_addr))
	} else {
		i.memory.PutInt32(-1, int64(ending_cursor_addr))
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwrittenaddr))
	fmt.Printf("wrote %d bytes\n", nwritten)
	fmt.Printf("set cursor to %d\n", i.memory.Uint32(int64(ending_cursor_addr)))

	return 0
}

func (i *Instance) xqd_req_header_values_get2(rh int32, nameaddr int32, namelen int32, addr int32, maxlen int32, cursor int32, ending_cursor_addr int32, nwrittenaddr int32) int32 {
	fmt.Printf("xqd_req_header_values_get, rh=%d, nameaddr=%d, cursor=%d\n", rh, nameaddr, cursor)

	// read namelen bytes at nameaddr for the name of the header that the caller wants
	var hdrb = make([]byte, namelen)
	i.memory.ReadAt(hdrb, int64(nameaddr))

	var hdr = http.CanonicalHeaderKey(string(hdrb))

	fmt.Printf("looking for header %s\n", hdr)

	if cursor == -1 {
		fmt.Println("  returning early")
		i.memory.PutUint32(uint32(0), int64(nwrittenaddr))
		return 0
	}

	var r = i.requests[rh]
	var hv string
	if hdr == "Host" {
		hv = r.Host
	} else {
		hv = r.Header.Get(hdr)
	}

	hv += "\x00"
	var hvb = []byte(hv)
	for n, b := range []byte(hv) {
		i.memory.PutUint8(uint8(b), int64(addr+int32(n)))
	}
	for n, b := range []byte(hv) {
		i.memory.PutUint8(uint8(b), int64(addr+int32(n)))
	}
	fmt.Printf("wrote header value %s (%d bytes)\n", string(hvb), len(hvb))
	i.memory.PutUint32(uint32(len(hvb)*2), int64(nwrittenaddr))
	i.memory.PutInt32(int32(-1), int64(ending_cursor_addr))
	return 0
}

func (i *Instance) xqd_req_uri_get(rh int32, addr int32, maxlen, nwrittenaddr int32) int32 {
	fmt.Printf("xqd_req_uri_get, rh=%d, addr=%d\n", rh, addr)
	var r = i.requests[rh]
	nwritten, err := i.memory.WriteAt([]byte(r.URL.String()), int64(addr))
	check(err)
	i.memory.PutUint32(uint32(nwritten), int64(nwrittenaddr))
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

func (i *Instance) xqd_init(abiv int64) int32 {
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

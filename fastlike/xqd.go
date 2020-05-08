package fastlike

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
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

func (i *Instance) xqd_body_write(bh int32, addr int32, maxlen int32, end int32, nreadaddr int32) int32 {
	fmt.Printf("xqd_body_write, bh=%d, addr=%d, maxlen=%d\n", bh, addr, maxlen)

	i.bodies[bh] = make([]byte, maxlen)
	var body = i.bodies[bh]

	// copy the body from wasm memory addr to maxlen into our body
	nread, err := i.memory.ReadAt(body, int64(addr))
	check(err)
	i.memory.PutUint32(uint32(nread), int64(nreadaddr))

	fmt.Printf("\twrote body: %q\n", body)
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

func (i *Instance) xqd_req_version_set(reqH int32, httpversion int32) int32 {
	fmt.Printf("xqd_req_version_set, rh=%d, vers=%d\n", reqH, httpversion)

	// The default http version is http/1.1. Panic if we get anything else.
	if httpversion != 2 {
		panic("Unsupported HTTP version")
	}
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

func (i *Instance) xqd_req_method_set(rh int32, addr int32, size int32) int32 {
	fmt.Printf("xqd_req_method_set, rh=%d, addr=%d\n", rh, addr)

	var meth = make([]byte, size)
	i.memory.ReadAt(meth, int64(addr))

	var r = i.requests[rh]
	r.Method = string(meth)
	return 0
}

func (i *Instance) xqd_req_uri_set(rh int32, addr int32, size int32) int32 {
	fmt.Printf("xqd_req_uri_set, rh=%d, addr=%d\n", rh, addr)

	var uri = make([]byte, size)
	i.memory.ReadAt(uri, int64(addr))

	var u, err = url.Parse(string(uri))
	check(err)

	var r = i.requests[rh]
	r.URL = u
	return 0
}

func (i *Instance) xqd_req_header_names_get(rh int32, addr int32, maxlen int32, cursor int32, ending_cursor_addr int32, nwrittenaddr int32) int32 {
	fmt.Printf("xqd_req_header_names_get, rh=%d, addr=%d, cursor=%d\n", rh, addr, cursor)
	var r = i.requests[rh]
	var names = []string{"Host"}
	for n, _ := range r.Header {
		names = append(names, n)
	}

	// these names are explicitly unsorted, so let's sort them ourselves
	sort.Strings(names[1:])

	// and then join them together with a nul byte
	namelist := strings.Join(names, "\x00")

	// add another null byte to the end
	namelist += "\x00"

	// write the entire list, panic if it's over maxlen
	if int32(len(namelist)) > maxlen {
		panic("There's a bug in the ABI for Multi-Value Host Calls, where we cannot advance the cursor, which means we can't go over this single buffer.")
	}

	nwritten, err := i.memory.WriteAt([]byte(namelist), int64(addr))
	check(err)

	// read the names back out and pretty print
	var b2 = make([]byte, len(namelist))
	i.memory.ReadAt(b2, int64(addr))

	i.memory.PutUint32(uint32(nwritten), int64(nwrittenaddr))

	// Set the cursor to -1 to stop asking
	i.memory.PutInt64(-1, int64(ending_cursor_addr))

	fmt.Printf("  wrote %d bytes\n", nwritten)

	return 0
}

func (i *Instance) xqd_req_header_values_get(rh int32, nameaddr int32, namelen int32, addr int32, maxlen int32, cursor int32, ending_cursor_addr int32, nwrittenaddr int32) int32 {
	fmt.Printf("xqd_req_header_values_get, rh=%d, nameaddr=%d, cursor=%d\n", rh, nameaddr, cursor)
	var r = i.requests[rh]

	// read namelen bytes at nameaddr for the name of the header that the caller wants
	var hdrb = make([]byte, namelen)
	i.memory.ReadAt(hdrb, int64(nameaddr))

	var hdr = http.CanonicalHeaderKey(string(hdrb))

	fmt.Printf("\tlooking for header %s\n", hdr)

	var names = r.Header[hdr]
	if hdr == "Host" {
		names = []string{r.Host}
	}

	// these names are explicitly unsorted, so let's sort them ourselves
	sort.Strings(names[:])

	// and then join them together with a nul byte
	namelist := strings.Join(names, "\x00")

	// add another null byte to the end
	namelist += "\x00"

	// write the entire list, panic if it's over maxlen
	if int32(len(namelist)) > maxlen {
		panic("There's a bug in the ABI for Multi-Value Host Calls, where we cannot advance the cursor, which means we can't go over this single buffer.")
	}

	nwritten, err := i.memory.WriteAt([]byte(namelist), int64(addr))
	check(err)

	// read the names back out and pretty print
	var b2 = make([]byte, len(namelist))
	i.memory.ReadAt(b2, int64(addr))

	i.memory.PutUint32(uint32(nwritten), int64(nwrittenaddr))

	// Set the cursor to -1 to stop asking
	i.memory.PutInt64(-1, int64(ending_cursor_addr))

	fmt.Printf("\twrote %d bytes\n", nwritten)

	return 0
}

func (i *Instance) xqd_req_uri_get(rh int32, addr int32, maxlen, nwrittenaddr int32) int32 {
	fmt.Printf("xqd_req_uri_get, rh=%d, addr=%d\n", rh, addr)
	var r = i.requests[rh]
	uri := fmt.Sprintf("%s://%s%s", "http", r.Host, r.URL.String())
	fmt.Printf("\twrote url as %s\n", uri)
	nwritten, err := i.memory.WriteAt([]byte(uri), int64(addr))
	check(err)
	i.memory.PutUint32(uint32(nwritten), int64(nwrittenaddr))
	return 0
}

func (i *Instance) xqd_req_new(reqH int32) int32 {
	fmt.Printf("xqd_req_new, rh=%d\n", reqH)
	i.requests = append(i.requests, &http.Request{})
	i.memory.PutUint32(uint32(len(i.requests)-1), int64(reqH))
	return 0
}

func (i *Instance) xqd_resp_new(wh int32) int32 {
	fmt.Printf("xqd_resp_new, wh=%d\n", wh)
	i.responses = append(i.responses, &http.Response{})
	i.memory.PutUint32(uint32(len(i.responses)-1), int64(wh))
	return 0
}

func (i *Instance) xqd_req_send(rh int32, bh int32, backendOffset, backendSize int32, whaddr int32, bhaddr int32) int32 {
	// sends the request described by (rh, bh) to the backend
	// expects a response handle and response body handle
	fmt.Printf("xqd_req_send, rh=%d, bh=%d\n", rh, bh)

	var r = i.requests[rh]

	// pretty sure we can ignore this anyway
	var _ = i.bodies[bh]

	var backend = make([]byte, backendSize)
	i.memory.ReadAt(backend, int64(backendOffset))

	// TODO: Do we need to care about the backend?
	fmt.Printf("\tsending request to backend %s\n", string(backend))

	// The request they are sending may have been the original downstream request, we can't really
	// just *send* it again.
	var client = http.Client{}
	fmt.Printf("\tsending request %v\n", r)
	r.Body = ioutil.NopCloser(bytes.NewReader(nil))
	r.URL.Host = "localhost:8000"
	var w, err = client.Do(r)
	if err != nil {
		fmt.Printf("\tgot error? %s\n", err.Error())
	}
	check(err)

	i.responses = append(i.responses, w)
	var body, _ = ioutil.ReadAll(w.Body)
	i.bodies = append(i.bodies, body)

	fmt.Printf("\tgot body: %q\n", body)

	i.memory.PutUint32(uint32(len(i.responses)-1), int64(whaddr))
	i.memory.PutUint32(uint32(len(i.bodies)-1), int64(bhaddr))

	return 0
}

func (i *Instance) xqd_resp_status_set(wh int32, httpstatus int32) int32 {
	fmt.Printf("xqd_resp_status_set, wh=%d, status=%d\n", wh, httpstatus)
	w := i.responses[wh]
	w.StatusCode = int(httpstatus)
	return 0
}

func (i *Instance) xqd_resp_status_get(wh int32, addr int32) int32 {
	fmt.Printf("xqd_resp_status_get, wh=%d, addr=%d\n", wh, addr)
	w := i.responses[wh]
	i.memory.PutUint32(uint32(w.StatusCode), int64(addr))
	return 0
}

func (i *Instance) xqd_resp_version_set(wh int32, httpversion int32) int32 {
	fmt.Printf("xqd_resp_version_set, wh=%d, version=%d\n", wh, httpversion)
	// The default http version is http/1.1. Panic if we get anything else.
	// TODO: implement resp_version_get so we don't have to hardcode this to 2
	httpversion = 2
	if httpversion != 2 {
		panic("Unsupported HTTP version")
	}
	return 0
}

func (i *Instance) xqd_resp_send_downstream(wh int32, bh int32, stream int32) int32 {
	fmt.Printf("xqd_resp_send_downstream, wh=%d, bh=%d, stream=%d\n", wh, bh, stream)
	if stream != 0 {
		panic("Cannot stream responses...yet.")
	}

	var w, b = i.responses[wh], i.bodies[bh]
	fmt.Printf("\trw=%v, w=%v, b=%v\n", i.rw, w, b)

	// copy everything from `w` to `dw`, and then write the body
	for k, v := range w.Header {
		i.rw.Header()[k] = v
	}

	var bb, _ = httputil.DumpResponse(w, true)
	fmt.Printf("got: %q\n", bb)
	fmt.Printf("body: %q\n", string(b))

	var statusCode = w.StatusCode
	i.rw.WriteHeader(statusCode)
	io.Copy(i.rw, bytes.NewBuffer(b))

	return 0
}

func (i *Instance) xqd_init(abiv int64) int32 {
	fmt.Printf("xqd_init, abiv=%d\n", abiv)
	return 0
}

func (i *Instance) xqd_body_new(bh int32) int32 {
	fmt.Printf("xqd_body_new, bh=%d\n", bh)
	i.bodies = append(i.bodies, []byte{})
	i.memory.PutUint32(uint32(len(i.bodies)-1), int64(bh))
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

package fastlike

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

func (i *Instance) xqd_req_body_downstream_get(rh int32, bh int32) int32 {
	fmt.Printf("xqd_req_body_downstream_get, rh=%d, bh=%d\n", rh, bh)

	// Convert the downstream request into a (request, body) handle pair
	var rhh, rhandle = i.requests.New()
	rhandle.Request = i.ds_request

	// downstream requests don't carry host or scheme on their url for some dumb reason
	rhandle.URL.Host = rhandle.Host
	rhandle.URL.Scheme = "http"
	if rhandle.TLS != nil {
		rhandle.URL.Scheme = "https"
	}

	var bhh, _ = i.bodies.NewReader(rhandle.Body)

	i.memory.PutUint32(uint32(rhh), int64(rh))
	i.memory.PutUint32(uint32(bhh), int64(bh))
	return 0
}

func (i *Instance) xqd_body_write(bh int32, addr int32, maxlen int32, end int32, nreadaddr int32) XqdStatus {
	fmt.Printf("xqd_body_write, bh=%d, addr=%d, maxlen=%d\n", bh, addr, maxlen)

	// write maxlen bytes starting at addr to the body with handle bh
	var bhandle = i.bodies.Get(int(bh))
	if bhandle == nil {
		return XqdErrInvalidHandle
	}

	nread, err := io.CopyN(bhandle, bytes.NewReader(i.memory.Data()[addr:addr+maxlen]), int64(maxlen))
	check(err)
	i.memory.PutUint32(uint32(nread), int64(nreadaddr))
	return 0
}

func (i *Instance) xqd_req_version_get(rh int32, addr int32) int32 {
	fmt.Printf("xqd_req_version_get, rh=%d, addr=%d\n", rh, addr)
	i.memory.PutUint32(uint32(Http11), int64(addr))
	return 0
}

func (i *Instance) xqd_req_version_set(reqH int32, httpversion int32) int32 {
	fmt.Printf("xqd_req_version_set, rh=%d, vers=%d\n", reqH, httpversion)

	// The default http version is http/1.1. Panic if we get anything else.
	if httpversion != int32(Http11) {
		panic("Unsupported HTTP version")
	}
	return 0
}

func (i *Instance) xqd_req_method_get(rh int32, addr int32, maxlen, nwrittenaddr int32) XqdStatus {
	fmt.Printf("xqd_req_method_get, rh=%d, addr=%d\n", rh, addr)

	var rhandle = i.requests.Get(int(rh))
	if rhandle == nil {
		return XqdErrInvalidHandle
	}

	nwritten, err := i.memory.WriteAt([]byte(rhandle.Method), int64(addr))
	check(err)
	i.memory.PutUint32(uint32(nwritten), int64(nwrittenaddr))
	return 0
}

func (i *Instance) xqd_req_method_set(rh int32, addr int32, size int32) int32 {
	fmt.Printf("xqd_req_method_set, rh=%d, addr=%d\n", rh, addr)

	var meth = make([]byte, size)
	i.memory.ReadAt(meth, int64(addr))

	var r = i.requests.Get(int(rh))
	r.Method = string(meth)
	return 0
}

func (i *Instance) xqd_req_uri_set(rh int32, addr int32, size int32) int32 {
	fmt.Printf("xqd_req_uri_set, rh=%d, addr=%d\n", rh, addr)

	var uri = make([]byte, size)
	i.memory.ReadAt(uri, int64(addr))

	var u, err = url.Parse(string(uri))
	check(err)

	var r = i.requests.Get(int(rh))
	r.URL = u
	return 0
}

func (i *Instance) xqd_req_header_names_get(rh int32, addr int32, maxlen int32, cursor int32, ending_cursor_addr int32, nwrittenaddr int32) XqdStatus {
	fmt.Printf("xqd_req_header_names_get, rh=%d, addr=%d, cursor=%d\n", rh, addr, cursor)

	var r = i.requests.Get(int(rh))
	var names = []string{}
	for n, _ := range r.Header {
		names = append(names, n)
	}

	// these names are explicitly unsorted, so let's sort them ourselves
	sort.Strings(names[:])

	return multivalue(i.memory, names, addr, maxlen, cursor, ending_cursor_addr, nwrittenaddr)
}

func (i *Instance) xqd_req_header_values_get(rh int32, nameaddr int32, namelen int32, addr int32, maxlen int32, cursor int32, ending_cursor_addr int32, nwrittenaddr int32) XqdStatus {
	fmt.Printf("xqd_req_header_values_get, rh=%d, nameaddr=%d, cursor=%d\n", rh, nameaddr, cursor)

	var r = i.requests.Get(int(rh))

	// read namelen bytes at nameaddr for the name of the header that the caller wants
	var hdrb = make([]byte, namelen)
	i.memory.ReadAt(hdrb, int64(nameaddr))

	var hdr = http.CanonicalHeaderKey(string(hdrb))

	fmt.Printf("\tlooking for header %s\n", hdr)

	var values = r.Header.Values(hdr)

	// these names are explicitly unsorted, so let's sort them ourselves
	sort.Strings(values[:])

	return multivalue(i.memory, values, addr, maxlen, cursor, ending_cursor_addr, nwrittenaddr)
}

func (i *Instance) xqd_req_header_values_set(rh int32, nameaddr int32, namelen int32, addr int32, valuesz int32) int32 {
	fmt.Printf("xqd_req_header_values_set, rh=%d, nameaddr=%d\n", rh, nameaddr)
	var r = i.requests.Get(int(rh))

	// read namelen bytes at nameaddr for the name of the header that the caller wants to set
	var hdrb = make([]byte, namelen)
	i.memory.ReadAt(hdrb, int64(nameaddr))

	var hdr = http.CanonicalHeaderKey(string(hdrb))

	fmt.Printf("\tsetting values for for header %s\n", hdr)

	// read valuesz bytes from addr for a list of \0 terminated values for the header
	// but, read 1 less than that to avoid the trailing nul
	var valuebytes = make([]byte, valuesz-1)
	i.memory.ReadAt(valuebytes, int64(addr))

	var values = bytes.Split(valuebytes, []byte("\x00"))

	if r.Header == nil {
		r.Header = http.Header{}
	}

	for _, v := range values {
		fmt.Printf("\tadding value %q\n", v)
		r.Header.Add(hdr, string(v))
	}

	return 0
}

func (i *Instance) xqd_req_uri_get(rh int32, addr int32, maxlen, nwrittenaddr int32) int32 {
	fmt.Printf("xqd_req_uri_get, rh=%d, addr=%d\n", rh, addr)
	var r = i.requests.Get(int(rh))
	//uri := fmt.Sprintf("%s://%s%s", "http", r.Host, r.URL.String())
	uri := r.URL.String()
	fmt.Printf("\twrote url as %s\n", uri)
	nwritten, err := i.memory.WriteAt([]byte(uri), int64(addr))
	check(err)
	i.memory.PutUint32(uint32(nwritten), int64(nwrittenaddr))
	return 0
}

func (i *Instance) xqd_req_new(reqH int32) int32 {
	fmt.Printf("xqd_req_new, rh=%d\n", reqH)
	var rh, _ = i.requests.New()
	i.memory.PutUint32(uint32(rh), int64(reqH))
	return 0
}

func (i *Instance) xqd_resp_new(wh int32) int32 {
	fmt.Printf("xqd_resp_new, wh=%d\n", wh)
	var whh, _ = i.responses.New()
	i.memory.PutUint32(uint32(whh), int64(wh))
	return 0
}

func (i *Instance) xqd_req_send(rh int32, bh int32, backendOffset, backendSize int32, whaddr int32, bhaddr int32) int32 {
	// sends the request described by (rh, bh) to the backend
	// expects a response handle and response body handle
	fmt.Printf("xqd_req_send, rh=%d, bh=%d\n", rh, bh)

	var r = i.requests.Get(int(rh))
	var rb = i.bodies.Get(int(bh))

	var backend = make([]byte, backendSize)
	i.memory.ReadAt(backend, int64(backendOffset))

	fmt.Printf("\tsending request to backend %s\n", string(backend))

	fmt.Printf("\tsending request %v\n", r)
	// create a new http.Request using the values specified in the request handle

	// TODO: This panics if we don't replace the host with something that'll go somewhere else. By
	// default the host will be the original host, which is our proxy, which will send requests
	// back into wasm.
	req, err := http.NewRequest(r.Method, r.URL.String(), rb)
	check(err)

	for k, v := range r.Header {
		req.Header[http.CanonicalHeaderKey(k)] = v
	}

	// Make sure to add a CDN-Loop header, which we can check (and block) at ingress
	req.Header.Add("cdn-loop", "fastlike")

	var backendH = i.backends(string(backend))
	w, err := backendH(req)
	if err != nil {
		fmt.Printf("\tgot error? %s\n", err.Error())
	}
	check(err)

	// Convert the response into an (rh, bh) pair, put them in the list, and write out the handle
	// numbers
	var whh, whandle = i.responses.New()
	whandle.Status = w.Status
	whandle.StatusCode = w.StatusCode
	whandle.Header = w.Header.Clone()
	whandle.Body = w.Body

	var bhh, bhhandle = i.bodies.NewReader(whandle.Body)
	fmt.Printf("\tcreated response body handle %+v\n", bhhandle)

	i.memory.PutUint32(uint32(whh), int64(whaddr))
	i.memory.PutUint32(uint32(bhh), int64(bhaddr))

	return 0
}

func (i *Instance) xqd_resp_status_set(wh int32, httpstatus int32) int32 {
	fmt.Printf("xqd_resp_status_set, wh=%d, status=%d\n", wh, httpstatus)
	w := i.responses.Get(int(wh))
	w.StatusCode = int(httpstatus)
	w.Status = http.StatusText(w.StatusCode)
	return 0
}

func (i *Instance) xqd_resp_status_get(wh int32, addr int32) int32 {
	fmt.Printf("xqd_resp_status_get, wh=%d, addr=%d\n", wh, addr)
	w := i.responses.Get(int(wh))
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

	var w, b = i.responses.Get(int(wh)), i.bodies.Get(int(bh))
	fmt.Printf("\trw=%v, w=%v, b=%+v\n", i.ds_response, w, b)

	for k, v := range w.Header {
		i.ds_response.Header()[k] = v
	}

	i.ds_response.WriteHeader(w.StatusCode)

	io.Copy(i.ds_response, b)

	return 0
}

func (i *Instance) xqd_resp_header_names_get(rh int32, addr int32, maxlen int32, cursor int32, ending_cursor_addr int32, nwrittenaddr int32) XqdStatus {
	fmt.Printf("xqd_resp_header_names_get, rh=%d, addr=%d, cursor=%d\n", rh, addr, cursor)

	var r = i.responses.Get(int(rh))
	var names = []string{}
	for n, _ := range r.Header {
		names = append(names, n)
	}

	// these names are explicitly unsorted, so let's sort them ourselves
	sort.Strings(names[:])

	return multivalue(i.memory, names, addr, maxlen, cursor, ending_cursor_addr, nwrittenaddr)
}

func (i *Instance) xqd_resp_header_values_get(rh int32, nameaddr int32, namelen int32, addr int32, maxlen int32, cursor int32, ending_cursor_addr int32, nwrittenaddr int32) XqdStatus {
	fmt.Printf("xqd_resp_header_values_get, rh=%d, nameaddr=%d, cursor=%d\n", rh, nameaddr, cursor)

	var r = i.responses.Get(int(rh))

	// read namelen bytes at nameaddr for the name of the header that the caller wants
	var hdrb = make([]byte, namelen)
	i.memory.ReadAt(hdrb, int64(nameaddr))

	var hdr = http.CanonicalHeaderKey(string(hdrb))

	fmt.Printf("\tlooking for header %s\n", hdr)

	var values = r.Header.Values(hdr)

	// these names are explicitly unsorted, so let's sort them ourselves
	sort.Strings(values[:])

	return multivalue(i.memory, values, addr, maxlen, cursor, ending_cursor_addr, nwrittenaddr)
}

func (i *Instance) xqd_init(abiv int64) int32 {
	fmt.Printf("xqd_init, abiv=%d\n", abiv)
	return 0
}

func (i *Instance) xqd_body_new(bh int32) int32 {
	fmt.Printf("xqd_body_new, bh=%d\n", bh)
	var bhh, _ = i.bodies.NewBuffer()
	i.memory.PutUint32(uint32(bhh), int64(bh))
	return 0
}

func multivalue(memory *Memory, data []string, addr int32, maxlen int32, cursor int32, ending_cursor_addr int32, nwrittenaddr int32) XqdStatus {
	// If there's no data, return early
	if len(data) == 0 {
		memory.PutUint32(uint32(0), int64(nwrittenaddr))

		// Set the cursor to -1 to stop asking
		memory.PutInt64(-1, int64(ending_cursor_addr))
		return XqdStatusOK
	}

	// If the cursor points past our slice, return early
	if int(cursor) >= len(data) {
		memory.PutUint32(uint32(0), int64(nwrittenaddr))

		// Set the cursor to -1 to stop asking
		memory.PutInt64(-1, int64(ending_cursor_addr))
		return XqdStatusOK
	}

	var v = []byte(data[cursor])
	v = append(v, '\x00')

	nwritten, err := memory.WriteAt(v, int64(addr))
	check(err)

	memory.PutUint32(uint32(nwritten), int64(nwrittenaddr))

	// If there's more entries, set the cursor to +1
	var ec int
	if int(cursor) < len(data)-1 {
		ec = int(cursor) + 1
	} else {
		ec = -1
	}

	memory.PutInt64(int64(ec), int64(ending_cursor_addr))

	return XqdStatusOK
}

func p(name string, args ...int32) {
	xs := []string{}
	for _, x := range args {
		xs = append(xs, fmt.Sprintf("%d", x))
	}

	fmt.Printf(":STUB: %s with %s\n", name, strings.Join(xs, ", "))
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

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
	var rhandle = &requestHandle{}
	var bhandle = &bodyHandle{}

	rhandle.headers = i.ds_request.Header.Clone()
	rhandle.headers.Set("host", i.ds_request.Host)
	rhandle.method = i.ds_request.Method

	// TODO: Support http other than 1.1?
	rhandle.version = Http11

	var scheme = "http"
	if i.ds_request.TLS != nil {
		scheme = "https"
	}

	var urlstr = fmt.Sprintf("%s://%s%s", scheme, i.ds_request.Host, i.ds_request.URL.String())
	var uri, err = url.Parse(urlstr)
	check(err)
	rhandle.url = uri

	io.Copy(bhandle, i.ds_request.Body)
	defer i.ds_request.Body.Close()

	i.requests = append(i.requests, rhandle)
	i.bodies = append(i.bodies, bhandle)

	i.memory.PutUint32(uint32(len(i.requests)-1), int64(rh))
	i.memory.PutUint32(uint32(len(i.bodies)-1), int64(bh))
	return 0
}

func (i *Instance) xqd_body_write(bh int32, addr int32, maxlen int32, end int32, nreadaddr int32) int32 {
	fmt.Printf("xqd_body_write, bh=%d, addr=%d, maxlen=%d\n", bh, addr, maxlen)

	// write maxlen bytes starting at addr to the body with handle bh
	nread, err := io.CopyN(i.bodies[bh], bytes.NewReader(i.memory.Data()[addr:addr+maxlen]), int64(maxlen))
	check(err)
	i.memory.PutUint32(uint32(nread), int64(nreadaddr))
	return 0
}

func (i *Instance) xqd_req_version_get(rh int32, addr int32) int32 {
	fmt.Printf("xqd_req_version_get, rh=%d, addr=%d\n", rh, addr)

	var r = i.requests[rh]
	i.memory.PutUint32(uint32(r.version), int64(addr))
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
	nwritten, err := i.memory.WriteAt([]byte(r.method), int64(addr))
	check(err)
	i.memory.PutUint32(uint32(nwritten), int64(nwrittenaddr))
	return 0
}

func (i *Instance) xqd_req_method_set(rh int32, addr int32, size int32) int32 {
	fmt.Printf("xqd_req_method_set, rh=%d, addr=%d\n", rh, addr)

	var meth = make([]byte, size)
	i.memory.ReadAt(meth, int64(addr))

	var r = i.requests[rh]
	r.method = string(meth)
	return 0
}

func (i *Instance) xqd_req_uri_set(rh int32, addr int32, size int32) int32 {
	fmt.Printf("xqd_req_uri_set, rh=%d, addr=%d\n", rh, addr)

	var uri = make([]byte, size)
	i.memory.ReadAt(uri, int64(addr))

	var u, err = url.Parse(string(uri))
	check(err)

	var r = i.requests[rh]
	r.url = u
	return 0
}

func (i *Instance) xqd_req_header_names_get(rh int32, addr int32, maxlen int32, cursor int32, ending_cursor_addr int32, nwrittenaddr int32) int32 {
	fmt.Printf("xqd_req_header_names_get, rh=%d, addr=%d, cursor=%d\n", rh, addr, cursor)
	var r = i.requests[rh]
	var names = []string{}
	for n, _ := range r.headers {
		names = append(names, n)
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

	var names = r.headers[hdr]

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

func (i *Instance) xqd_req_header_values_set(rh int32, nameaddr int32, namelen int32, addr int32, valuesz int32) int32 {
	fmt.Printf("xqd_req_header_values_set, rh=%d, nameaddr=%d\n", rh, nameaddr)
	var r = i.requests[rh]

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

	if r.headers == nil {
		r.headers = http.Header{}
	}

	for _, v := range values {
		fmt.Printf("\tadding value %q\n", v)
		r.headers.Add(hdr, string(v))
	}

	return 0
}

func (i *Instance) xqd_req_uri_get(rh int32, addr int32, maxlen, nwrittenaddr int32) int32 {
	fmt.Printf("xqd_req_uri_get, rh=%d, addr=%d\n", rh, addr)
	var r = i.requests[rh]
	//uri := fmt.Sprintf("%s://%s%s", "http", r.Host, r.URL.String())
	uri := r.url.String()
	fmt.Printf("\twrote url as %s\n", uri)
	nwritten, err := i.memory.WriteAt([]byte(uri), int64(addr))
	check(err)
	i.memory.PutUint32(uint32(nwritten), int64(nwrittenaddr))
	return 0
}

func (i *Instance) xqd_req_new(reqH int32) int32 {
	fmt.Printf("xqd_req_new, rh=%d\n", reqH)
	i.requests = append(i.requests, &requestHandle{})
	i.memory.PutUint32(uint32(len(i.requests)-1), int64(reqH))
	return 0
}

func (i *Instance) xqd_resp_new(wh int32) int32 {
	fmt.Printf("xqd_resp_new, wh=%d\n", wh)
	i.responses = append(i.responses, &responseHandle{})
	i.memory.PutUint32(uint32(len(i.responses)-1), int64(wh))
	return 0
}

func (i *Instance) xqd_req_send(rh int32, bh int32, backendOffset, backendSize int32, whaddr int32, bhaddr int32) int32 {
	// sends the request described by (rh, bh) to the backend
	// expects a response handle and response body handle
	fmt.Printf("xqd_req_send, rh=%d, bh=%d\n", rh, bh)

	var r = i.requests[rh]
	var rb = i.bodies[bh]

	var backend = make([]byte, backendSize)
	i.memory.ReadAt(backend, int64(backendOffset))

	fmt.Printf("\tsending request to backend %s\n", string(backend))

	fmt.Printf("\tsending request %v\n", r)
	// create a new http.Request using the values specified in the request handle
	req, err := http.NewRequest(r.method, r.url.String(), rb)
	check(err)
	w, err := i.subrequest(string(backend), req)
	if err != nil {
		fmt.Printf("\tgot error? %s\n", err.Error())
	}
	check(err)

	// Convert the response into an (rh, bh) pair, put them in the list, and write out the handle
	// numbers
	var whandle = &responseHandle{}
	switch w.Proto {
	case "HTTP/0.9":
		whandle.version = Http09
	case "HTTP/1.0":
		whandle.version = Http10
	case "HTTP/1.1":
		whandle.version = Http11
	case "HTTP/2":
		whandle.version = Http2
	case "HTTP/3":
		whandle.version = Http3
	}
	whandle.status = w.StatusCode
	whandle.headers = w.Header.Clone()

	i.responses = append(i.responses, whandle)

	// and stick the body into a new body handle
	// TODO: Figure out how to stream this? w.Body is a ReadCloser
	// we could change body handles to be io.Reader but then we won't be able to write it..
	// if it was an io.ReadWriteCloser then we could in this case set it up with a discarding
	// writer?
	var bhandle = &bodyHandle{}
	io.Copy(bhandle, w.Body)
	i.bodies = append(i.bodies, bhandle)

	i.memory.PutUint32(uint32(len(i.responses)-1), int64(whaddr))
	i.memory.PutUint32(uint32(len(i.bodies)-1), int64(bhaddr))

	return 0
}

func (i *Instance) xqd_resp_status_set(wh int32, httpstatus int32) int32 {
	fmt.Printf("xqd_resp_status_set, wh=%d, status=%d\n", wh, httpstatus)
	w := i.responses[wh]
	w.status = int(httpstatus)
	return 0
}

func (i *Instance) xqd_resp_status_get(wh int32, addr int32) int32 {
	fmt.Printf("xqd_resp_status_get, wh=%d, addr=%d\n", wh, addr)
	w := i.responses[wh]
	i.memory.PutUint32(uint32(w.status), int64(addr))
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
	fmt.Printf("\trw=%v, w=%v, b=%v\n", i.ds_response, w, b)

	for k, v := range w.headers {
		i.ds_response.Header()[k] = v
	}

	i.ds_response.WriteHeader(w.status)

	io.Copy(i.ds_response, b)

	return 0
}

func (i *Instance) xqd_init(abiv int64) int32 {
	fmt.Printf("xqd_init, abiv=%d\n", abiv)
	return 0
}

func (i *Instance) xqd_body_new(bh int32) int32 {
	fmt.Printf("xqd_body_new, bh=%d\n", bh)
	i.bodies = append(i.bodies, &bodyHandle{})
	i.memory.PutUint32(uint32(len(i.bodies)-1), int64(bh))
	return 0
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

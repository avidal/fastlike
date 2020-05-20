package fastlike

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

func (i *Instance) xqd_req_version_get(handle int32, version_out int32) XqdStatus {
	fmt.Printf("xqd_req_version_get, rh=%d, addr=%d\n", handle, version_out)

	if i.requests.Get(int(handle)) == nil {
		return XqdErrInvalidHandle
	}

	i.memory.PutUint32(uint32(Http11), int64(version_out))
	return XqdStatusOK
}

func (i *Instance) xqd_req_version_set(handle int32, version int32) XqdStatus {
	fmt.Printf("xqd_req_version_set, rh=%d, version=%d\n", handle, version)

	if i.requests.Get(int(handle)) == nil {
		return XqdErrInvalidHandle
	}

	if version != int32(Http11) {
		return XqdErrUnsupported
	}

	return XqdStatusOK
}

func (i *Instance) xqd_req_cache_override_set(handle int32, tag int32, ttl int32, swr int32) XqdStatus {
	// We don't actually *do* anything with cache overrides, since we don't have or need a cache.
	fmt.Printf("xqd_req_cache_override_set, rh=%d, tag=%d, ttl=%d, swr=%d\n", handle, tag, ttl, swr)

	if i.requests.Get(int(handle)) == nil {
		return XqdErrInvalidHandle
	}

	return XqdStatusOK
}

func (i *Instance) xqd_req_method_get(handle int32, addr int32, maxlen int32, nwritten_out int32) XqdStatus {
	fmt.Printf("xqd_req_method_get, rh=%d, addr=%d\n", handle, addr)

	var r = i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	if int(maxlen) < len(r.Method) {
		return XqdErrBufferLength
	}

	nwritten, err := i.memory.WriteAt([]byte(r.Method), int64(addr))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	return XqdStatusOK
}

func (i *Instance) xqd_req_method_set(handle int32, addr int32, size int32) XqdStatus {
	fmt.Printf("xqd_req_method_set, rh=%d, addr=%d\n", handle, addr)

	var r = i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	var method = make([]byte, size)
	var _, err = i.memory.ReadAt(method, int64(addr))
	if err != nil {
		return XqdError
	}

	// Make sure the method is in the set of valid http methods
	var methods = strings.Join([]string{
		http.MethodGet,
		http.MethodHead,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodConnect,
		http.MethodOptions,
		http.MethodTrace,
	}, "\x00")

	if !strings.Contains(methods, strings.ToUpper(string(method))) {
		return XqdErrHttpParse
	}

	r.Method = strings.ToUpper(string(method))
	return XqdStatusOK
}

func (i *Instance) xqd_req_uri_set(handle int32, addr int32, size int32) XqdStatus {
	fmt.Printf("xqd_req_uri_set, rh=%d, addr=%d\n", handle, addr)

	var r = i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	var buf = make([]byte, size)
	var nread, err = i.memory.ReadAt(buf, int64(addr))
	if err != nil {
		return XqdError
	}
	if nread < int(size) {
		return XqdError
	}

	u, err := url.Parse(string(buf))
	if err != nil {
		return XqdErrHttpParse
	}

	r.URL = u
	return XqdStatusOK
}

func (i *Instance) xqd_req_header_names_get(handle int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) XqdStatus {
	fmt.Printf("xqd_req_header_names_get, rh=%d, addr=%d, cursor=%d\n", handle, addr, cursor)

	var r = i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	var names = []string{}
	for n, _ := range r.Header {
		names = append(names, n)
	}

	// these names are explicitly unsorted, so let's sort them ourselves
	sort.Strings(names[:])

	return xqd_multivalue(i.memory, names, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

func (i *Instance) xqd_req_header_values_get(handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) XqdStatus {
	fmt.Printf("xqd_req_header_values_get, rh=%d, nameaddr=%d, cursor=%d\n", handle, name_addr, cursor)

	var r = i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	var buf = make([]byte, name_size)
	var _, err = i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	var header = http.CanonicalHeaderKey(string(buf))
	fmt.Printf("\tlooking for header %s\n", header)

	var values, ok = r.Header[header]
	if !ok {
		values = []string{}
	}

	// Sort the values otherwise cursors don't work
	sort.Strings(values[:])

	return xqd_multivalue(i.memory, values, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

func (i *Instance) xqd_req_header_values_set(handle int32, name_addr int32, name_size int32, values_addr int32, values_size int32) XqdStatus {
	fmt.Printf("xqd_req_header_values_set, rh=%d, nameaddr=%d\n", handle, name_addr)
	var r = i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	var buf = make([]byte, name_size)
	var _, err = i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	var header = http.CanonicalHeaderKey(string(buf))
	fmt.Printf("\tsetting values for for header %s\n", header)

	// read values_size bytes from values_addr for a list of \0 terminated values for the header
	// but, read 1 less than that to avoid the trailing nul
	buf = make([]byte, values_size-1)
	_, err = i.memory.ReadAt(buf, int64(values_addr))
	if err != nil {
		return XqdError
	}

	var values = bytes.Split(buf, []byte("\x00"))

	if r.Header == nil {
		r.Header = http.Header{}
	}

	for _, v := range values {
		fmt.Printf("\tadding value %q\n", v)
		r.Header.Add(header, string(v))
	}

	return XqdStatusOK
}

func (i *Instance) xqd_req_uri_get(handle int32, addr int32, maxlen int32, nwritten_out int32) XqdStatus {
	fmt.Printf("xqd_req_uri_get, rh=%d, addr=%d\n", handle, addr)

	var r = i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	uri := r.URL.String()
	fmt.Printf("\tsending url %s\n", uri)
	nwritten, err := i.memory.WriteAt([]byte(uri), int64(addr))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	return XqdStatusOK
}

func (i *Instance) xqd_req_new(handle_out int32) XqdStatus {
	fmt.Printf("xqd_req_new, rh_out=%d\n", handle_out)
	var rhid, _ = i.requests.New()
	i.memory.PutUint32(uint32(rhid), int64(handle_out))
	return XqdStatusOK
}

func (i *Instance) xqd_req_send(rhandle int32, bhandle int32, backend_addr, backend_size int32, wh_out int32, bh_out int32) XqdStatus {
	// sends the request described by (rh, bh) to the backend
	// expects a response handle and response body handle
	fmt.Printf("xqd_req_send, rh=%d, bh=%d\n", rhandle, bhandle)

	var r = i.requests.Get(int(rhandle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	var b = i.bodies.Get(int(bhandle))
	if b == nil {
		return XqdErrInvalidHandle
	}

	var buf = make([]byte, backend_size)
	_, err := i.memory.ReadAt(buf, int64(backend_addr))
	if err != nil {
		return XqdError
	}

	var backend = string(buf)

	fmt.Printf("\tsending request to backend %s\n", backend)
	fmt.Printf("\tsending request %v\n", r.URL.String())

	req, err := http.NewRequest(r.Method, r.URL.String(), b)
	if err != nil {
		return XqdErrHttpUserInvalid
	}

	req.Header = r.Header.Clone()

	// TODO: Ensure we always have something in r.Header so we can avoid the nil check here
	if req.Header == nil {
		req.Header = http.Header{}
	}

	// Make sure to add a CDN-Loop header, which we can check (and block) at ingress
	req.Header.Add("cdn-loop", "fastlike")

	// If the backend is geolocation, we select the geobackend explicitly
	var transport Backend
	if backend == "geolocation" {
		transport = i.geobackend
	} else {
		transport = i.backends(backend)
	}

	w, err := transport(req)
	if err != nil {
		return XqdError
	}

	// Convert the response into an (rh, bh) pair, put them in the list, and write out the handles
	var whid, wh = i.responses.New()
	wh.Status = w.Status
	wh.StatusCode = w.StatusCode
	wh.Header = w.Header.Clone()
	wh.Body = w.Body

	var bhid, _ = i.bodies.NewReader(wh.Body)

	i.memory.PutUint32(uint32(whid), int64(wh_out))
	i.memory.PutUint32(uint32(bhid), int64(bh_out))

	return XqdStatusOK
}

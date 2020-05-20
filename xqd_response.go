package fastlike

import (
	"bytes"
	"fmt"
	"net/http"
	"sort"
)

func (i *Instance) xqd_resp_new(handle_out int32) XqdStatus {
	fmt.Printf("xqd_resp_new, wh=%d\n", handle_out)
	var whid, _ = i.responses.New()
	i.memory.PutUint32(uint32(whid), int64(handle_out))
	return XqdStatusOK
}

func (i *Instance) xqd_resp_status_set(handle int32, status int32) XqdStatus {
	fmt.Printf("xqd_resp_status_set, wh=%d, status=%d\n", handle, status)
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	w.StatusCode = int(status)
	w.Status = http.StatusText(w.StatusCode)
	return XqdStatusOK
}

func (i *Instance) xqd_resp_status_get(handle int32, status_out int32) XqdStatus {
	fmt.Printf("xqd_resp_status_get, wh=%d, addr=%d\n", handle, status_out)
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	i.memory.PutUint32(uint32(w.StatusCode), int64(status_out))
	return XqdStatusOK
}

func (i *Instance) xqd_resp_version_set(handle int32, version int32) XqdStatus {
	fmt.Printf("xqd_resp_version_set, wh=%d, version=%d\n", handle, version)
	if i.responses.Get(int(handle)) == nil {
		return XqdErrInvalidHandle
	}

	return XqdStatusOK
}

func (i *Instance) xqd_resp_version_get(handle int32, version_out int32) XqdStatus {
	fmt.Printf("xqd_resp_version_get, wh=%d, addr=%d\n", handle, version_out)

	if i.responses.Get(int(handle)) == nil {
		return XqdErrInvalidHandle
	}

	i.memory.PutUint32(uint32(Http11), int64(version_out))
	return XqdStatusOK
}

func (i *Instance) xqd_resp_header_names_get(handle int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) XqdStatus {
	fmt.Printf("xqd_resp_header_names_get, wh=%d, addr=%d, cursor=%d\n", handle, addr, cursor)

	var w = i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	var names = []string{}
	for n, _ := range w.Header {
		names = append(names, n)
	}

	// these names are explicitly unsorted, so let's sort them ourselves
	sort.Strings(names[:])

	return xqd_multivalue(i.memory, names, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

func (i *Instance) xqd_resp_header_values_get(handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) XqdStatus {
	fmt.Printf("xqd_resp_header_values_get, wh=%d, nameaddr=%d, cursor=%d\n", handle, name_addr, cursor)

	var w = i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	var buf = make([]byte, name_size)
	var _, err = i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	var header = http.CanonicalHeaderKey(string(buf))
	var values, ok = w.Header[header]
	if !ok {
		values = []string{}
	}

	fmt.Printf("\tlooking for header %s\n", header)

	// Sort the values otherwise cursors don't work
	sort.Strings(values[:])

	return xqd_multivalue(i.memory, values, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

func (i *Instance) xqd_resp_header_values_set(handle int32, name_addr int32, name_size int32, values_addr int32, values_size int32) XqdStatus {
	fmt.Printf("xqd_resp_header_values_set, wh=%d, nameaddr=%d\n", handle, name_addr)
	var w = i.responses.Get(int(handle))
	if w == nil {
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

	if w.Header == nil {
		w.Header = http.Header{}
	}

	for _, v := range values {
		fmt.Printf("\tadding value %q\n", v)
		w.Header.Add(header, string(v))
	}

	return XqdStatusOK
}

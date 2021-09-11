package fastlike

import (
	"bytes"
	"net/http"
	"sort"
)

func (i *Instance) xqd_resp_new(handle_out int32) int32 {
	var whid, _ = i.responses.New()
	i.abilog.Printf("resp_new handle=%d\n", whid)
	i.memory.PutUint32(uint32(whid), int64(handle_out))
	return XqdStatusOK
}

func (i *Instance) xqd_resp_status_set(handle int32, status int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_status_set: handle=%d status=%d", handle, status)

	w.StatusCode = int(status)
	w.Status = http.StatusText(w.StatusCode)
	return XqdStatusOK
}

func (i *Instance) xqd_resp_status_get(handle int32, status_out int32) int32 {
	w := i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_status_get: handle=%d status=%d", handle, w.StatusCode)
	i.memory.PutUint32(uint32(w.StatusCode), int64(status_out))
	return XqdStatusOK
}

func (i *Instance) xqd_resp_version_set(handle int32, version int32) int32 {
	i.abilog.Printf("resp_version_set: handle=%d version=%d", handle, version)

	if i.responses.Get(int(handle)) == nil {
		return XqdErrInvalidHandle
	}

	if int32(version) != Http11 {
		i.abilog.Printf("resp_version_set: unsupported version=%d", version)
	}

	return XqdStatusOK
}

func (i *Instance) xqd_resp_version_get(handle int32, version_out int32) int32 {
	if i.responses.Get(int(handle)) == nil {
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("resp_version_get: handle=%d version=%d", handle, Http11)

	i.memory.PutUint32(uint32(Http11), int64(version_out))
	return XqdStatusOK
}

func (i *Instance) xqd_resp_header_names_get(handle int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
	i.abilog.Printf("resp_header_names_get: handle=%d cursor=%d", handle, cursor)

	var w = i.responses.Get(int(handle))
	if w == nil {
		return XqdErrInvalidHandle
	}

	var names = []string{}
	for n := range w.Header {
		names = append(names, n)
	}

	// these names are explicitly unsorted, so let's sort them ourselves
	sort.Strings(names[:])

	return xqd_multivalue(i.memory, names, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

func (i *Instance) xqd_resp_header_remove(handle int32, name_addr int32, name_size int32) int32 {
	var r = i.requests.Get(int(handle))
	if r == nil {
		return XqdErrInvalidHandle
	}

	var name = make([]byte, name_size)
	var _, err = i.memory.ReadAt(name, int64(name_addr))
	if err != nil {
		return XqdError
	}

	r.Header.Del(string(name))

	return XqdStatusOK
}

func (i *Instance) xqd_resp_header_values_get(handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
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

	i.abilog.Printf("resp_header_values_get: handle=%d header=%q cursor=%d\n", handle, header, cursor)

	// Sort the values otherwise cursors don't work
	sort.Strings(values[:])

	return xqd_multivalue(i.memory, values, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

func (i *Instance) xqd_resp_header_values_set(handle int32, name_addr int32, name_size int32, values_addr int32, values_size int32) int32 {
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

	// read values_size bytes from values_addr for a list of \0 terminated values for the header
	// but, read 1 less than that to avoid the trailing nul
	buf = make([]byte, values_size-1)
	_, err = i.memory.ReadAt(buf, int64(values_addr))
	if err != nil {
		return XqdError
	}

	var values = bytes.Split(buf, []byte("\x00"))

	i.abilog.Printf("resp_header_values_set: handle=%d header=%q values=%q\n", handle, header, values)

	if w.Header == nil {
		w.Header = http.Header{}
	}

	for _, v := range values {
		w.Header.Add(header, string(v))
	}

	return XqdStatusOK
}

func (i *Instance) xqd_resp_close(handle int32) {
	i.responses.Get(int(handle)).Close = true
}

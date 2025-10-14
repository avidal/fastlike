package fastlike

import (
	"bytes"
	"io"
)

func (i *Instance) xqd_body_new(handle_out int32) int32 {
	var bhid, _ = i.bodies.NewBuffer()
	i.abilog.Printf("body_new: handle=%d", bhid)
	i.memory.PutUint32(uint32(bhid), int64(handle_out))
	return XqdStatusOK
}

func (i *Instance) xqd_body_write(handle int32, addr int32, size int32, body_end int32, nwritten_out int32) int32 {
	// TODO: Figure out what we're supposed to do with `body_end` which can be 0 (back) or
	// 1 (front)
	i.abilog.Printf("body_write: handle=%d size=%d, body_end=%d", handle, size, body_end)

	var body = i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Copy size bytes starting at addr into the body handle
	nwritten, err := io.CopyN(body, bytes.NewReader(i.memory.Data()[addr:]), int64(size))
	if err != nil {
		// TODO: If err == EOF then there's a specific error code we can return (it means they
		// didn't have `size` bytes in memory)
		return XqdError
	}

	// Write out how many bytes we copied
	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))

	return XqdStatusOK
}

func (i *Instance) xqd_body_read(handle int32, addr int32, maxlen int32, nread_out int32) int32 {
	var body = i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	var buf = bytes.NewBuffer(make([]byte, 0, maxlen))
	var ncopied, err = io.Copy(buf, io.LimitReader(body, int64(maxlen)))
	if err != nil {
		i.abilog.Printf("body_read: error copying got=%s", err.Error())
		return XqdError
	}

	var nwritten, err2 = i.memory.WriteAt(buf.Bytes(), int64(addr))
	if err2 != nil {
		i.abilog.Printf("body_read: error writing got=%s", err2.Error())
		return XqdError
	}

	if ncopied != int64(nwritten) {
		i.abilog.Printf("body_read: error copying copied=%d wrote=%d", ncopied, nwritten)
		return XqdError
	}

	i.abilog.Printf("body_read: handle=%d copied=%d", handle, ncopied)

	// Write out how many bytes we copied
	i.memory.PutUint32(uint32(nwritten), int64(nread_out))

	return XqdStatusOK
}

func (i *Instance) xqd_body_append(dst_handle int32, src_handle int32) int32 {
	i.abilog.Printf("body_append: dst=%d src=%d", dst_handle, src_handle)

	var dst = i.bodies.Get(int(dst_handle))
	if dst == nil {
		return XqdErrInvalidHandle
	}

	var src = i.bodies.Get(int(src_handle))
	if src == nil {
		return XqdErrInvalidHandle
	}

	// replace the destination reader with a multireader that reads first from the original reader
	// and then from the source
	dst.reader = io.MultiReader(dst.reader, src)

	return XqdStatusOK
}

func (i *Instance) xqd_body_close(handle int32) int32 {
	var body = i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	if err := body.Close(); err != nil {
		return XqdErrInvalidHandle
	}

	return XqdStatusOK
}

func (i *Instance) xqd_body_trailer_append(handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
	var body = i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Read the trailer name
	var name = make([]byte, name_size)
	var _, err = i.memory.ReadAt(name, int64(name_addr))
	if err != nil {
		return XqdError
	}

	// Read the trailer value
	var value = make([]byte, value_size)
	_, err = i.memory.ReadAt(value, int64(value_addr))
	if err != nil {
		return XqdError
	}

	// Initialize the trailer map if it doesn't exist
	if body.trailers == nil {
		body.trailers = make(map[string][]string)
	}

	// Append the trailer (using Add which appends to existing values)
	body.trailers.Add(string(name), string(value))

	i.abilog.Printf("body_trailer_append: handle=%d name=%q value=%q", handle, string(name), string(value))

	return XqdStatusOK
}

func (i *Instance) xqd_body_trailer_names_get(handle int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
	i.abilog.Printf("body_trailer_names_get: handle=%d cursor=%d", handle, cursor)

	var body = i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	var names = []string{}
	for n := range body.trailers {
		names = append(names, n)
	}

	return xqd_multivalue(i.memory, names, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

func (i *Instance) xqd_body_trailer_value_get(handle int32, name_addr int32, name_size int32, value_addr int32, value_maxlen int32, nwritten_out int32) int32 {
	var body = i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Read the trailer name
	var nameBuf = make([]byte, name_size)
	var _, err = i.memory.ReadAt(nameBuf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	// Get the first value for this trailer name
	var values = body.trailers.Values(string(nameBuf))
	if len(values) == 0 {
		i.memory.PutUint32(0, int64(nwritten_out))
		return XqdStatusOK
	}

	// Get the first value
	var value = []byte(values[0])

	// Check if buffer is large enough
	if len(value)+1 > int(value_maxlen) {
		return XqdErrBufferLength
	}

	// Append null terminator
	value = append(value, '\x00')

	// Write to memory
	nwritten, err := i.memory.WriteAt(value, int64(value_addr))
	if err != nil {
		return XqdError
	}

	i.abilog.Printf("body_trailer_value_get: handle=%d name=%q value=%q", handle, string(nameBuf), values[0])
	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))

	return XqdStatusOK
}

func (i *Instance) xqd_body_trailer_values_get(handle int32, name_addr int32, name_size int32, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
	var body = i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Read the trailer name
	var buf = make([]byte, name_size)
	var _, err = i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	// Get all values for this trailer name
	var values = body.trailers.Values(string(buf))

	i.abilog.Printf("body_trailer_values_get: handle=%d name=%q cursor=%d", handle, string(buf), cursor)

	return xqd_multivalue(i.memory, values, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

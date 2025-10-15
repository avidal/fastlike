package fastlike

import (
	"bytes"
	"io"
)

func (i *Instance) xqd_body_new(handle_out int32) int32 {
	bhid, _ := i.bodies.NewBuffer()
	i.abilog.Printf("body_new: handle=%d", bhid)
	i.memory.PutUint32(uint32(bhid), int64(handle_out))
	return XqdStatusOK
}

func (i *Instance) xqd_body_write(handle int32, addr int32, size int32, body_end int32, nwritten_out int32) int32 {
	i.abilog.Printf("body_write: handle=%d size=%d, body_end=%d", handle, size, body_end)

	body := i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Check if this is a streaming body
	if body.IsStreaming() {
		// Use streaming write
		data := make([]byte, size)
		_, err := i.memory.ReadAt(data, int64(addr))
		if err != nil {
			return XqdError
		}

		n, err := body.WriteStreaming(data)
		if err != nil {
			i.abilog.Printf("body_write: streaming write error: %v", err)
			return XqdError
		}

		i.memory.PutUint32(uint32(n), int64(nwritten_out))

		// Handle end flag - close the streaming body when done
		if body_end == BodyWriteEndBack || body_end == BodyWriteEndFront {
			body.CloseStreaming()
		}

		return XqdStatusOK
	}

	// Original non-streaming logic continues...
	// Read the data from guest memory
	data := make([]byte, size)
	_, err := i.memory.ReadAt(data, int64(addr))
	if err != nil {
		return XqdError
	}

	if body_end == 1 {
		// Write to front (prepend)
		// Replace the reader with a MultiReader that first reads the new data, then the existing reader
		body.reader = io.MultiReader(bytes.NewReader(data), body.reader)
		i.memory.PutUint32(uint32(size), int64(nwritten_out))
		return XqdStatusOK
	}

	// Write to back (append) - default behavior
	// Copy size bytes into the body handle's writer
	nwritten, err := io.CopyN(body, bytes.NewReader(data), int64(size))
	if err != nil {
		return XqdError
	}

	// Write out how many bytes we copied
	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))

	return XqdStatusOK
}

func (i *Instance) xqd_body_read(handle int32, addr int32, maxlen int32, nread_out int32) int32 {
	body := i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("body_read: handle=%d addr=%d maxlen=%d", handle, addr, maxlen)

	buf := bytes.NewBuffer(make([]byte, 0, maxlen))
	ncopied, err := io.Copy(buf, io.LimitReader(body, int64(maxlen)))
	if err != nil {
		i.abilog.Printf("body_read: error copying got=%s", err.Error())
		return XqdError
	}

	nwritten, err2 := i.memory.WriteAt(buf.Bytes(), int64(addr))
	if err2 != nil {
		i.abilog.Printf("body_read: error writing got=%s", err2.Error())
		return XqdError
	}

	if ncopied != int64(nwritten) {
		i.abilog.Printf("body_read: error copying copied=%d wrote=%d", ncopied, nwritten)
		return XqdError
	}

	i.abilog.Printf("body_read: handle=%d maxlen=%d copied=%d", handle, maxlen, ncopied)

	// Write out how many bytes we copied
	i.memory.PutUint32(uint32(nwritten), int64(nread_out))

	return XqdStatusOK
}

func (i *Instance) xqd_body_append(dst_handle int32, src_handle int32) int32 {
	i.abilog.Printf("body_append: dst=%d src=%d", dst_handle, src_handle)

	dst := i.bodies.Get(int(dst_handle))
	if dst == nil {
		return XqdErrInvalidHandle
	}

	src := i.bodies.Get(int(src_handle))
	if src == nil {
		return XqdErrInvalidHandle
	}

	// replace the destination reader with a multireader that reads first from the original reader
	// and then from the source
	dst.reader = io.MultiReader(dst.reader, src)

	return XqdStatusOK
}

func (i *Instance) xqd_body_close(handle int32) int32 {
	body := i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	if err := body.Close(); err != nil {
		return XqdErrInvalidHandle
	}

	return XqdStatusOK
}

func (i *Instance) xqd_body_trailer_append(handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
	body := i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Read the trailer name
	name := make([]byte, name_size)
	_, err := i.memory.ReadAt(name, int64(name_addr))
	if err != nil {
		return XqdError
	}

	// Read the trailer value
	value := make([]byte, value_size)
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

	body := i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	names := []string{}
	for n := range body.trailers {
		names = append(names, n)
	}

	return xqd_multivalue(i.memory, names, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

func (i *Instance) xqd_body_trailer_value_get(handle int32, name_addr int32, name_size int32, value_addr int32, value_maxlen int32, nwritten_out int32) int32 {
	body := i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Read the trailer name
	nameBuf := make([]byte, name_size)
	_, err := i.memory.ReadAt(nameBuf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	// Get the first value for this trailer name
	values := body.trailers.Values(string(nameBuf))
	if len(values) == 0 {
		i.memory.PutUint32(0, int64(nwritten_out))
		return XqdStatusOK
	}

	// Get the first value
	value := []byte(values[0])

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
	body := i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Read the trailer name
	buf := make([]byte, name_size)
	_, err := i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	// Get all values for this trailer name
	values := body.trailers.Values(string(buf))

	i.abilog.Printf("body_trailer_values_get: handle=%d name=%q cursor=%d", handle, string(buf), cursor)

	return xqd_multivalue(i.memory, values, addr, maxlen, cursor, ending_cursor_out, nwritten_out)
}

func (i *Instance) xqd_body_abandon(handle int32) int32 {
	i.abilog.Printf("body_abandon: handle=%d", handle)

	body := i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Close and drop the body without finishing it properly
	// This is used when streaming is incomplete or failed
	if err := body.Close(); err != nil {
		return XqdError
	}

	return XqdStatusOK
}

func (i *Instance) xqd_body_known_length(handle int32, length_out int32) int32 {
	body := i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Get the known length of the body
	// If the length is not known (e.g., streaming body), return XqdErrNone
	length := body.Size()
	if length < 0 {
		i.abilog.Printf("body_known_length: handle=%d length=unknown", handle)
		return XqdErrNone
	}

	i.abilog.Printf("body_known_length: handle=%d length=%d", handle, length)
	i.memory.PutUint64(uint64(length), int64(length_out))

	return XqdStatusOK
}

package fastlike

import (
	"bytes"
	"io"
)

// xqd_body_new creates a new empty body handle.
// The newly created body handle is written to handle_out in guest memory.
// Returns XqdStatusOK on success.
func (i *Instance) xqd_body_new(handle_out int32) int32 {
	bhid, _ := i.bodies.NewBuffer()
	i.abilog.Printf("body_new: handle=%d", bhid)
	i.memory.PutUint32(uint32(bhid), int64(handle_out))
	return XqdStatusOK
}

// xqd_body_write writes data to a body handle from guest memory.
// Reads size bytes from addr and writes them to the body identified by handle.
// The body_end parameter controls write position: BodyWriteEndBack (0) appends to back,
// BodyWriteEndFront (1) prepends to front. For streaming bodies, body_end also signals
// when to close the stream. The number of bytes written is stored in nwritten_out.
// Returns XqdStatusOK on success, XqdErrInvalidHandle if the body handle is invalid,
// or XqdError if memory operations fail.
func (i *Instance) xqd_body_write(handle int32, addr int32, size int32, body_end int32, nwritten_out int32) int32 {
	i.abilog.Printf("body_write: handle=%d size=%d, body_end=%d", handle, size, body_end)

	body := i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Check if this is a streaming body (used with async streaming requests)
	if body.IsStreaming() {
		if body_end == BodyWriteEndFront {
			i.abilog.Printf("body_write: front-write not supported on streaming bodies")
			return XqdErrUnsupported
		}

		// Read data from guest memory
		data := make([]byte, size)
		_, err := i.memory.ReadAt(data, int64(addr))
		if err != nil {
			return XqdError
		}

		// Write to the streaming channel
		n, err := body.WriteStreaming(data)
		if err != nil {
			i.abilog.Printf("body_write: streaming write error: %v", err)
			return XqdError
		}

		i.memory.PutUint32(uint32(n), int64(nwritten_out))

		// Note: The body_end parameter indicates write POSITION (back vs front),
		// NOT whether to close the stream. The stream is closed via body_close.

		return XqdStatusOK
	}

	// Non-streaming body logic
	// Read the data from guest memory
	data := make([]byte, size)
	_, err := i.memory.ReadAt(data, int64(addr))
	if err != nil {
		return XqdError
	}

	const (
		writeEndBack  = 0 // Append to back (default)
		writeEndFront = 1 // Prepend to front
	)

	if body_end == writeEndFront {
		// Prepend: Create a MultiReader that reads new data first, then existing content
		body.reader = io.MultiReader(bytes.NewReader(data), body.reader)
		i.memory.PutUint32(uint32(size), int64(nwritten_out))
		return XqdStatusOK
	}

	// Append to back (default behavior)
	// Copy the data into the body handle's internal buffer
	nwritten, err := io.CopyN(body, bytes.NewReader(data), int64(size))
	if err != nil {
		return XqdError
	}

	// Write the number of bytes copied to guest memory
	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))

	return XqdStatusOK
}

// xqd_body_read reads up to maxlen bytes from a body handle into guest memory.
// Copies data from the body identified by handle into guest memory starting at addr.
// The actual number of bytes read is written to nread_out. This is a streaming operation
// that consumes bytes from the body's reader (subsequent reads continue from where the last read left off).
// Returns XqdStatusOK on success (even if 0 bytes read/EOF), XqdErrInvalidHandle if the body handle is invalid,
// or XqdError if memory or I/O operations fail.
func (i *Instance) xqd_body_read(handle int32, addr int32, maxlen int32, nread_out int32) int32 {
	body := i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	i.abilog.Printf("body_read: handle=%d addr=%d maxlen=%d", handle, addr, maxlen)

	// Read up to maxlen bytes from the body
	buf := bytes.NewBuffer(make([]byte, 0, maxlen))
	ncopied, err := io.Copy(buf, io.LimitReader(body, int64(maxlen)))
	if err != nil {
		i.abilog.Printf("body_read: error copying got=%s", err.Error())
		return XqdError
	}

	// Write the read data to guest memory
	nwritten, err := i.memory.WriteAt(buf.Bytes(), int64(addr))
	if err != nil {
		i.abilog.Printf("body_read: error writing to guest memory got=%s", err.Error())
		return XqdError
	}

	// Sanity check: bytes copied from body should match bytes written to memory
	if ncopied != int64(nwritten) {
		i.abilog.Printf("body_read: mismatch: copied=%d wrote=%d", ncopied, nwritten)
		return XqdError
	}

	i.abilog.Printf("body_read: handle=%d maxlen=%d read=%d", handle, maxlen, ncopied)

	// Write the number of bytes read to guest memory
	i.memory.PutUint32(uint32(nwritten), int64(nread_out))

	return XqdStatusOK
}

// xqd_body_append appends the contents of one body to another.
// Combines src_handle body with dst_handle by creating a MultiReader that reads
// from dst first, then src. This is a zero-copy operation that chains the readers.
// Both handles must be valid body handles.
// Returns XqdStatusOK on success or XqdErrInvalidHandle if either handle is invalid.
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

	// Chain the readers: dst content followed by src content
	// This is efficient as it doesn't copy data, just creates a composite reader
	dst.reader = io.MultiReader(dst.reader, src)

	return XqdStatusOK
}

// xqd_body_close closes a body handle and releases its resources.
// This properly finalizes the body, completing any pending operations.
// Returns XqdStatusOK on success or XqdErrInvalidHandle if the handle is invalid
// or the close operation fails.
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

// xqd_body_trailer_append adds a trailer header to a body.
// Reads the trailer name from guest memory at name_addr (name_size bytes) and the value
// from value_addr (value_size bytes), then appends it to the body's trailer map.
// If a trailer with the same name already exists, the new value is appended to the list.
// Trailers are HTTP headers sent after the body content (used with chunked transfer encoding).
// Returns XqdStatusOK on success, XqdErrInvalidHandle if the body handle is invalid,
// or XqdError if memory operations fail.
func (i *Instance) xqd_body_trailer_append(handle int32, name_addr int32, name_size int32, value_addr int32, value_size int32) int32 {
	body := i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Read the trailer name from guest memory
	name := make([]byte, name_size)
	_, err := i.memory.ReadAt(name, int64(name_addr))
	if err != nil {
		return XqdError
	}

	// Read the trailer value from guest memory
	value := make([]byte, value_size)
	_, err = i.memory.ReadAt(value, int64(value_addr))
	if err != nil {
		return XqdError
	}

	// Initialize the trailer map if this is the first trailer
	if body.trailers == nil {
		body.trailers = make(map[string][]string)
	}

	// Append the trailer value (supports multiple values per trailer name)
	body.trailers.Add(string(name), string(value))

	i.abilog.Printf("body_trailer_append: handle=%d name=%q value=%q", handle, string(name), string(value))

	return XqdStatusOK
}

// xqd_body_trailer_names_get retrieves the list of trailer header names from a body.
// Returns trailer names as a multi-value list written to guest memory at addr.
// Uses cursor-based pagination to handle results that exceed maxlen buffer size.
// The ending_cursor_out indicates the position for the next call, and nwritten_out
// contains the number of bytes written. Call repeatedly with the ending cursor until
// it equals -1 to retrieve all names.
// Returns XqdStatusOK on success or XqdErrInvalidHandle if the body handle is invalid.
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

// xqd_body_trailer_value_get retrieves the first value of a trailer header.
// Reads the trailer name from guest memory at name_addr (name_size bytes) and writes
// the first value for that trailer to value_addr (up to value_maxlen bytes), null-terminated.
// If the trailer doesn't exist or has no values, nwritten_out is set to 0.
// Returns XqdStatusOK on success, XqdErrInvalidHandle if the body handle is invalid,
// XqdErrBufferLength if the buffer is too small, or XqdError if memory operations fail.
func (i *Instance) xqd_body_trailer_value_get(handle int32, name_addr int32, name_size int32, value_addr int32, value_maxlen int32, nwritten_out int32) int32 {
	body := i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Read the trailer name from guest memory
	nameBuf := make([]byte, name_size)
	_, err := i.memory.ReadAt(nameBuf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	// Get all values for this trailer name
	values := body.trailers.Values(string(nameBuf))
	if len(values) == 0 {
		// No values found - return 0 bytes written
		i.memory.PutUint32(0, int64(nwritten_out))
		return XqdStatusOK
	}

	// Get the first value and prepare to null-terminate it
	value := []byte(values[0])

	// Check if buffer is large enough (including null terminator)
	if len(value)+1 > int(value_maxlen) {
		return XqdErrBufferLength
	}

	// Append null terminator
	value = append(value, '\x00')

	// Write the null-terminated value to guest memory
	nwritten, err := i.memory.WriteAt(value, int64(value_addr))
	if err != nil {
		return XqdError
	}

	i.abilog.Printf("body_trailer_value_get: handle=%d name=%q value=%q", handle, string(nameBuf), values[0])
	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))

	return XqdStatusOK
}

// xqd_body_trailer_values_get retrieves all values for a specific trailer header.
// Reads the trailer name from guest memory at name_addr (name_size bytes) and writes
// all values for that trailer to addr as a multi-value list. Uses cursor-based pagination
// to handle results that exceed maxlen buffer size. The ending_cursor_out indicates the
// position for the next call, and nwritten_out contains the number of bytes written.
// Returns XqdStatusOK on success, XqdErrInvalidHandle if the body handle is invalid,
// or XqdError if memory operations fail.
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

// xqd_body_abandon abandons a body without properly finishing it.
// Closes and drops the body immediately, discarding any incomplete streaming operations.
// This is used when a body is no longer needed or when an error occurs during streaming.
// Unlike xqd_body_close which properly finalizes the body, this aborts the operation.
// Returns XqdStatusOK on success, XqdErrInvalidHandle if the body handle is invalid,
// or XqdError if the close operation fails.
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

// xqd_body_known_length retrieves the known length of a body if available.
// Writes the body's size in bytes to length_out in guest memory as a uint64.
// If the body's length is not known in advance (e.g., for streaming bodies with unknown size),
// returns XqdErrNone without writing to length_out.
// This is useful for setting Content-Length headers or preallocating buffers.
// Returns XqdStatusOK if the length is known, XqdErrNone if unknown,
// or XqdErrInvalidHandle if the body handle is invalid.
func (i *Instance) xqd_body_known_length(handle int32, length_out int32) int32 {
	body := i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Get the body's size (-1 indicates unknown length)
	length := body.Size()
	if length < 0 {
		i.abilog.Printf("body_known_length: handle=%d length=unknown", handle)
		return XqdErrNone
	}

	// Write the known length as uint64 to guest memory
	i.abilog.Printf("body_known_length: handle=%d length=%d", handle, length)
	i.memory.PutUint64(uint64(length), int64(length_out))

	return XqdStatusOK
}

package fastlike

// xqd_multivalue implements a cursor-based mechanism for iterating over multiple values.
// This is not an actual ABI method, but a helper used by the guest to retrieve multiple values
// (such as multi-value headers) across multiple hostcalls using a cursor.
// Writes the value at the cursor position to addr, the number of bytes written to nwritten_out,
// and the next cursor position to ending_cursor_out (-1 if no more values).
// Returns XqdErrBufferLength if the buffer is too small for the current value.
func xqd_multivalue(memory *Memory, data []string, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
	// If there's no data, return early
	if len(data) == 0 {
		memory.PutUint32(uint32(0), int64(nwritten_out))

		// Set the cursor to -1 to stop asking
		memory.PutInt64(-1, int64(ending_cursor_out))
		return XqdStatusOK
	}

	// If the cursor points outside our slice, return early
	if int(cursor) < 0 || int(cursor) >= len(data) {
		memory.PutUint32(uint32(0), int64(nwritten_out))

		// Set the cursor to -1 to stop asking
		memory.PutInt64(-1, int64(ending_cursor_out))
		return XqdStatusOK
	}

	if len([]byte(data[cursor]))+1 > int(maxlen) {
		return XqdErrBufferLength
	}
	v := []byte(data[cursor])
	v = append(v, '\x00')

	nwritten, err := memory.WriteAt(v, int64(addr))
	check(err)

	memory.PutUint32(uint32(nwritten), int64(nwritten_out))

	// If there's more entries, set the cursor to +1
	var ec int
	if int(cursor) < len(data)-1 {
		ec = int(cursor) + 1
	} else {
		ec = -1
	}

	memory.PutInt64(int64(ec), int64(ending_cursor_out))

	return XqdStatusOK
}

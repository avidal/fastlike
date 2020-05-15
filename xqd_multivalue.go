package fastlike

// xqd_multivalue is not an actual ABI method, but it's an implementation of a mechanism used by
// the guest to make multiple hostcalls via a cursor
// For usage, see the abi methods for headers
func xqd_multivalue(memory *Memory, data []string, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) XqdStatus {
	// If there's no data, return early
	if len(data) == 0 {
		memory.PutUint32(uint32(0), int64(nwritten_out))

		// Set the cursor to -1 to stop asking
		memory.PutInt64(-1, int64(ending_cursor_out))
		return XqdStatusOK
	}

	// If the cursor points past our slice, return early
	if int(cursor) >= len(data) {
		memory.PutUint32(uint32(0), int64(nwritten_out))

		// Set the cursor to -1 to stop asking
		memory.PutInt64(-1, int64(ending_cursor_out))
		return XqdStatusOK
	}

	var v = []byte(data[cursor])
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

package fastlike

// Multi-Value XQD Helper
//
// The XQD ABI uses a cursor-based iteration protocol for retrieving multiple values
// (such as multi-value HTTP headers). The guest makes repeated calls, passing the
// cursor from the previous call, until the cursor becomes -1 (indicating no more values).

const (
	// cursorEnd signals there are no more values to retrieve
	cursorEnd = -1
)

// xqd_multivalue implements a cursor-based mechanism for iterating over multiple values.
// This is not a direct ABI method, but a shared helper used by various XQD functions
// that return multiple values (e.g., xqd_req_header_values_get).
//
// Protocol:
//  1. Guest provides a cursor (starting at 0)
//  2. Host writes the value at cursor position to guest memory
//  3. Host writes the next cursor position to ending_cursor_out:
//     - If more values exist: next cursor = current cursor + 1
//     - If no more values: next cursor = -1
//  4. Guest repeats with next cursor until receiving -1
//
// Values are null-terminated strings (C-style) for compatibility with guest code.
//
// Parameters:
//   - memory: the guest's linear memory
//   - data: slice of values to iterate over
//   - addr: guest memory address to write the current value
//   - maxlen: maximum size of the guest's buffer
//   - cursor: current position in the data slice (0-indexed)
//   - ending_cursor_out: guest memory address to write the next cursor (-1 if done)
//   - nwritten_out: guest memory address to write bytes written
//
// Return values:
//   - XqdErrBufferLength: guest's buffer is too small for current value
//   - XqdStatusOK: value written successfully (or no more values)
func xqd_multivalue(memory *Memory, data []string, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
	// Helper to signal end of iteration
	signalEndOfIteration := func() {
		memory.PutUint32(0, int64(nwritten_out))
		memory.PutInt64(cursorEnd, int64(ending_cursor_out))
	}

	// No data to iterate over
	if len(data) == 0 {
		signalEndOfIteration()
		return XqdStatusOK
	}

	// Cursor out of bounds (invalid or reached end)
	if int(cursor) < 0 || int(cursor) >= len(data) {
		signalEndOfIteration()
		return XqdStatusOK
	}

	// Get the current value and add null terminator (C-style string)
	currentValue := []byte(data[cursor])
	nullTerminatedValue := append(currentValue, '\x00')

	// Check if guest's buffer can hold the null-terminated value
	if len(nullTerminatedValue) > int(maxlen) {
		return XqdErrBufferLength
	}

	// Write the null-terminated value to guest memory
	bytesWritten, err := memory.WriteAt(nullTerminatedValue, int64(addr))
	check(err)

	memory.PutUint32(uint32(bytesWritten), int64(nwritten_out))

	// Calculate the next cursor position
	var nextCursor int
	if int(cursor) < len(data)-1 {
		// More values remain - advance to next position
		nextCursor = int(cursor) + 1
	} else {
		// This was the last value - signal end of iteration
		nextCursor = cursorEnd
	}

	memory.PutInt64(int64(nextCursor), int64(ending_cursor_out))

	return XqdStatusOK
}

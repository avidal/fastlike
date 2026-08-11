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
//  2. Host writes as many values as fit, starting at the cursor position
//  3. Host writes the next unwritten position to ending_cursor_out, or -1 when done
//  4. Guest repeats with the ending cursor until receiving -1
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
//   - XqdErrBufferLength: guest's buffer cannot hold the first remaining value;
//     nwritten_out receives the required size
//   - XqdStatusOK: value written successfully (or no more values)
func xqd_multivalue(memory *Memory, data []string, addr int32, maxlen int32, cursor int32, ending_cursor_out int32, nwritten_out int32) int32 {
	if !memory.validRange(int64(nwritten_out), 4) ||
		ending_cursor_out != 0 && !memory.validRange(int64(ending_cursor_out), 8) {
		return XqdError
	}

	writeEndingCursor := func(cursor int64) {
		// The legacy ABI treats a null ending-cursor pointer as an explicit
		// request to omit that output.
		if ending_cursor_out != 0 {
			memory.PutUint64(uint64(cursor), int64(ending_cursor_out))
		}
	}
	if maxlen < 0 || !memory.validRange(int64(addr), uint64(maxlen)) {
		writeEndingCursor(cursorEnd)
		return XqdError
	}

	// Helper to signal end of iteration
	signalEndOfIteration := func() {
		memory.PutUint32(0, int64(nwritten_out))
		writeEndingCursor(cursorEnd)
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

	written := 0
	nextCursor := int(cursor)
	for nextCursor < len(data) {
		value := []byte(data[nextCursor])
		required := len(value) + 1
		if required > int(maxlen)-written {
			if written == 0 {
				memory.PutUint32(uint32(required), int64(nwritten_out))
				writeEndingCursor(cursorEnd)
				return XqdErrBufferLength
			}
			break
		}

		n, err := memory.WriteAt(value, int64(addr)+int64(written))
		if err != nil || n != len(value) {
			return XqdError
		}
		memory.PutUint8(0, int64(addr)+int64(written+len(value)))
		written += required
		nextCursor++
	}

	if nextCursor == len(data) {
		writeEndingCursor(cursorEnd)
	} else {
		writeEndingCursor(int64(nextCursor))
	}
	memory.PutUint32(uint32(written), int64(nwritten_out))

	return XqdStatusOK
}

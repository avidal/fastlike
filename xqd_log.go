package fastlike

import (
	"bytes"
	"fmt"
	"io"
)

// xqd_log_endpoint_get retrieves a handle to a named log endpoint.
// Reads the log endpoint name from guest memory and writes the corresponding handle to addr.
// Returns XqdErrInvalidArgument if the logger name is not found or is reserved.
func (i *Instance) xqd_log_endpoint_get(name_addr int32, name_size int32, addr int32) int32 {
	buf := make([]byte, name_size)
	_, err := i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	name := string(buf)

	i.abilog.Printf("log_endpoint_get: name=%s\n", name)

	// Get the logger handle
	handle, ok := i.getLoggerHandle(name)
	if !ok {
		i.abilog.Printf("log_endpoint_get: logger '%s' not found or is reserved", name)
		return XqdErrInvalidArgument
	}

	// Write the handle to the output pointer
	i.memory.PutUint32(uint32(handle), int64(addr))

	return XqdStatusOK
}

// xqd_log_write writes data to a log endpoint.
// Copies size bytes from guest memory starting at addr to the logger identified by handle.
// Writes the number of bytes successfully written to nwritten_out.
// Returns XqdErrInvalidHandle if the handle does not refer to a valid logger.
func (i *Instance) xqd_log_write(handle int32, addr int32, size int32, nwritten_out int32) int32 {
	i.abilog.Printf("log_write: handle=%d size=%d", handle, size)

	logger := i.getLogger(int(handle))
	if logger == nil {
		return XqdErrInvalidHandle
	}

	// Copy size bytes starting at addr into the logger
	nwritten, err := io.CopyN(logger, bytes.NewReader(i.memory.Data()[addr:]), int64(size))
	if err != nil {
		fmt.Printf("got error writing to logger, err=%q\n", err)
		// TODO: If err == EOF then there's a specific error code we can return (it means they
		// didn't have `size` bytes in memory)
		return XqdError
	}

	// Write out how many bytes we copied
	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))

	return XqdStatusOK
}

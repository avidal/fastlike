package fastlike

import (
	"bytes"
	"fmt"
	"io"
)

func (i *Instance) xqd_log_endpoint_get(name_addr int32, name_size int32, addr int32) int32 {
	var buf = make([]byte, name_size)
	var _, err = i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	var name = string(buf)

	i.abilog.Printf("log_endpoint_get: name=%s\n", name)

	// Write an int32 "handle" to the configured log endpoint to `addr`
	handle := i.getLoggerHandle(name)

	// TODO: Should there be a way to disable the default logger to exercise errors when fetching
	// loggers in the guest?

	i.memory.PutUint32(uint32(handle), int64(addr))

	return XqdStatusOK
}

func (i *Instance) xqd_log_write(handle int32, addr int32, size int32, nwritten_out int32) int32 {
	i.abilog.Printf("log_write: handle=%d size=%d", handle, size)

	var logger = i.getLogger(int(handle))
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

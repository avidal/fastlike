package fastlike

import (
	"bytes"
	"fmt"
	"io"
)

func (i *Instance) xqd_body_new(handle_out int32) XqdStatus {
	fmt.Printf("xqd_body_new, bh=%d\n", handle_out)
	var bhid, _ = i.bodies.NewBuffer()
	i.memory.PutUint32(uint32(bhid), int64(handle_out))
	return XqdStatusOK
}

func (i *Instance) xqd_body_write(handle int32, addr int32, size int32, body_end int32, nwritten_out int32) XqdStatus {
	// TODO: Figure out what we're supposed to do with `body_end` which can be 0 (back) or
	// 1 (front)
	fmt.Printf("xqd_body_write, bh=%d, addr=%d, size=%d\n", handle, addr, size)

	// write maxlen bytes starting at addr to the body with handle bh
	var body = i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Copy size bytes starting at addr into the body handle
	nwritten, err := io.CopyN(body, bytes.NewReader(i.memory.Data()[addr:]), int64(size))
	if err != nil {
		return XqdError
	}

	// Write out how many bytes we copied
	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))

	return XqdStatusOK
}

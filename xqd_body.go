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

func (i *Instance) xqd_body_read(handle int32, addr int32, maxlen int32, nread_out int32) XqdStatus {
	fmt.Printf("xqd_body_read, bh=%d, addr=%d, size=%d\n", handle, addr, maxlen)

	var body = i.bodies.Get(int(handle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	var buf = bytes.NewBuffer(make([]byte, 0, maxlen))
	var nread, err = io.Copy(buf, io.LimitReader(body, int64(maxlen)))
	if err != nil {
		return XqdError
	}

	var nread2, err2 = i.memory.WriteAt(buf.Bytes(), int64(addr))
	if err2 != nil {
		return XqdError
	}

	if nread != int64(nread2) {
		return XqdError
	}

	// Write out how many bytes we copied
	i.memory.PutUint32(uint32(nread), int64(nread_out))

	return XqdStatusOK
}

func (i *Instance) xqd_body_append(dst_handle int32, src_handle int32) XqdStatus {
	fmt.Printf("xqd_body_append, dst=%d, src=%d\n", dst_handle, src_handle)

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

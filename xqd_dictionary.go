package fastlike

// xqd_dictionary_open opens a named dictionary and returns a handle.
// Reads the dictionary name from guest memory and writes the corresponding handle to addr.
// The dictionary must be configured via WithDictionary option.
func (i *Instance) xqd_dictionary_open(name_addr int32, name_size int32, addr int32) int32 {
	buf := make([]byte, name_size)
	_, err := i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	name := string(buf)

	i.abilog.Printf("dictionary_open: name=%s\n", name)

	// Write an int32 "handle" to the configured dictionary to `addr`
	handle := i.getDictionaryHandle(name)

	i.memory.PutUint32(uint32(handle), int64(addr))

	return XqdStatusOK
}

// xqd_dictionary_get retrieves a value from a dictionary by key.
// Reads the key from guest memory and writes the corresponding value to addr.
// Writes the number of bytes written to nwritten_out.
// Returns XqdErrInvalidHandle if the handle is invalid, XqdErrBufferLength if the buffer is too small,
// or XqdErrNone with nwritten_out=0 if the key does not exist.
func (i *Instance) xqd_dictionary_get(handle int32, key_addr int32, key_size int32, addr int32, size int32, nwritten_out int32) int32 {
	lookup := i.getDictionary(int(handle))
	if lookup == nil {
		return XqdErrInvalidHandle
	}

	buf := make([]byte, key_size)
	_, err := i.memory.ReadAt(buf, int64(key_addr))
	if err != nil {
		return XqdError
	}

	key := string(buf)

	i.abilog.Printf("dictionary_get: handle=%d key=%s", handle, key)

	value := lookup(key)

	// If value is empty, the key doesn't exist
	if value == "" {
		i.memory.PutUint32(0, int64(nwritten_out))
		return XqdErrNone
	}

	// Check if buffer is large enough
	if len(value) > int(size) {
		// Buffer too small - write the required size
		i.memory.PutUint32(uint32(len(value)), int64(nwritten_out))
		return XqdErrBufferLength
	}

	nwritten, err := i.memory.WriteAt([]byte(value), int64(addr))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	return XqdStatusOK
}

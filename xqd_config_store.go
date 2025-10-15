package fastlike

// xqd_config_store_open opens a named config store and returns a handle.
// Reads the config store name from guest memory and writes the corresponding handle to addr.
// The config store must be configured via WithConfigStore option.
func (i *Instance) xqd_config_store_open(name_addr int32, name_size int32, addr int32) int32 {
	buf := make([]byte, name_size)
	_, err := i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	name := string(buf)

	i.abilog.Printf("config_store_open: name=%s\n", name)

	// Write an int32 "handle" to the configured config store to `addr`
	handle := i.getConfigStoreHandle(name)

	i.memory.PutUint32(uint32(handle), int64(addr))

	return XqdStatusOK
}

// xqd_config_store_get retrieves a value from a config store by key.
// Reads the key from guest memory and writes the corresponding value to addr.
// Writes the number of bytes written to nwritten_out.
// Returns XqdErrInvalidHandle if the handle is invalid, XqdErrBufferLength if the buffer is too small,
// or XqdErrNone with nwritten_out=0 if the key does not exist.
func (i *Instance) xqd_config_store_get(handle int32, key_addr int32, key_size int32, addr int32, size int32, nwritten_out int32) int32 {
	lookup := i.getConfigStore(int(handle))
	if lookup == nil {
		return XqdErrInvalidHandle
	}

	buf := make([]byte, key_size)
	_, err := i.memory.ReadAt(buf, int64(key_addr))
	if err != nil {
		return XqdError
	}

	key := string(buf)

	i.abilog.Printf("config_store_get: handle=%d key=%s", handle, key)

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

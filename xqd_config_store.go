package fastlike

// xqd_config_store_open opens a named config store and returns a handle.
// The guest provides the config store name in its linear memory at name_addr,
// and this function writes the allocated handle back to the guest's memory at addr.
// The config store must be pre-configured via WithConfigStore option.
func (i *Instance) xqd_config_store_open(name_addr int32, name_size int32, addr int32) int32 {
	nameBytes := make([]byte, name_size)
	_, err := i.memory.ReadAt(nameBytes, int64(name_addr))
	if err != nil {
		return XqdError
	}

	configStoreName := string(nameBytes)

	i.abilog.Printf("config_store_open: name=%s\n", configStoreName)

	// Allocate a handle for this config store and write it to guest memory
	handle := i.getConfigStoreHandle(configStoreName)
	i.memory.PutUint32(uint32(handle), int64(addr))

	return XqdStatusOK
}

// xqd_config_store_get retrieves a value from a config store by key.
// The guest provides the key in its linear memory at key_addr.
// This function writes the value back to the guest buffer at addr (max size bytes),
// and writes the actual bytes written to nwritten_out.
//
// Return values:
//   - XqdErrInvalidHandle: the config store handle doesn't exist
//   - XqdErrBufferLength: the guest's buffer is too small (nwritten_out contains required size)
//   - XqdErrNone: the key doesn't exist (nwritten_out=0)
//   - XqdStatusOK: success (nwritten_out contains bytes written)
func (i *Instance) xqd_config_store_get(handle int32, key_addr int32, key_size int32, addr int32, size int32, nwritten_out int32) int32 {
	lookupFunc := i.getConfigStore(int(handle))
	if lookupFunc == nil {
		return XqdErrInvalidHandle
	}

	keyBytes := make([]byte, key_size)
	_, err := i.memory.ReadAt(keyBytes, int64(key_addr))
	if err != nil {
		return XqdError
	}

	key := string(keyBytes)

	i.abilog.Printf("config_store_get: handle=%d key=%s", handle, key)

	value := lookupFunc(key)

	// Empty value indicates the key doesn't exist
	if value == "" {
		i.memory.PutUint32(0, int64(nwritten_out))
		return XqdErrNone
	}

	// Check if the guest's buffer can hold the value
	if len(value) > int(size) {
		// Buffer too small - tell the guest how much space is needed
		i.memory.PutUint32(uint32(len(value)), int64(nwritten_out))
		return XqdErrBufferLength
	}

	// Write value to guest memory
	bytesWritten, err := i.memory.WriteAt([]byte(value), int64(addr))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(bytesWritten), int64(nwritten_out))
	return XqdStatusOK
}

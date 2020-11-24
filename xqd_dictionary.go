package fastlike

func (i *Instance) xqd_dictionary_open(name_addr int32, name_size int32, addr int32) int32 {
	var buf = make([]byte, name_size)
	var _, err = i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	var name = string(buf)

	i.abilog.Printf("dictionary_open: name=%s\n", name)

	// Write an int32 "handle" to the configured dictionary to `addr`
	handle := i.getDictionaryHandle(name)

	i.memory.PutUint32(uint32(handle), int64(addr))

	return XqdStatusOK
}

func (i *Instance) xqd_dictionary_get(handle int32, key_addr int32, key_size int32, addr int32, size int32, nwritten_out int32) int32 {

	var lookup = i.getDictionary(int(handle))
	if lookup == nil {
		return XqdErrInvalidHandle
	}

	var buf = make([]byte, key_size)
	var _, err = i.memory.ReadAt(buf, int64(key_addr))
	if err != nil {
		return XqdError
	}

	var key = string(buf)

	i.abilog.Printf("dictionary_get: handle=%d key=%s", handle, key)

	var value = lookup(key)

	nwritten, err := i.memory.WriteAt([]byte(value), int64(addr))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	return XqdStatusOK
}

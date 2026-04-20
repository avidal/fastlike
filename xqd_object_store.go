package fastlike

// Legacy fastly_object_store namespace.
// These are deprecated in favor of fastly_kv_store but still used by older SDKs.

func (i *Instance) xqd_object_store_open(
	namePtr int32,
	nameLen int32,
	storeHandleOut int32,
) int32 {
	i.abilog.Println("xqd_object_store_open")

	nameBuf := make([]byte, nameLen)
	_, err := i.memory.ReadAt(nameBuf, int64(namePtr))
	if err != nil {
		return XqdError
	}

	store, exists := i.kvStoreRegistry[string(nameBuf)]
	if !exists {
		return XqdErrInvalidArgument
	}

	handleID := i.kvStores.New(store)
	i.memory.WriteUint32(storeHandleOut, uint32(handleID))

	return XqdStatusOK
}

func (i *Instance) xqd_object_store_lookup(
	storeHandle int32,
	keyPtr int32,
	keyLen int32,
	bodyHandleOut int32,
) int32 {
	i.abilog.Println("xqd_object_store_lookup")

	store := i.kvStores.Get(int(storeHandle))
	if store == nil {
		return XqdErrInvalidHandle
	}

	keyBuf := make([]byte, keyLen)
	_, err := i.memory.ReadAt(keyBuf, int64(keyPtr))
	if err != nil {
		return XqdError
	}
	key := string(keyBuf)

	if err := ValidateKey(key); err != nil {
		return XqdErrInvalidArgument
	}

	result, err := store.Store.Lookup(key)
	if err != nil {
		return XqdError
	}

	if result == nil {
		i.memory.WriteUint32(bodyHandleOut, uint32(HandleInvalid))
		return XqdStatusOK
	}

	bodyID, bodyHandle := i.bodies.NewBuffer()
	_, _ = bodyHandle.Write(result.Body)
	i.memory.WriteUint32(bodyHandleOut, uint32(bodyID))

	return XqdStatusOK
}

func (i *Instance) xqd_object_store_lookup_async(
	storeHandle int32,
	keyPtr int32,
	keyLen int32,
	pendingHandleOut int32,
) int32 {
	i.abilog.Println("xqd_object_store_lookup_async")
	return i.xqd_kv_store_lookup(storeHandle, keyPtr, keyLen, 0, 0, pendingHandleOut)
}

func (i *Instance) xqd_object_store_pending_lookup_wait(
	pendingHandle int32,
	bodyHandleOut int32,
) int32 {
	i.abilog.Println("xqd_object_store_pending_lookup_wait")

	lookup := i.kvLookups.Get(int(pendingHandle))
	if lookup == nil {
		return XqdErrInvalidHandle
	}

	result, err := lookup.Wait()
	if err != nil {
		return XqdError
	}

	if result == nil {
		i.memory.WriteUint32(bodyHandleOut, uint32(HandleInvalid))
		return XqdStatusOK
	}

	bodyID, bodyHandle := i.bodies.NewBuffer()
	_, _ = bodyHandle.Write(result.Body)
	i.memory.WriteUint32(bodyHandleOut, uint32(bodyID))

	return XqdStatusOK
}

func (i *Instance) xqd_object_store_insert(
	storeHandle int32,
	keyPtr int32,
	keyLen int32,
	bodyHandle int32,
) int32 {
	i.abilog.Println("xqd_object_store_insert")

	store := i.kvStores.Get(int(storeHandle))
	if store == nil {
		return XqdErrInvalidHandle
	}

	keyBuf := make([]byte, keyLen)
	_, err := i.memory.ReadAt(keyBuf, int64(keyPtr))
	if err != nil {
		return XqdError
	}
	key := string(keyBuf)

	if err := ValidateKey(key); err != nil {
		return XqdErrInvalidArgument
	}

	body := i.bodies.Get(int(bodyHandle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	var value []byte
	if body.buf != nil {
		value = body.buf.Bytes()
	}

	_, err = store.Store.Insert(key, value, "", nil, InsertModeOverwrite, nil)
	if err != nil {
		return XqdError
	}

	return XqdStatusOK
}

func (i *Instance) xqd_object_store_insert_async(
	storeHandle int32,
	keyPtr int32,
	keyLen int32,
	bodyHandle int32,
	pendingHandleOut int32,
) int32 {
	i.abilog.Println("xqd_object_store_insert_async")

	store := i.kvStores.Get(int(storeHandle))
	if store == nil {
		return XqdErrInvalidHandle
	}

	keyBuf := make([]byte, keyLen)
	_, err := i.memory.ReadAt(keyBuf, int64(keyPtr))
	if err != nil {
		return XqdError
	}
	key := string(keyBuf)

	if err := ValidateKey(key); err != nil {
		return XqdErrInvalidArgument
	}

	body := i.bodies.Get(int(bodyHandle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	var value []byte
	if body.buf != nil {
		value = body.buf.Bytes()
	}

	insertID, insertHandle := i.kvInserts.New()

	go func() {
		generation, err := store.Store.Insert(key, value, "", nil, InsertModeOverwrite, nil)
		insertHandle.Complete(generation, err)
	}()

	i.memory.WriteUint32(pendingHandleOut, uint32(insertID))

	return XqdStatusOK
}

func (i *Instance) xqd_object_store_pending_insert_wait(
	pendingHandle int32,
) int32 {
	i.abilog.Println("xqd_object_store_pending_insert_wait")

	insert := i.kvInserts.Get(int(pendingHandle))
	if insert == nil {
		return XqdErrInvalidHandle
	}

	_, err := insert.Wait()
	if err != nil {
		return XqdError
	}

	return XqdStatusOK
}

func (i *Instance) xqd_object_store_delete_async(
	storeHandle int32,
	keyPtr int32,
	keyLen int32,
	pendingHandleOut int32,
) int32 {
	i.abilog.Println("xqd_object_store_delete_async")
	return i.xqd_kv_store_delete(storeHandle, keyPtr, keyLen, 0, 0, pendingHandleOut)
}

func (i *Instance) xqd_object_store_pending_delete_wait(
	pendingHandle int32,
) int32 {
	i.abilog.Println("xqd_object_store_pending_delete_wait")

	del := i.kvDeletes.Get(int(pendingHandle))
	if del == nil {
		return XqdErrInvalidHandle
	}

	err := del.Wait()
	if err != nil {
		return XqdError
	}

	return XqdStatusOK
}

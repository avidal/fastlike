package fastlike

import (
	"time"
)

// xqd_kv_store_open opens a KV store by name
// Returns a KV store handle or HandleInvalid if the store doesn't exist
func (i *Instance) xqd_kv_store_open(
	namePtr int32,
	nameLen int32,
	kvStoreHandleOut int32,
) int32 {
	i.abilog.Println("xqd_kv_store_open")

	// Read store name from guest memory
	nameBuf := make([]byte, nameLen)
	_, err := i.memory.ReadAt(nameBuf, int64(namePtr))
	if err != nil {
		return XqdError
	}

	// Look up the store in the registry
	store, exists := i.kvStoreRegistry[string(nameBuf)]
	if !exists {
		// Store not found - return invalid handle
		i.memory.WriteUint32(kvStoreHandleOut, uint32(HandleInvalid))
		return XqdStatusOK
	}

	// Create a handle for the store
	handleID := i.kvStores.New(store)

	// Write the handle to guest memory
	i.memory.WriteUint32(kvStoreHandleOut, uint32(handleID))

	return XqdStatusOK
}

// xqd_kv_store_lookup initiates an async lookup operation
// Structure: kv_lookup_config (4 bytes)
//   - reserved (u32)
func (i *Instance) xqd_kv_store_lookup(
	kvStoreHandle int32,
	keyPtr int32,
	keyLen int32,
	lookupConfigMask int32,
	lookupConfigBuf int32,
	lookupHandleOut int32,
) int32 {
	i.abilog.Println("xqd_kv_store_lookup")

	// Get the store handle
	storeHandle := i.kvStores.Get(int(kvStoreHandle))
	if storeHandle == nil {
		return XqdErrInvalidHandle
	}

	// Read the key from guest memory
	keyBuf := make([]byte, keyLen)
	_, err := i.memory.ReadAt(keyBuf, int64(keyPtr))
	if err != nil {
		return XqdError
	}
	key := string(keyBuf)

	// Validate the key
	if err := ValidateKey(key); err != nil {
		return XqdErrInvalidArgument
	}

	// Note: lookup config only contains reserved fields, so we ignore it

	// Create a pending lookup handle
	lookupID, lookupHandle := i.kvLookups.New()

	// Start the lookup in a goroutine (simulating async operation)
	go func() {
		result, err := storeHandle.Store.Lookup(key)
		lookupHandle.Complete(result, err)
	}()

	// Write the lookup handle to guest memory
	i.memory.WriteUint32(lookupHandleOut, uint32(lookupID))

	return XqdStatusOK
}

// xqd_kv_store_lookup_wait waits for a lookup to complete (V1 - deprecated)
// Returns u32 generation (always 0 for V1 compatibility)
func (i *Instance) xqd_kv_store_lookup_wait(
	lookupHandle int32,
	bodyHandleOut int32,
	metadataOut int32,
	metadataMaxLen int32,
	metadataLenOut int32,
	generationOut int32, // u32
	kvErrorOut int32,
) int32 {
	i.abilog.Println("xqd_kv_store_lookup_wait")

	// Get the lookup handle
	lookup := i.kvLookups.Get(int(lookupHandle))
	if lookup == nil {
		return XqdErrInvalidHandle
	}

	// Wait for the lookup to complete
	result, err := lookup.Wait()
	if err != nil {
		// Write error code
		i.memory.WriteUint32(kvErrorOut, uint32(1)) // KV_ERROR_UNKNOWN
		return XqdError
	}

	// If key not found
	if result == nil {
		i.memory.WriteUint32(bodyHandleOut, uint32(HandleInvalid))
		i.memory.WriteUint32(metadataLenOut, 0)
		i.memory.WriteUint32(generationOut, 0)
		i.memory.WriteUint32(kvErrorOut, 0) // No error
		return XqdErrNone
	}

	// Create a body handle for the value
	bodyID, bodyHandle := i.bodies.NewBuffer()
	_, _ = bodyHandle.Write(result.Body)

	// Write body handle
	i.memory.WriteUint32(bodyHandleOut, uint32(bodyID))

	// Write metadata
	metadataBytes := []byte(result.Metadata)
	metadataLen := len(metadataBytes)
	if int32(metadataLen) > metadataMaxLen {
		metadataLen = int(metadataMaxLen)
	}
	_, _ = i.memory.WriteAt(metadataBytes[:metadataLen], int64(metadataOut))
	i.memory.WriteUint32(metadataLenOut, uint32(metadataLen))

	// Write generation (V1 uses u32, always 0 for compatibility)
	i.memory.WriteUint32(generationOut, 0)

	// Write error code (success)
	i.memory.WriteUint32(kvErrorOut, 0)

	return XqdStatusOK
}

// xqd_kv_store_lookup_wait_v2 waits for a lookup to complete (V2)
// Returns u64 generation
func (i *Instance) xqd_kv_store_lookup_wait_v2(
	lookupHandle int32,
	bodyHandleOut int32,
	metadataOut int32,
	metadataMaxLen int32,
	metadataLenOut int32,
	generationOut int32, // u64
	kvErrorOut int32,
) int32 {
	i.abilog.Println("xqd_kv_store_lookup_wait_v2")

	// Get the lookup handle
	lookup := i.kvLookups.Get(int(lookupHandle))
	if lookup == nil {
		return XqdErrInvalidHandle
	}

	// Wait for the lookup to complete
	result, err := lookup.Wait()
	if err != nil {
		// Write error code
		i.memory.WriteUint32(kvErrorOut, uint32(1)) // KV_ERROR_UNKNOWN
		return XqdError
	}

	// If key not found
	if result == nil {
		i.memory.WriteUint32(bodyHandleOut, uint32(HandleInvalid))
		i.memory.WriteUint32(metadataLenOut, 0)
		i.memory.WriteUint64(generationOut, 0)
		i.memory.WriteUint32(kvErrorOut, 0) // No error
		return XqdErrNone
	}

	// Create a body handle for the value
	bodyID, bodyHandle := i.bodies.NewBuffer()
	_, _ = bodyHandle.Write(result.Body)

	// Write body handle
	i.memory.WriteUint32(bodyHandleOut, uint32(bodyID))

	// Write metadata
	metadataBytes := []byte(result.Metadata)
	metadataLen := len(metadataBytes)
	if int32(metadataLen) > metadataMaxLen {
		metadataLen = int(metadataMaxLen)
	}
	_, _ = i.memory.WriteAt(metadataBytes[:metadataLen], int64(metadataOut))
	i.memory.WriteUint32(metadataLenOut, uint32(metadataLen))

	// Write generation (V2 uses u64)
	i.memory.WriteUint64(generationOut, result.Generation)

	// Write error code (success)
	i.memory.WriteUint32(kvErrorOut, 0)

	return XqdStatusOK
}

// xqd_kv_store_insert initiates an async insert operation
// Structure: kv_insert_config (24 bytes)
//   - mode (u32)
//   - unused (u32)
//   - metadata_ptr (u32)
//   - metadata_len (u32)
//   - time_to_live_sec (u32)
//   - if_generation_match (u64)
func (i *Instance) xqd_kv_store_insert(
	kvStoreHandle int32,
	keyPtr int32,
	keyLen int32,
	bodyHandle int32,
	insertConfigMask int32,
	insertConfigBuf int32,
	insertHandleOut int32,
) int32 {
	i.abilog.Println("xqd_kv_store_insert")

	// Get the store handle
	storeHandle := i.kvStores.Get(int(kvStoreHandle))
	if storeHandle == nil {
		return XqdErrInvalidHandle
	}

	// Read the key
	keyBuf := make([]byte, keyLen)
	_, err := i.memory.ReadAt(keyBuf, int64(keyPtr))
	if err != nil {
		return XqdError
	}
	key := string(keyBuf)

	// Validate the key
	if err := ValidateKey(key); err != nil {
		return XqdErrInvalidArgument
	}

	// Get the body handle
	body := i.bodies.Get(int(bodyHandle))
	if body == nil {
		return XqdErrInvalidHandle
	}

	// Read the body contents
	var value []byte
	if body.buf != nil {
		value = body.buf.Bytes()
	}

	// Read insert config structure
	var mode InsertMode
	var metadata string
	var ttl *time.Duration
	var ifGenerationMatch *uint64

	// Read mode (always present at offset 0)
	mode = InsertMode(i.memory.ReadUint32(insertConfigBuf))

	// unused u32 at offset 4

	// Mask bit 3: metadata (pointer + length)
	if insertConfigMask&(1<<3) != 0 {
		metadataPtr := i.memory.ReadUint32(insertConfigBuf + 8)
		metadataLen := i.memory.ReadUint32(insertConfigBuf + 12)
		if metadataLen > 0 {
			metadataBuf := make([]byte, metadataLen)
			_, err = i.memory.ReadAt(metadataBuf, int64(metadataPtr))
			if err != nil {
				return XqdError
			}
			metadata = string(metadataBuf)
		}
	}

	// Mask bit 4: time_to_live_sec (u32)
	if insertConfigMask&(1<<4) != 0 {
		ttlSec := i.memory.ReadUint32(insertConfigBuf + 16)
		duration := time.Duration(ttlSec) * time.Second
		ttl = &duration
	}

	// Mask bit 5: if_generation_match (u64)
	if insertConfigMask&(1<<5) != 0 {
		generation := i.memory.ReadUint64(insertConfigBuf + 20)
		ifGenerationMatch = &generation
	}

	// Create a pending insert handle
	insertID, insertHandle := i.kvInserts.New()

	// Start the insert in a goroutine
	go func() {
		generation, err := storeHandle.Store.Insert(key, value, metadata, ttl, mode, ifGenerationMatch)
		insertHandle.Complete(generation, err)
	}()

	// Write the insert handle to guest memory
	i.memory.WriteUint32(insertHandleOut, uint32(insertID))

	return XqdStatusOK
}

// xqd_kv_store_insert_wait waits for an insert to complete
func (i *Instance) xqd_kv_store_insert_wait(
	insertHandle int32,
	generationOut int32, // u64
) int32 {
	i.abilog.Println("xqd_kv_store_insert_wait")

	// Get the insert handle
	insert := i.kvInserts.Get(int(insertHandle))
	if insert == nil {
		return XqdErrInvalidHandle
	}

	// Wait for the insert to complete
	generation, err := insert.Wait()
	if err != nil {
		return XqdError
	}

	// Write the generation number
	i.memory.WriteUint64(generationOut, generation)

	return XqdStatusOK
}

// xqd_kv_store_delete initiates an async delete operation
// Structure: kv_delete_config (4 bytes)
//   - reserved (u32)
func (i *Instance) xqd_kv_store_delete(
	kvStoreHandle int32,
	keyPtr int32,
	keyLen int32,
	deleteConfigMask int32,
	deleteConfigBuf int32,
	deleteHandleOut int32,
) int32 {
	i.abilog.Println("xqd_kv_store_delete")

	// Get the store handle
	storeHandle := i.kvStores.Get(int(kvStoreHandle))
	if storeHandle == nil {
		return XqdErrInvalidHandle
	}

	// Read the key
	keyBuf := make([]byte, keyLen)
	_, err := i.memory.ReadAt(keyBuf, int64(keyPtr))
	if err != nil {
		return XqdError
	}
	key := string(keyBuf)

	// Validate the key
	if err := ValidateKey(key); err != nil {
		return XqdErrInvalidArgument
	}

	// Note: delete config only contains reserved fields, so we ignore it

	// Create a pending delete handle
	deleteID, deleteHandle := i.kvDeletes.New()

	// Start the delete in a goroutine
	go func() {
		err := storeHandle.Store.Delete(key)
		deleteHandle.Complete(err)
	}()

	// Write the delete handle to guest memory
	i.memory.WriteUint32(deleteHandleOut, uint32(deleteID))

	return XqdStatusOK
}

// xqd_kv_store_delete_wait waits for a delete to complete
func (i *Instance) xqd_kv_store_delete_wait(
	deleteHandle int32,
) int32 {
	i.abilog.Println("xqd_kv_store_delete_wait")

	// Get the delete handle
	del := i.kvDeletes.Get(int(deleteHandle))
	if del == nil {
		return XqdErrInvalidHandle
	}

	// Wait for the delete to complete
	err := del.Wait()
	if err != nil {
		return XqdError
	}

	return XqdStatusOK
}

// xqd_kv_store_list initiates an async list operation
func (i *Instance) xqd_kv_store_list(
	kvStoreHandle int32,
	listConfigMask uint32,
	listConfigBuf int32,
	listHandleOut int32,
) int32 {
	i.abilog.Println("xqd_kv_store_list")

	// Get the store handle
	storeHandle := i.kvStores.Get(int(kvStoreHandle))
	if storeHandle == nil {
		return XqdErrInvalidHandle
	}

	// Parse list config
	var prefix string
	var limit uint32
	var cursor *string

	offset := int32(0)

	// Mask bit 0: limit (u32)
	if listConfigMask&(1<<0) != 0 {
		limit = i.memory.ReadUint32(listConfigBuf + offset)
		offset += 4
	}

	// Mask bit 1: prefix (string)
	if listConfigMask&(1<<1) != 0 {
		prefixPtr := i.memory.ReadUint32(listConfigBuf + offset)
		prefixLen := i.memory.ReadUint32(listConfigBuf + offset + 4)
		if prefixLen > 0 {
			prefixBuf := make([]byte, prefixLen)
			_, err := i.memory.ReadAt(prefixBuf, int64(prefixPtr))
			if err != nil {
				return XqdError
			}
			prefix = string(prefixBuf)
		}
		offset += 8
	}

	// Mask bit 2: cursor (string)
	if listConfigMask&(1<<2) != 0 {
		cursorPtr := i.memory.ReadUint32(listConfigBuf + offset)
		cursorLen := i.memory.ReadUint32(listConfigBuf + offset + 4)
		if cursorLen > 0 {
			cursorBuf := make([]byte, cursorLen)
			_, err := i.memory.ReadAt(cursorBuf, int64(cursorPtr))
			if err != nil {
				return XqdError
			}
			cursorStr := string(cursorBuf)
			cursor = &cursorStr
		}
	}

	// Create a pending list handle
	listID, listHandle := i.kvLists.New()

	// Start the list operation in a goroutine
	go func() {
		result, err := storeHandle.Store.List(prefix, limit, cursor)
		listHandle.Complete(result, err)
	}()

	// Write the list handle to guest memory
	i.memory.WriteUint32(listHandleOut, uint32(listID))

	return XqdStatusOK
}

// xqd_kv_store_list_wait waits for a list operation to complete
func (i *Instance) xqd_kv_store_list_wait(
	listHandle int32,
	bodyHandleOut int32,
	metadataOut int32,
	metadataMaxLen int32,
	metadataLenOut int32,
) int32 {
	i.abilog.Println("xqd_kv_store_list_wait")

	// Get the list handle
	list := i.kvLists.Get(int(listHandle))
	if list == nil {
		return XqdErrInvalidHandle
	}

	// Wait for the list to complete
	result, err := list.Wait()
	if err != nil {
		return XqdError
	}

	// Convert result to JSON
	jsonBytes, err := result.ToJSON()
	if err != nil {
		return XqdError
	}

	// Create a body handle for the JSON result
	bodyID, bodyHandle := i.bodies.NewBuffer()
	_, _ = bodyHandle.Write(jsonBytes)

	// Write body handle
	i.memory.WriteUint32(bodyHandleOut, uint32(bodyID))

	// Metadata is always empty for list operations
	i.memory.WriteUint32(metadataLenOut, 0)

	return XqdStatusOK
}

// addKVStore registers a KV store by name in the instance's KV store registry.
// The registered store can be accessed by guest programs through xqd_kv_store_open.
func (i *Instance) addKVStore(name string, store *KVStore) {
	i.kvStoreRegistry[name] = store
}

package fastlike

// Secret Store XQD ABI Implementation
//
// Secret stores provide secure storage for credentials like API keys and tokens.
// Unlike dictionaries and config stores, secrets use a two-level handle system:
//  1. A secret store handle (obtained via xqd_secret_store_open)
//  2. Individual secret handles (obtained via xqd_secret_store_get)
//
// This design ensures plaintext secrets are only accessible through explicit
// plaintext() calls, reducing the risk of accidental secret exposure.

// xqd_secret_store_open opens a secret store by name and returns a handle to it.
// The guest provides the store name in its linear memory at name_addr,
// and this function writes the allocated handle back to the guest's memory at handle_out.
//
// Parameters:
//   - name_addr: pointer to the secret store name string in guest memory
//   - name_size: length of the secret store name
//   - handle_out: pointer where the secret store handle will be written
//
// Return values:
//   - XqdStatusOK: store exists (handle_out contains valid handle)
//   - XqdErrNone: store doesn't exist (handle_out contains HandleInvalid)
func (i *Instance) xqd_secret_store_open(name_addr int32, name_size int32, handle_out int32) int32 {
	nameBytes := make([]byte, name_size)
	_, err := i.memory.ReadAt(nameBytes, int64(name_addr))
	if err != nil {
		return XqdError
	}

	storeName := string(nameBytes)

	i.abilog.Printf("secret_store_open: name=%s", storeName)

	// Attempt to get a handle for the secret store
	handle := i.getSecretStoreHandle(storeName)

	// If the store doesn't exist, write invalid handle and return XqdErrNone
	if handle == int(HandleInvalid) {
		i.memory.PutUint32(HandleInvalid, int64(handle_out))
		return XqdErrNone
	}

	// Write the valid handle to guest memory
	i.memory.PutUint32(uint32(handle), int64(handle_out))

	return XqdStatusOK
}

// xqd_secret_store_get retrieves a secret from a secret store by key.
// This returns a secret handle (not the plaintext directly). The guest must
// call xqd_secret_store_plaintext() to retrieve the actual secret value.
//
// Parameters:
//   - store_handle: handle to the secret store (from xqd_secret_store_open)
//   - key_addr: pointer to the secret key string in guest memory
//   - key_size: length of the secret key
//   - secret_handle_out: pointer where the secret handle will be written
//
// Return values:
//   - XqdErrInvalidHandle: the store handle is invalid
//   - XqdErrNone: the secret doesn't exist (secret_handle_out contains HandleInvalid)
//   - XqdStatusOK: success (secret_handle_out contains valid secret handle)
func (i *Instance) xqd_secret_store_get(store_handle int32, key_addr int32, key_size int32, secret_handle_out int32) int32 {
	lookupFunc := i.getSecretStore(int(store_handle))
	if lookupFunc == nil {
		return XqdErrInvalidHandle
	}

	keyBytes := make([]byte, key_size)
	_, err := i.memory.ReadAt(keyBytes, int64(key_addr))
	if err != nil {
		return XqdError
	}

	key := string(keyBytes)

	i.abilog.Printf("secret_store_get: store_handle=%d key=%s", store_handle, key)

	// Look up the secret by key
	plaintext, found := lookupFunc(key)
	if !found {
		i.abilog.Printf("secret_store_get: secret '%s' not found", key)
		i.memory.PutUint32(HandleInvalid, int64(secret_handle_out))
		return XqdErrNone
	}

	// Create a new secret handle that wraps the plaintext
	secretHandle := i.secretHandles.New(plaintext)

	// Write the secret handle to guest memory
	i.memory.PutUint32(uint32(secretHandle), int64(secret_handle_out))

	return XqdStatusOK
}

// xqd_secret_store_plaintext retrieves the plaintext value of a secret.
// This is the only way to access the actual secret value after obtaining a secret handle.
//
// Parameters:
//   - secret_handle: handle to the secret (from xqd_secret_store_get)
//   - plaintext_addr: pointer to buffer in guest memory where plaintext will be written
//   - plaintext_max_len: maximum size of the plaintext buffer
//   - nwritten_out: pointer where the number of bytes written will be stored
//
// Return values:
//   - XqdErrInvalidHandle: the secret handle is invalid
//   - XqdErrBufferLength: the guest's buffer is too small (nwritten_out contains required size)
//   - XqdStatusOK: success (nwritten_out contains bytes written)
func (i *Instance) xqd_secret_store_plaintext(secret_handle int32, plaintext_addr int32, plaintext_max_len int32, nwritten_out int32) int32 {
	secret := i.secretHandles.Get(int(secret_handle))
	if secret == nil {
		return XqdErrInvalidHandle
	}

	plaintext := secret.Plaintext()

	i.abilog.Printf("secret_store_plaintext: secret_handle=%d plaintext_len=%d max_len=%d", secret_handle, len(plaintext), plaintext_max_len)

	// Check if the guest's buffer can hold the plaintext
	if len(plaintext) > int(plaintext_max_len) {
		// Buffer too small - tell the guest how much space is needed
		i.memory.PutUint32(uint32(len(plaintext)), int64(nwritten_out))
		return XqdErrBufferLength
	}

	// Write the plaintext to guest memory
	bytesWritten, err := i.memory.WriteAt(plaintext, int64(plaintext_addr))
	if err != nil {
		return XqdError
	}

	// Write the number of bytes written
	i.memory.PutUint32(uint32(bytesWritten), int64(nwritten_out))

	return XqdStatusOK
}

// xqd_secret_store_from_bytes creates a secret handle from raw bytes.
// This allows creating "injected" secrets that aren't associated with a specific store.
// Useful for secrets obtained from other sources (e.g., generated dynamically, fetched from APIs).
//
// Parameters:
//   - plaintext_addr: pointer to the plaintext bytes in guest memory
//   - plaintext_len: length of the plaintext
//   - secret_handle_out: pointer where the secret handle will be written
//
// Returns XqdStatusOK on success
func (i *Instance) xqd_secret_store_from_bytes(plaintext_addr int32, plaintext_len int32, secret_handle_out int32) int32 {
	plaintextBytes := make([]byte, plaintext_len)
	_, err := i.memory.ReadAt(plaintextBytes, int64(plaintext_addr))
	if err != nil {
		return XqdError
	}

	i.abilog.Printf("secret_store_from_bytes: plaintext_len=%d", plaintext_len)

	// Create a new secret handle with the plaintext bytes
	secretHandle := i.secretHandles.New(plaintextBytes)

	// Write the secret handle to guest memory
	i.memory.PutUint32(uint32(secretHandle), int64(secret_handle_out))

	return XqdStatusOK
}

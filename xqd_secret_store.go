package fastlike

// xqd_secret_store_open opens a secret store by name and returns a handle to it.
// Parameters:
//   - name_addr: pointer to the secret store name string in guest memory
//   - name_size: length of the secret store name
//   - handle_out: pointer where the secret store handle will be written
//
// Returns XqdStatusOK on success, XqdErrNone if the store doesn't exist
func (i *Instance) xqd_secret_store_open(name_addr int32, name_size int32, handle_out int32) int32 {
	buf := make([]byte, name_size)
	_, err := i.memory.ReadAt(buf, int64(name_addr))
	if err != nil {
		return XqdError
	}

	name := string(buf)

	i.abilog.Printf("secret_store_open: name=%s", name)

	// Get the handle for the secret store
	handle := i.getSecretStoreHandle(name)

	// If the store doesn't exist, return XqdErrNone (not found)
	if handle == int(HandleInvalid) {
		i.memory.PutUint32(HandleInvalid, int64(handle_out))
		return XqdErrNone
	}

	// Write the handle to guest memory
	i.memory.PutUint32(uint32(handle), int64(handle_out))

	return XqdStatusOK
}

// xqd_secret_store_get retrieves a secret from a secret store by key.
// Parameters:
//   - store_handle: handle to the secret store
//   - key_addr: pointer to the secret key string in guest memory
//   - key_size: length of the secret key
//   - secret_handle_out: pointer where the secret handle will be written
//
// Returns XqdStatusOK on success, XqdErrInvalidHandle if the store handle is invalid,
// XqdErrNone if the secret doesn't exist
func (i *Instance) xqd_secret_store_get(store_handle int32, key_addr int32, key_size int32, secret_handle_out int32) int32 {
	lookup := i.getSecretStore(int(store_handle))
	if lookup == nil {
		return XqdErrInvalidHandle
	}

	buf := make([]byte, key_size)
	_, err := i.memory.ReadAt(buf, int64(key_addr))
	if err != nil {
		return XqdError
	}

	key := string(buf)

	i.abilog.Printf("secret_store_get: store_handle=%d key=%s", store_handle, key)

	// Look up the secret
	plaintext, found := lookup(key)
	if !found {
		i.abilog.Printf("secret_store_get: secret '%s' not found", key)
		i.memory.PutUint32(HandleInvalid, int64(secret_handle_out))
		return XqdErrNone
	}

	// Create a new secret handle with the plaintext
	secretHandle := i.secretHandles.New(plaintext)

	// Write the secret handle to guest memory
	i.memory.PutUint32(uint32(secretHandle), int64(secret_handle_out))

	return XqdStatusOK
}

// xqd_secret_store_plaintext retrieves the plaintext value of a secret.
// Parameters:
//   - secret_handle: handle to the secret
//   - plaintext_addr: pointer to buffer where plaintext will be written
//   - plaintext_max_len: maximum size of the plaintext buffer
//   - nwritten_out: pointer where the number of bytes written will be stored
//
// Returns XqdStatusOK on success, XqdErrInvalidHandle if the secret handle is invalid,
// XqdErrBufferLength if the buffer is too small
func (i *Instance) xqd_secret_store_plaintext(secret_handle int32, plaintext_addr int32, plaintext_max_len int32, nwritten_out int32) int32 {
	secret := i.secretHandles.Get(int(secret_handle))
	if secret == nil {
		return XqdErrInvalidHandle
	}

	plaintext := secret.Plaintext()

	i.abilog.Printf("secret_store_plaintext: secret_handle=%d plaintext_len=%d max_len=%d", secret_handle, len(plaintext), plaintext_max_len)

	// Check if the buffer is large enough
	if len(plaintext) > int(plaintext_max_len) {
		// Write the required size so the caller can allocate a larger buffer
		i.memory.PutUint32(uint32(len(plaintext)), int64(nwritten_out))
		return XqdErrBufferLength
	}

	// Write the plaintext to guest memory
	nwritten, err := i.memory.WriteAt(plaintext, int64(plaintext_addr))
	if err != nil {
		return XqdError
	}

	// Write the number of bytes written
	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))

	return XqdStatusOK
}

// xqd_secret_store_from_bytes creates a secret handle from raw bytes.
// This allows creating "injected" secrets that aren't associated with a specific store.
// Parameters:
//   - plaintext_addr: pointer to the plaintext bytes in guest memory
//   - plaintext_len: length of the plaintext
//   - secret_handle_out: pointer where the secret handle will be written
//
// Returns XqdStatusOK on success
func (i *Instance) xqd_secret_store_from_bytes(plaintext_addr int32, plaintext_len int32, secret_handle_out int32) int32 {
	buf := make([]byte, plaintext_len)
	_, err := i.memory.ReadAt(buf, int64(plaintext_addr))
	if err != nil {
		return XqdError
	}

	i.abilog.Printf("secret_store_from_bytes: plaintext_len=%d", plaintext_len)

	// Create a new secret handle with the plaintext
	secretHandle := i.secretHandles.New(buf)

	// Write the secret handle to guest memory
	i.memory.PutUint32(uint32(secretHandle), int64(secret_handle_out))

	return XqdStatusOK
}

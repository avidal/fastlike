package fastlike

// SecretLookupFunc is a function that retrieves a secret value by name from a secret store.
// It returns the secret's plaintext bytes and a boolean indicating whether the secret was found.
type SecretLookupFunc func(key string) ([]byte, bool)

// secretStore represents a named secret store with a lookup function
type secretStore struct {
	name   string
	lookup SecretLookupFunc
}

// addSecretStore registers a new secret store with the instance
func (i *Instance) addSecretStore(name string, lookup SecretLookupFunc) {
	i.secretStores = append(i.secretStores, secretStore{name: name, lookup: lookup})
}

// getSecretStoreHandle returns the handle for a secret store by name, or HandleInvalid if not found
func (i *Instance) getSecretStoreHandle(name string) int {
	for idx, store := range i.secretStores {
		if store.name == name {
			// Create a new handle for this secret store
			handle := i.secretStoreHandles.New(name)
			i.abilog.Printf("secret_store: opened store '%s' with handle %d (registry index %d)", name, handle, idx)
			return handle
		}
	}
	i.abilog.Printf("secret_store: store '%s' not found", name)
	return int(HandleInvalid)
}

// getSecretStore returns the SecretLookupFunc for a given secret store handle
func (i *Instance) getSecretStore(handle int) SecretLookupFunc {
	storeHandle := i.secretStoreHandles.Get(handle)
	if storeHandle == nil {
		return nil
	}

	// Find the secret store by name
	for _, store := range i.secretStores {
		if store.name == storeHandle.name {
			return store.lookup
		}
	}

	return nil
}

package fastlike

// addConfigStore registers a named config store with the given lookup function.
func (i *Instance) addConfigStore(name string, fn LookupFunc) {
	if i.configStores == nil {
		i.configStores = []configStore{}
	}

	i.configStores = append(i.configStores, configStore{name, fn})
}

// getConfigStoreHandle retrieves the handle for a named config store.
// Returns HandleInvalid if the config store is not found.
func (i *Instance) getConfigStoreHandle(name string) int {
	for j, cs := range i.configStores {
		if cs.name == name {
			return j
		}
	}

	return HandleInvalid
}

// getConfigStore retrieves a config store's lookup function by handle.
// Returns nil if the handle is invalid.
func (i *Instance) getConfigStore(handle int) LookupFunc {
	if handle < 0 || handle > len(i.configStores)-1 {
		return nil
	}

	return i.configStores[handle].get
}

// configStore represents a named configuration key-value store.
type configStore struct {
	name string
	get  LookupFunc
}

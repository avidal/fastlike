package fastlike

// LookupFunc is a function that retrieves a value by key from a dictionary or config store.
// Returns the value as a string, or an empty string if the key is not found.
type LookupFunc func(key string) string

// addDictionary registers a named dictionary with the given lookup function.
func (i *Instance) addDictionary(name string, fn LookupFunc) {
	if i.dictionaries == nil {
		i.dictionaries = []dictionary{}
	}

	i.dictionaries = append(i.dictionaries, dictionary{name, fn})
}

// getDictionaryHandle retrieves the handle for a named dictionary.
// Returns HandleInvalid if the dictionary is not found.
func (i *Instance) getDictionaryHandle(name string) int {
	for j, d := range i.dictionaries {
		if d.name == name {
			return j
		}
	}

	return HandleInvalid
}

// getDictionary retrieves a dictionary's lookup function by handle.
// Returns nil if the handle is invalid.
func (i *Instance) getDictionary(handle int) LookupFunc {
	if handle < 0 || handle > len(i.dictionaries)-1 {
		return nil
	}

	return i.dictionaries[handle].get
}

// dictionary represents a named key-value lookup store.
type dictionary struct {
	name string
	get  LookupFunc
}

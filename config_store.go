package fastlike

func (i *Instance) addConfigStore(name string, fn LookupFunc) {
	if i.configStores == nil {
		i.configStores = []configStore{}
	}

	i.configStores = append(i.configStores, configStore{name, fn})
}

func (i *Instance) getConfigStoreHandle(name string) int {
	for j, cs := range i.configStores {
		if cs.name == name {
			return j
		}
	}

	return HandleInvalid
}

func (i *Instance) getConfigStore(handle int) LookupFunc {
	if handle < 0 || handle > len(i.configStores)-1 {
		return nil
	}

	return i.configStores[handle].get
}

type configStore struct {
	name string
	get  LookupFunc
}

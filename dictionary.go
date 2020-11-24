package fastlike

type LookupFunc func(key string) string

func (i *Instance) addDictionary(name string, fn LookupFunc) {
	if i.dictionaries == nil {
		i.dictionaries = []dictionary{}
	}

	i.dictionaries = append(i.dictionaries, dictionary{name, fn})
}

func (i *Instance) getDictionaryHandle(name string) int {
	for j, d := range i.dictionaries {
		if d.name == name {
			return j
		}
	}

	return HandleInvalid
}

func (i *Instance) getDictionary(handle int) LookupFunc {
	if handle > len(i.dictionaries)-1 {
		return nil
	}

	return i.dictionaries[handle].get
}

type dictionary struct {
	name string
	get  LookupFunc
}

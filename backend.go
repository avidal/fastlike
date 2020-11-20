package fastlike

import (
	"fmt"
	"net/http"
)

func (i *Instance) addBackend(name string, h http.Handler) {
	i.backends[name] = h
}

func (i *Instance) getBackend(name string) http.Handler {
	h, ok := i.backends[name]
	if !ok {
		return i.defaultBackend(name)
	}

	return h
}

func defaultBackend(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		var msg = fmt.Sprintf(`Unknown backend '%s'. Did you configure your backends correctly?`, name)
		w.Write([]byte(msg))
	})
}

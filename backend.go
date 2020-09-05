package fastlike

import (
	"fmt"
	"net/http"
)

// BackendHandler is a function that takes a backend name and returns an http.Handler that can
// satisfy that backend
type BackendHandler func(backend string) http.Handler

func defaultBackendHandler() BackendHandler {
	return func(backend string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			var msg = fmt.Sprintf(`Unknown backend '%s'. Did you configure your backends correctly?`, backend)
			w.Write([]byte(msg))
		})
	}
}

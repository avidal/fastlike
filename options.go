package fastlike

import (
	"log"
	"net/http"
)

// InstanceOption is a functional option applied to an Instance when it's created
type InstanceOption func(*Instance)

// BackendHandlerOption is an InstanceOption which configures how subrequests are issued by backend
func BackendHandlerOption(b BackendHandler) InstanceOption {
	return func(i *Instance) {
		i.backends = b
	}
}

// GeoHandlerOption is an InstanceOption which controls how geographic requests are handled
func GeoHandlerOption(b http.Handler) InstanceOption {
	return func(i *Instance) {
		i.geobackend = b
	}
}

// LoggerConfigOption is an InstanceOption that allows configuring the loggers
func LoggerConfigOption(fn func(logger, abilogger *log.Logger)) InstanceOption {
	return func(i *Instance) {
		fn(i.log, i.abilog)
	}
}

// SecureRequestOption is an InstanceOption that determines if a request should be considered
// "secure" or not. If it returns true, the request url has the "https" scheme and the "fastly-ssl"
// header set when going into the wasm program.
// The default implementation checks if `req.TLS` is non-nil.
func SecureRequestOption(fn func(*http.Request) bool) InstanceOption {
	return func(i *Instance) {
		i.isSecure = fn
	}
}

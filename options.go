package fastlike

import "log"

// InstanceOption is a functional option applied to an Instance when it's created
type InstanceOption func(*Instance)

// BackendHandlerOption is an InstanceOption which configures how subrequests are issued by backend
func BackendHandlerOption(b BackendHandler) InstanceOption {
	return func(i *Instance) {
		i.backends = b
	}
}

// GeoHandlerOption is an InstanceOption which controls how geographic requests are handled
func GeoHandlerOption(b Backend) InstanceOption {
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

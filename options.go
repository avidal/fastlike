package fastlike

import (
	"io"
	"net"
	"net/http"
	"os"
)

// Option is a functional option applied to an Instance at creation time
type Option func(*Instance)

// WithBackend registers an `http.Handler` identified by `name` used for subrequests targeting that
// backend
func WithBackend(name string, h http.Handler) Option {
	return func(i *Instance) {
		i.addBackend(name, h)
	}
}

// WithDefaultBackend is an Option to override the default subrequest backend.
func WithDefaultBackend(fn func(name string) http.Handler) Option {
	return func(i *Instance) {
		i.defaultBackend = fn
	}
}

// WithGeo replaces the default geographic lookup function
func WithGeo(fn func(net.IP) Geo) Option {
	return func(i *Instance) {
		i.geolookup = fn
	}
}

// WithLogger registers a new log endpoint usable from a wasm guest
func WithLogger(name string, w io.Writer) Option {
	return func(i *Instance) {
		i.addLogger(name, w)
	}
}

// WithDefaultLogger sets the default logger used for logs issued by the guest
// This one is different from WithLogger, because it accepts a name and returns a writer so that
// custom implementations can print the name, if they prefer
func WithDefaultLogger(fn func(name string) io.Writer) Option {
	return func(i *Instance) {
		i.defaultLogger = fn
	}
}

// WithDictionary registers a new dictionary with a corresponding lookup function
func WithDictionary(name string, fn LookupFunc) Option {
	return func(i *Instance) {
		i.addDictionary(name, fn)
	}
}

// WithSecureFunc is an Option that determines if a request should be considered "secure" or not.
// If it returns true, the request url has the "https" scheme and the "fastly-ssl" header set when
// going into the wasm program.
// The default implementation checks if `req.TLS` is non-nil.
func WithSecureFunc(fn func(*http.Request) bool) Option {
	return func(i *Instance) {
		i.secureFn = fn
	}
}

// WithUserAgentParser is an Option that converts user agent header values into UserAgent structs,
// called when the guest code uses the user agent parser XQD call.
func WithUserAgentParser(fn UserAgentParser) Option {
	return func(i *Instance) {
		i.uaparser = fn
	}
}

// WithVerbosity controls how verbose the system level logs are.
// A verbosity of 2 prints all calls from the wasm guest into the host methods
// Currently, verbosity less than 2 does nothing
func WithVerbosity(v int) Option {
	return func(i *Instance) {
		if v >= 2 {
			i.abilog.SetOutput(os.Stderr)
		}
		if v >= 1 {
			i.log.SetOutput(os.Stderr)
		}
	}
}

package fastlike

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
)

// Option is a functional option applied to an Instance at creation time
type Option func(*Instance)

// WithBackend registers an `http.Handler` identified by `name` used for subrequests targeting that
// backend. This creates a simple backend with default settings.
func WithBackend(name string, h http.Handler) Option {
	return func(i *Instance) {
		// Parse a default URL from the backend name
		u, err := url.Parse("http://" + name)
		if err != nil {
			// Fallback to a simple localhost URL
			u, _ = url.Parse("http://localhost")
		}

		backend := &Backend{
			Name:    name,
			URL:     u,
			Handler: h,
		}
		i.addBackend(name, backend)
	}
}

// WithBackendConfig registers a fully configured Backend
func WithBackendConfig(backend *Backend) Option {
	return func(i *Instance) {
		i.addBackend(backend.Name, backend)
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

// WithConfigStore registers a new config store with a corresponding lookup function
func WithConfigStore(name string, fn LookupFunc) Option {
	return func(i *Instance) {
		i.addConfigStore(name, fn)
	}
}

// WithKVStore registers a new KV store with the given name
func WithKVStore(name string) Option {
	return func(i *Instance) {
		store := NewKVStore(name)
		i.addKVStore(name, store)
	}
}

// WithSecretStore registers a new secret store with a corresponding lookup function
func WithSecretStore(name string, fn SecretLookupFunc) Option {
	return func(i *Instance) {
		i.addSecretStore(name, fn)
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

// WithDeviceDetection is an Option that provides device detection data for user agents.
// The function takes a user agent string and returns device detection data as a JSON string.
// If no data is available for a given user agent, the function should return an empty string.
func WithDeviceDetection(fn DeviceLookupFunc) Option {
	return func(i *Instance) {
		i.deviceDetection = fn
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

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

// WithDefaultBackend sets a fallback handler for backend requests to undefined backends.
// The function receives the backend name and returns an http.Handler.
// If not set, undefined backends return 502 Bad Gateway.
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

// WithDefaultLogger sets a fallback logger for log endpoints not registered with WithLogger.
// The function receives the log endpoint name and returns an io.Writer.
// This differs from WithLogger in that it's called dynamically when the guest requests
// an undefined log endpoint, allowing custom implementations to print the endpoint name.
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

// WithKVStoreData registers a pre-populated KV store with the given name
func WithKVStoreData(name string, store *KVStore) Option {
	return func(i *Instance) {
		i.addKVStore(name, store)
	}
}

// WithSecretStore registers a new secret store with a corresponding lookup function
func WithSecretStore(name string, fn SecretLookupFunc) Option {
	return func(i *Instance) {
		i.addSecretStore(name, fn)
	}
}

// WithSecureFunc sets a custom function to determine if a request should be considered "secure".
// When the function returns true, the request will have:
//   - URL scheme set to "https"
//   - "fastly-ssl" header set to "1"
//
// This affects how the wasm guest program sees the request.
// The default implementation checks if req.TLS is non-nil.
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

// WithImageOptimizer is an Option that provides image transformation functionality.
// The function receives the origin request, body, backend name, and transformation config,
// and returns a transformed image response.
// By default, image optimization returns an error indicating it's not configured.
func WithImageOptimizer(fn ImageOptimizerTransformFunc) Option {
	return func(i *Instance) {
		i.imageOptimizer = fn
	}
}

// WithVerbosity controls logging verbosity for ABI calls and system-level operations.
//   - Level 0 (default): No logging
//   - Level 1: System-level logs to stderr
//   - Level 2: All XQD ABI calls from guest to host logged to stderr
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

// WithComplianceRegion sets the compliance region identifier for data locality requirements.
// Valid values include "none", "us-eu", "us", etc.
// This is used by guest programs to implement GDPR compliance and data residency policies.
func WithComplianceRegion(region string) Option {
	return func(i *Instance) {
		i.complianceRegion = region
	}
}

// WithACL registers a new ACL (Access Control List) with the given name.
// The ACL data should be in Fastly's JSON format with entries containing
// IP prefix/mask and actions (ALLOW/BLOCK).
func WithACL(name string, acl *Acl) Option {
	return func(i *Instance) {
		i.addACL(name, acl)
	}
}

// WithRateCounter registers a new rate counter for Edge Rate Limiting.
// Rate counters track request counts over time windows to calculate rates.
// If counter is nil, a new rate counter is created.
func WithRateCounter(name string, counter *RateCounter) Option {
	return func(i *Instance) {
		if counter == nil {
			counter = NewRateCounter()
		}
		i.addRateCounter(name, counter)
	}
}

// WithPenaltyBox registers a new penalty box for Edge Rate Limiting.
// Penalty boxes track entries that have exceeded rate limits.
// If box is nil, a new penalty box is created.
func WithPenaltyBox(name string, box *PenaltyBox) Option {
	return func(i *Instance) {
		if box == nil {
			box = NewPenaltyBox()
		}
		i.addPenaltyBox(name, box)
	}
}

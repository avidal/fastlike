package fastlike

import (
	"fmt"
	"net/http"
	"net/url"
)

// Backend represents a complete backend configuration with all introspectable properties
type Backend struct {
	// Name is the identifier for this backend
	Name string

	// URL is the target URL for this backend (scheme, host, port)
	URL *url.URL

	// OverrideHost is an optional Host header override
	OverrideHost string

	// Handler is the actual http.Handler used to make requests
	Handler http.Handler

	// IsDynamic indicates if this backend was registered at runtime
	IsDynamic bool

	// Timeout settings (in milliseconds)
	ConnectTimeoutMs      uint32
	FirstByteTimeoutMs    uint32
	BetweenBytesTimeoutMs uint32

	// HTTP keepalive time (in milliseconds)
	HTTPKeepaliveTimeMs uint32

	// TCP keepalive settings
	TCPKeepaliveEnable     bool
	TCPKeepaliveTimeMs     uint32
	TCPKeepaliveIntervalMs uint32
	TCPKeepaliveProbes     uint32

	// SSL/TLS settings
	UseSSL        bool
	SSLMinVersion uint32 // TLS version constant
	SSLMaxVersion uint32 // TLS version constant
}

func (i *Instance) addBackend(name string, b *Backend) {
	b.Name = name
	i.backends[name] = b
}

func (i *Instance) getBackend(name string) *Backend {
	b, ok := i.backends[name]
	if !ok {
		return nil
	}

	return b
}

func (i *Instance) backendExists(name string) bool {
	_, ok := i.backends[name]
	return ok
}

func (i *Instance) getBackendHandler(name string) http.Handler {
	b := i.getBackend(name)
	if b == nil {
		return i.defaultBackend(name)
	}

	return b.Handler
}

func defaultBackend(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		msg := fmt.Sprintf(`Unknown backend '%s'. Did you configure your backends correctly?`, name)
		_, _ = w.Write([]byte(msg))
	})
}

package fastlike

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
)

// Backend is a function used to send requests to a backend
type Backend func(*http.Request) (*http.Response, error)

// BackendHandler is a function that takes a backend name and returns a Backend function
type BackendHandler func(backend string) Backend

func defaultBackendHandler() BackendHandler {
	return func(backend string) Backend {
		return func(_ *http.Request) (*http.Response, error) {
			var msg = fmt.Sprintf(`Unknown backend '%s'. Did you configure your backends correctly?`, backend)
			return &http.Response{
				Status:     http.StatusText(http.StatusBadGateway),
				StatusCode: http.StatusBadGateway,
				Body:       ioutil.NopCloser(bytes.NewBufferString(msg)),
			}, nil
		}
	}
}

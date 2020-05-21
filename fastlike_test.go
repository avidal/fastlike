package fastlike_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/khan/fastlike"
)

func TestFastlike(t *testing.T) {
	// Skip the test of the module doesn't exist
	if _, perr := os.Stat("testdata/bin/main.wasm"); os.IsNotExist(perr) {
		t.Skip("wasm test file does not exist. Try running `fastly compute build` in ./testdata")
	}

	f := fastlike.New("testdata/bin/main.wasm")

	// Each test case will create its own instance and request/response pair to test against
	t.Run("simple-response", func(st *testing.T) {
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/simple-response", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.BackendHandlerOption(failingBackendHandler(st)))
		i.ServeHTTP(w, r)

		if w.Body.String() != "Hello, world!" {
			st.Fail()
		}

		if w.Code != http.StatusOK {
			st.Fail()
		}
	})

	t.Run("no-body", func(st *testing.T) {
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/no-body", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.BackendHandlerOption(failingBackendHandler(st)))
		i.ServeHTTP(w, r)

		if w.Body.String() != "" {
			st.Fail()
		}

		if w.Code != http.StatusNoContent {
			st.Fail()
		}
	})

	t.Run("append-body", func(st *testing.T) {
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/append-body", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.BackendHandlerOption(failingBackendHandler(st)))
		i.ServeHTTP(w, r)

		if w.Body.String() != "original\nappended" {
			st.Fail()
		}

		if w.Code != http.StatusOK {
			st.Fail()
		}
	})

	t.Run("user-agent", func(st *testing.T) {
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/user-agent", ioutil.NopCloser(bytes.NewBuffer(nil)))
		r.Header.Set("user-agent", "Mozilla/5.0 (X11; Fedora; Linux x86_64; rv:76.0) Gecko/20100101 Firefox/76.1.15")
		i := f.Instantiate(fastlike.BackendHandlerOption(failingBackendHandler(st)), fastlike.UserAgentParserOption(func(_ string) fastlike.UserAgent {
			return fastlike.UserAgent{
				Family: "Firefox",
				Major:  "76",
				Minor:  "1",
				Patch:  "15",
			}
		}))
		i.ServeHTTP(w, r)

		if w.Body.String() != "Firefox 76.1.15" {
			st.Fail()
		}

		if w.Code != http.StatusOK {
			st.Fail()
		}
	})

	t.Run("proxy", func(st *testing.T) {
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/proxy", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.BackendHandlerOption(testBackendHandler(st, &http.Response{
			StatusCode: http.StatusTeapot,
			Body:       ioutil.NopCloser(bytes.NewBuffer([]byte("i am a teapot"))),
		})))
		i.ServeHTTP(w, r)

		if w.Body.String() != "i am a teapot" {
			st.Fail()
		}

		if w.Code != http.StatusTeapot {
			st.Fail()
		}
	})

	t.Run("append-header", func(st *testing.T) {
		// Assert that we can carry headers via subrequests
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/append-header", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.BackendHandlerOption(func(_ string) fastlike.Backend {
			return func(r *http.Request) (*http.Response, error) {
				defer r.Body.Close()
				if r.Header.Get("test-header") != "test-value" {
					st.Fail()
				}

				return &http.Response{
					StatusCode: http.StatusNoContent, Body: ioutil.NopCloser(bytes.NewBuffer(nil)),
				}, nil
			}
		}))
		i.ServeHTTP(w, r)
	})

	t.Run("panic!", func(st *testing.T) {
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/panic!", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.BackendHandlerOption(failingBackendHandler(st)))
		i.ServeHTTP(w, r)

		if w.Code != http.StatusInternalServerError {
			st.Fail()
		}

		if !strings.Contains(w.Body.String(), "Error running wasm program") {
			st.Fail()
		}
	})

	t.Run("geo", func(st *testing.T) {
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/geo", ioutil.NopCloser(bytes.NewBuffer(nil)))

		// In normal operation (ie, part of an http server handler), these requests will always
		// have a remote addr. But not if you create them yourself.
		r.RemoteAddr = "127.0.0.1:9999"
		i := f.Instantiate(fastlike.BackendHandlerOption(failingBackendHandler(st)))
		i.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			st.Fail()
		}

		var payload = struct {
			ASName string `json:"as_name"`
		}{}
		json.NewDecoder(w.Body).Decode(&payload)

		if payload.ASName != "fastlike" {
			st.Fail()
		}
	})
}

func failingBackendHandler(t *testing.T) fastlike.BackendHandler {
	return func(_ string) fastlike.Backend {
		return func(_ *http.Request) (*http.Response, error) {
			t.Helper()
			t.Fail()
			return nil, errors.New("No subrequests allowed!")
		}
	}
}

func testBackendHandler(t *testing.T, w *http.Response) fastlike.BackendHandler {
	return func(_ string) fastlike.Backend {
		return func(_ *http.Request) (*http.Response, error) {
			t.Helper()
			return w, nil
		}
	}
}

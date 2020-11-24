package fastlike_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"fastlike.dev"
)

const wasmfile = "testdata/target/wasm32-wasi/debug/example.wasm"

func TestFastlike(t *testing.T) {
	t.Parallel()

	// Skip the test if the module doesn't exist
	if _, perr := os.Stat(wasmfile); os.IsNotExist(perr) {
		t.Skip("wasm test file does not exist. Try running `cargo build` in ./testdata")
	}

	f := fastlike.New(wasmfile)

	// Each test case will create its own instance and request/response pair to test against
	t.Run("simple-response", func(st *testing.T) {
		st.Parallel()
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/simple-response", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.WithDefaultBackend(failingBackendHandler(st)))
		i.ServeHTTP(w, r)

		if w.Body.String() != "Hello, world!" {
			st.Fail()
		}

		if w.Code != http.StatusOK {
			st.Fail()
		}
	})

	t.Run("no-body", func(st *testing.T) {
		st.Parallel()
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/no-body", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.WithDefaultBackend(failingBackendHandler(st)))
		i.ServeHTTP(w, r)

		if w.Body.String() != "" {
			st.Fail()
		}

		if w.Code != http.StatusNoContent {
			st.Fail()
		}
	})

	t.Run("append-body", func(st *testing.T) {
		st.Parallel()
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/append-body", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.WithDefaultBackend(failingBackendHandler(st)))
		i.ServeHTTP(w, r)

		if w.Body.String() != "original\nappended" {
			st.Fail()
		}

		if w.Code != http.StatusOK {
			st.Fail()
		}
	})

	t.Run("user-agent", func(st *testing.T) {
		st.Parallel()
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/user-agent", ioutil.NopCloser(bytes.NewBuffer(nil)))
		r.Header.Set("user-agent", "Mozilla/5.0 (X11; Fedora; Linux x86_64; rv:76.0) Gecko/20100101 Firefox/76.1.15")
		i := f.Instantiate(fastlike.WithDefaultBackend(failingBackendHandler(st)), fastlike.WithUserAgentParser(func(_ string) fastlike.UserAgent {
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
		st.Parallel()
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/proxy", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.WithDefaultBackend(testBackendHandler(st, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
			w.Write([]byte("i am a teapot"))
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
		st.Parallel()
		// Assert that we can carry headers via subrequests
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/append-header", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.WithDefaultBackend(testBackendHandler(st, func(w http.ResponseWriter, r *http.Request) {
			defer r.Body.Close()
			if r.Header.Get("test-header") != "test-value" {
				st.Fail()
			}

			w.WriteHeader(http.StatusNoContent)
		})))
		i.ServeHTTP(w, r)
	})

	t.Run("panic!", func(st *testing.T) {
		st.Parallel()
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/panic!", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.WithDefaultBackend(failingBackendHandler(st)))
		i.ServeHTTP(w, r)

		if w.Code != http.StatusInternalServerError {
			st.Fail()
		}

		if !strings.Contains(w.Body.String(), "Error running wasm program") {
			st.Fail()
		}
	})

	t.Run("geo", func(st *testing.T) {
		st.Parallel()
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/geo", ioutil.NopCloser(bytes.NewBuffer(nil)))

		// In normal operation (ie, part of an http server handler), these requests will always
		// have a remote addr. But not if you create them yourself.
		r.RemoteAddr = "127.0.0.1:9999"
		i := f.Instantiate(fastlike.WithDefaultBackend(failingBackendHandler(st)))
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

	t.Run("logger", func(st *testing.T) {
		st.Parallel()
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/log", ioutil.NopCloser(bytes.NewBuffer(nil)))

		// In normal operation (ie, part of an http server handler), these requests will always
		// have a remote addr. But not if you create them yourself.
		r.RemoteAddr = "127.0.0.1:9999"
		buf := new(bytes.Buffer)
		i := f.Instantiate(
			fastlike.WithDefaultBackend(failingBackendHandler(st)),
			fastlike.WithLogger("default", buf),
		)
		i.ServeHTTP(w, r)

		if w.Code != http.StatusNoContent {
			st.Fail()
		}

		// The contents of the buffer must be "Hello from fastlike!"
		actual := buf.String()
		expected := "Hello from fastlike!\n"
		if actual != expected {
			st.Logf("expected %q, got %q", expected, actual)
			st.Fail()
		}
	})

	t.Run("dictionary", func(st *testing.T) {
		st.Parallel()
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/dictionary/testdict/testkey", ioutil.NopCloser(bytes.NewBuffer(nil)))

		// In normal operation (ie, part of an http server handler), these requests will always
		// have a remote addr. But not if you create them yourself.
		r.RemoteAddr = "127.0.0.1:9999"
		i := f.Instantiate(
			fastlike.WithDefaultBackend(failingBackendHandler(st)),
			fastlike.WithDictionary("testdict", func(key string) string {
				if key == "testkey" {
					return "Hello from the dictionary"
				}
				return ""
			}),
		)
		i.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			st.Fail()
		}

		actual := w.Body.String()
		expected := "Hello from the dictionary"
		if actual != expected {
			st.Logf("expected %q, got %q", expected, actual)
			st.Fail()
		}
	})

	t.Run("parallel", func(st *testing.T) {
		// Assert that we can safely handle concurrent requests by sending off 5 requests each of
		// which sleep for 500ms in the host.
		for i := 1; i <= 5; i++ {
			st.Run("", func(stt *testing.T) {
				stt.Parallel()
				w := httptest.NewRecorder()
				r, _ := http.NewRequest("GET", "http://localhost:1337/proxy", ioutil.NopCloser(bytes.NewBuffer(nil)))

				r.RemoteAddr = "127.0.0.1:9999"
				i := f.Instantiate(fastlike.WithDefaultBackend(testBackendHandler(st, func(w http.ResponseWriter, r *http.Request) {
					<-time.After(500 * time.Millisecond)
					w.WriteHeader(http.StatusTeapot)
					w.Write([]byte("i am a teapot"))
				})))
				i.ServeHTTP(w, r)

				if w.Code != http.StatusTeapot {
					stt.Fail()
				}
			})
		}
	})

	t.Run("context-cancel", func(st *testing.T) {
		st.Parallel()
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/proxy", ioutil.NopCloser(bytes.NewBuffer(nil)))
		ctx, cancel := context.WithTimeout(r.Context(), 50*time.Millisecond)
		defer cancel()

		r = r.WithContext(ctx)
		r.RemoteAddr = "127.0.0.1:9999"
		i := f.Instantiate(fastlike.WithDefaultBackend(testBackendHandler(st, func(w http.ResponseWriter, r *http.Request) {
			<-time.After(100 * time.Millisecond)
			w.WriteHeader(http.StatusTeapot)
			w.Write([]byte("i am a teapot"))
		})))
		i.ServeHTTP(w, r)

		if w.Code != http.StatusInternalServerError {
			st.Fail()
		}

		// TODO: Come up with a better way to test this behavior.
		// If the embedding application sets up a custom deadline for fastlike calls, they may want
		// to catch this case and return their own response. In the default setup though, the
		// context will only cancel when the client hangs up and they won't see whatever we write
		// to the response.
		if !strings.Contains(w.Body.String(), "wasm trap: interrupt") {
			st.Fail()
		}
	})
}

func failingBackendHandler(t *testing.T) func(string) http.Handler {
	return func(_ string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Helper()
			t.Fail()
			w.WriteHeader(http.StatusTeapot)
		})
	}
}

func testBackendHandler(t *testing.T, h http.HandlerFunc) func(string) http.Handler {
	return func(_ string) http.Handler {
		t.Helper()
		return h
	}
}

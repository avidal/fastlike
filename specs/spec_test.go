package spec_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"fastlike.dev"
)

var wasmfile = flag.String("wasm", "testdata/rust/target/wasm32-wasip1/debug/example.wasm", "wasm program to run spec tests against")

func TestFastlike(t *testing.T) {
	t.Parallel()

	// Skip the test if the module doesn't exist
	if _, perr := os.Stat(*wasmfile); os.IsNotExist(perr) {
		t.Logf("wasm test file '%s' does not exist.", *wasmfile)
		t.Log("Note that paths are resolved relative to the specs/ directory, not where you ran go test from.")
		t.Log("Either specify an absolute path, or cd into ./specs first.")
		t.Skip()
	}

	f := fastlike.New(*wasmfile)

	// Each test case will create its own instance and request/response pair to test against
	t.Run("simple-response", func(st *testing.T) {
		st.Parallel()
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/simple-response", io.NopCloser(bytes.NewBuffer(nil)))
		inst := f.Instantiate(fastlike.WithDefaultBackend(failingBackendHandler(st)))
		inst.ServeHTTP(w, r)

		expectedBody := "Hello, world!"
		if w.Body.String() != expectedBody {
			st.Errorf("Expected body %q, got %q", expectedBody, w.Body.String())
		}

		if w.Code != http.StatusOK {
			st.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
		}
	})

	t.Run("no-body", func(st *testing.T) {
		st.Parallel()
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/no-body", io.NopCloser(bytes.NewBuffer(nil)))
		inst := f.Instantiate(fastlike.WithDefaultBackend(failingBackendHandler(st)))
		inst.ServeHTTP(w, r)

		if w.Body.String() != "" {
			st.Errorf("Expected empty body, got %q", w.Body.String())
		}

		if w.Code != http.StatusNoContent {
			st.Errorf("Expected status %d, got %d", http.StatusNoContent, w.Code)
		}
	})

	t.Run("append-body", func(st *testing.T) {
		st.Parallel()
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/append-body", io.NopCloser(bytes.NewBuffer(nil)))
		inst := f.Instantiate(fastlike.WithDefaultBackend(failingBackendHandler(st)))
		inst.ServeHTTP(w, r)

		expectedBody := "original\nappended"
		if w.Body.String() != expectedBody {
			st.Errorf("Expected body %q, got %q", expectedBody, w.Body.String())
		}

		if w.Code != http.StatusOK {
			st.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
		}
	})

	t.Run("user-agent", func(st *testing.T) {
		st.Parallel()
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/user-agent", io.NopCloser(bytes.NewBuffer(nil)))
		r.Header.Set("user-agent", "Mozilla/5.0 (X11; Fedora; Linux x86_64; rv:76.0) Gecko/20100101 Firefox/76.1.15")
		inst := f.Instantiate(fastlike.WithDefaultBackend(failingBackendHandler(st)), fastlike.WithUserAgentParser(func(_ string) fastlike.UserAgent {
			return fastlike.UserAgent{
				Family: "Firefox",
				Major:  "76",
				Minor:  "1",
				Patch:  "15",
			}
		}))
		inst.ServeHTTP(w, r)

		expectedBody := "Firefox 76.1.15"
		if w.Body.String() != expectedBody {
			st.Errorf("Expected body %q, got %q", expectedBody, w.Body.String())
		}

		if w.Code != http.StatusOK {
			st.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
		}
	})

	t.Run("proxy", func(st *testing.T) {
		st.Parallel()
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/proxy", io.NopCloser(bytes.NewBuffer(nil)))
		inst := f.Instantiate(fastlike.WithDefaultBackend(testBackendHandler(st, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte("i am a teapot"))
		})))
		inst.ServeHTTP(w, r)

		expectedBody := "i am a teapot"
		if w.Body.String() != expectedBody {
			st.Errorf("Expected body %q, got %q", expectedBody, w.Body.String())
		}

		if w.Code != http.StatusTeapot {
			st.Errorf("Expected status %d, got %d", http.StatusTeapot, w.Code)
		}
	})

	t.Run("append-header", func(st *testing.T) {
		st.Parallel()
		// Verify that headers are correctly passed through subrequests
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/append-header", io.NopCloser(bytes.NewBuffer(nil)))
		inst := f.Instantiate(fastlike.WithDefaultBackend(testBackendHandler(st, func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			expectedHeader := "test-value"
			if actualHeader := r.Header.Get("test-header"); actualHeader != expectedHeader {
				st.Errorf("Expected header 'test-header' to be %q, got %q", expectedHeader, actualHeader)
			}

			w.WriteHeader(http.StatusNoContent)
		})))
		inst.ServeHTTP(w, r)
	})

	t.Run("panic!", func(st *testing.T) {
		st.Parallel()
		// Verify that wasm panics are caught and return 500 errors
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/panic!", io.NopCloser(bytes.NewBuffer(nil)))
		inst := f.Instantiate(fastlike.WithDefaultBackend(failingBackendHandler(st)))
		inst.ServeHTTP(w, r)

		if w.Code != http.StatusInternalServerError {
			st.Errorf("Expected status %d, got %d", http.StatusInternalServerError, w.Code)
		}

		expectedErrorText := "Error running wasm program"
		if !strings.Contains(w.Body.String(), expectedErrorText) {
			st.Errorf("Expected error message to contain %q, got %q", expectedErrorText, w.Body.String())
		}
	})

	t.Run("geo", func(st *testing.T) {
		st.Parallel()
		// Verify geolocation API returns correct data
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/geo", io.NopCloser(bytes.NewBuffer(nil)))

		// Set RemoteAddr explicitly (normally set by http.Server but not in tests)
		r.RemoteAddr = "127.0.0.1:9999"
		inst := f.Instantiate(fastlike.WithDefaultBackend(failingBackendHandler(st)))
		inst.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			st.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
		}

		payload := struct {
			ASName string `json:"as_name"`
		}{}
		if err := json.NewDecoder(w.Body).Decode(&payload); err != nil {
			st.Errorf("Failed to decode response body: %v", err)
		}

		expectedASName := "fastlike"
		if payload.ASName != expectedASName {
			st.Errorf("Expected AS name %q, got %q", expectedASName, payload.ASName)
		}
	})

	t.Run("logger", func(st *testing.T) {
		st.Parallel()
		// Verify logging API writes to the configured logger
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/log", io.NopCloser(bytes.NewBuffer(nil)))

		// Set RemoteAddr explicitly (normally set by http.Server but not in tests)
		r.RemoteAddr = "127.0.0.1:9999"
		logBuffer := new(bytes.Buffer)
		inst := f.Instantiate(
			fastlike.WithDefaultBackend(failingBackendHandler(st)),
			fastlike.WithLogger("default", logBuffer),
		)
		inst.ServeHTTP(w, r)

		if w.Code != http.StatusNoContent {
			st.Errorf("Expected status %d, got %d", http.StatusNoContent, w.Code)
		}

		expected := "Hello from fastlike!\n"
		actual := logBuffer.String()
		if actual != expected {
			st.Errorf("Expected log output %q, got %q", expected, actual)
		}
	})

	t.Run("dictionary", func(st *testing.T) {
		st.Parallel()
		// Verify dictionary lookup returns correct values
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/dictionary/testdict/testkey", io.NopCloser(bytes.NewBuffer(nil)))

		// Set RemoteAddr explicitly (normally set by http.Server but not in tests)
		r.RemoteAddr = "127.0.0.1:9999"
		inst := f.Instantiate(
			fastlike.WithDefaultBackend(failingBackendHandler(st)),
			fastlike.WithDictionary("testdict", func(key string) string {
				if key == "testkey" {
					return "Hello from the dictionary"
				}
				return ""
			}),
		)
		inst.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			st.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
		}

		expected := "Hello from the dictionary"
		actual := w.Body.String()
		if actual != expected {
			st.Errorf("Expected body %q, got %q", expected, actual)
		}
	})

	t.Run("parallel", func(st *testing.T) {
		// Verify that concurrent requests are handled safely by running 5 parallel requests,
		// each with a backend that sleeps for 500ms
		for requestNum := 1; requestNum <= 5; requestNum++ {
			st.Run("", func(stt *testing.T) {
				stt.Parallel()
				w := httptest.NewRecorder()
				r, _ := http.NewRequest("GET", "http://localhost:1337/proxy", io.NopCloser(bytes.NewBuffer(nil)))

				r.RemoteAddr = "127.0.0.1:9999"
				inst := f.Instantiate(fastlike.WithDefaultBackend(testBackendHandler(st, func(w http.ResponseWriter, r *http.Request) {
					<-time.After(500 * time.Millisecond)
					w.WriteHeader(http.StatusTeapot)
					_, _ = w.Write([]byte("i am a teapot"))
				})))
				inst.ServeHTTP(w, r)

				if w.Code != http.StatusTeapot {
					stt.Errorf("Expected status %d, got %d", http.StatusTeapot, w.Code)
				}
			})
		}
	})

	t.Run("context-cancel", func(st *testing.T) {
		st.Parallel()
		// Verify that context cancellation properly interrupts wasm execution
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "http://localhost:1337/proxy", io.NopCloser(bytes.NewBuffer(nil)))
		r.Header.Set("fastlike-verbose", "1") // Enable verbose logging

		// Create a context that times out before the backend responds
		ctx, cancel := context.WithTimeout(r.Context(), 50*time.Millisecond)
		defer cancel()

		r = r.WithContext(ctx)
		r.RemoteAddr = "127.0.0.1:9999"
		inst := f.Instantiate(fastlike.WithVerbosity(2), fastlike.WithDefaultBackend(testBackendHandler(st, func(w http.ResponseWriter, r *http.Request) {
			<-time.After(100 * time.Millisecond) // Backend takes longer than context timeout
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte("i am a teapot"))
		})))
		inst.ServeHTTP(w, r)

		if w.Code != http.StatusInternalServerError {
			st.Errorf("Expected status %d, got %d", http.StatusInternalServerError, w.Code)
		}

		// Verify the error indicates an interrupted wasm execution
		// TODO: Come up with a better way to test this behavior.
		// If the embedding application sets up a custom deadline for fastlike calls, they may want
		// to catch this case and return their own response. In the default setup though, the
		// context will only cancel when the client hangs up and they won't see whatever we write
		// to the response.
		expectedError := "wasm trap: interrupt"
		if !strings.Contains(w.Body.String(), expectedError) {
			st.Errorf("Expected error message to contain %q, got %q", expectedError, w.Body.String())
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

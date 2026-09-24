package spec_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
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

	t.Run("http-cache", func(st *testing.T) {
		st.Parallel()
		// Cacheable responses are served through the HTTP cache, on misses and
		// hits alike.
		var fetches atomic.Int32
		inst := f.Instantiate(fastlike.WithDefaultBackend(testBackendHandler(st, func(w http.ResponseWriter, r *http.Request) {
			fetches.Add(1)
			if r.Header.Get("If-None-Match") != "" {
				st.Errorf("backend received the client's conditional header %q", r.Header.Get("If-None-Match"))
			}
			w.Header().Set("Cache-Control", "max-age=60")
			w.Header().Set("ETag", `"v1"`)
			w.Header().Set("X-Backend", "origin")
			if r.URL.Path == "/proxy/moved" {
				w.Header().Set("Location", "/elsewhere")
				w.WriteHeader(http.StatusMovedPermanently)
				return
			}
			_, _ = w.Write([]byte("cached body"))
		})))

		for n, label := range []string{"miss", "hit"} {
			w := serveGet(inst, "/proxy/cached", nil)
			if w.Code != http.StatusOK || w.Body.String() != "cached body" {
				st.Errorf("%s: got %d %q, want 200 %q", label, w.Code, w.Body.String(), "cached body")
			}
			if got := w.Header().Get("X-Backend"); got != "origin" {
				st.Errorf("%s: X-Backend = %q, want %q", label, got, "origin")
			}
			if got := w.Header().Get("Accept-Ranges"); got != "bytes" {
				st.Errorf("%s: Accept-Ranges = %q, want %q", label, got, "bytes")
			}
			if w.Header().Get("Age") == "" {
				st.Errorf("%s: no Age header", label)
			}
			if got := fetches.Load(); got != 1 {
				st.Errorf("%s: backend fetched %d times after %d requests, want 1", label, got, n+1)
			}
		}

		w := serveGet(inst, "/proxy/cached", http.Header{"If-None-Match": {`"v1"`}})
		if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
			st.Errorf("conditional hit: got %d %q, want an empty 304", w.Code, w.Body.String())
		}
		if got := w.Header().Get("ETag"); got != `"v1"` {
			st.Errorf("conditional hit: ETag = %q, want %q", got, `"v1"`)
		}

		w = serveGet(inst, "/proxy/moved", nil)
		if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/elsewhere" {
			st.Errorf("redirect: got %d with Location %q", w.Code, w.Header().Get("Location"))
		}
	})

	t.Run("http-cache-private", func(st *testing.T) {
		st.Parallel()
		// Private responses are never served from the cache.
		var fetches atomic.Int32
		inst := f.Instantiate(fastlike.WithDefaultBackend(testBackendHandler(st, func(w http.ResponseWriter, _ *http.Request) {
			fetches.Add(1)
			w.Header().Set("Cache-Control", "private, max-age=60")
			_, _ = w.Write([]byte("personal"))
		})))
		for n := range 2 {
			if w := serveGet(inst, "/proxy/private", nil); w.Code != http.StatusOK || w.Body.String() != "personal" {
				st.Errorf("request %d: got %d %q", n, w.Code, w.Body.String())
			}
		}
		if got := fetches.Load(); got != 2 {
			st.Errorf("backend fetched %d times for two requests, want 2", got)
		}
	})

	t.Run("http-cache-vary", func(st *testing.T) {
		st.Parallel()
		// The suggested vary rule comes from Vary, so each encoding gets its
		// own variant.
		var fetches atomic.Int32
		inst := f.Instantiate(fastlike.WithDefaultBackend(testBackendHandler(st, func(w http.ResponseWriter, r *http.Request) {
			fetches.Add(1)
			w.Header().Set("Cache-Control", "max-age=60")
			w.Header().Set("Vary", "Accept-Encoding")
			_, _ = w.Write([]byte("encoding:" + r.Header.Get("Accept-Encoding")))
		})))
		for _, encoding := range []string{"gzip", "br", "gzip", "br"} {
			w := serveGet(inst, "/proxy/vary", http.Header{"Accept-Encoding": {encoding}})
			if want := "encoding:" + encoding; w.Body.String() != want {
				st.Errorf("Accept-Encoding %s: got %q, want %q", encoding, w.Body.String(), want)
			}
		}
		if got := fetches.Load(); got != 2 {
			st.Errorf("backend fetched %d times, want 2", got)
		}
	})

	t.Run("http-cache-range", func(st *testing.T) {
		st.Parallel()
		// Hits answer single ranges with a 206 stating the stored length.
		var fetches atomic.Int32
		inst := f.Instantiate(fastlike.WithDefaultBackend(testBackendHandler(st, func(w http.ResponseWriter, r *http.Request) {
			fetches.Add(1)
			if r.Header.Get("Range") != "" {
				st.Errorf("backend received the client's Range %q", r.Header.Get("Range"))
			}
			w.Header().Set("Cache-Control", "max-age=60")
			_, _ = w.Write([]byte("0123456789"))
		})))
		tests := []struct {
			rangeHeader  string
			status       int
			contentRange string
			body         string
		}{
			{"", http.StatusOK, "", "0123456789"},
			{"bytes=2-5", http.StatusPartialContent, "bytes 2-5/10", "2345"},
			{"bytes=-3", http.StatusPartialContent, "bytes 7-9/10", "789"},
			{"bytes=4-4", http.StatusOK, "", "0123456789"},
		}
		for _, tt := range tests {
			var header http.Header
			if tt.rangeHeader != "" {
				header = http.Header{"Range": {tt.rangeHeader}}
			}
			w := serveGet(inst, "/proxy/range", header)
			if w.Code != tt.status || w.Header().Get("Content-Range") != tt.contentRange || w.Body.String() != tt.body {
				st.Errorf("Range %q: got %d %q %q, want %d %q %q", tt.rangeHeader, w.Code, w.Header().Get("Content-Range"), w.Body.String(), tt.status, tt.contentRange, tt.body)
			}
		}
		if got := fetches.Load(); got != 1 {
			st.Errorf("backend fetched %d times, want 1", got)
		}
	})

	t.Run("http-cache-expires", func(st *testing.T) {
		st.Parallel()
		// An Expires equal to Date leaves no freshness, instead of the default
		// hour.
		var fetches atomic.Int32
		inst := f.Instantiate(fastlike.WithDefaultBackend(testBackendHandler(st, func(w http.ResponseWriter, _ *http.Request) {
			fetches.Add(1)
			now := time.Now().UTC().Format(http.TimeFormat)
			w.Header().Set("Date", now)
			w.Header().Set("Expires", now)
			_, _ = w.Write([]byte("already stale"))
		})))
		for range 2 {
			if w := serveGet(inst, "/proxy/expires", nil); w.Code != http.StatusOK || w.Body.String() != "already stale" {
				st.Errorf("got %d %q", w.Code, w.Body.String())
			}
		}
		if got := fetches.Load(); got != 2 {
			st.Errorf("backend fetched %d times for two requests, want 2", got)
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

		// Measure execution time to verify interruption actually occurred
		start := time.Now()
		inst.ServeHTTP(w, r)
		elapsed := time.Since(start)

		if w.Code != http.StatusInternalServerError {
			st.Errorf("Expected status %d, got %d", http.StatusInternalServerError, w.Code)
		}

		// Verify the error indicates an interrupted wasm execution
		expectedError := "wasm trap: interrupt"
		if !strings.Contains(w.Body.String(), expectedError) {
			st.Errorf("Expected error message to contain %q, got %q", expectedError, w.Body.String())
		}

		// Verify that the execution was actually interrupted (not just that the backend timed out).
		// The context timeout is 50ms but the backend takes 100ms to respond.
		// If interruption works correctly, the test should complete in < 100ms.
		// We allow up to 90ms to account for overhead while ensuring it didn't wait the full 100ms.
		maxAllowedDuration := 90 * time.Millisecond
		if elapsed > maxAllowedDuration {
			st.Errorf("Expected interruption to occur within %v, but took %v (backend would take 100ms)",
				maxAllowedDuration, elapsed)
		}

		// Note on production behavior:
		// In real deployments, when a client disconnects (ctx.Done()), they won't see the error response
		// since the connection is already closed. This test uses httptest.ResponseRecorder which captures
		// the response even after context cancellation, allowing us to verify the interruption behavior.
		// Embedding applications may want to use middleware to detect context cancellation and return
		// custom error responses before calling fastlike.ServeHTTP().
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

// serveGet sends a GET for path with the given headers to inst.
func serveGet(inst *fastlike.Instance, path string, header http.Header) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r, _ := http.NewRequest("GET", "http://localhost:1337"+path, io.NopCloser(bytes.NewBuffer(nil)))
	maps.Copy(r.Header, header)
	inst.ServeHTTP(w, r)
	return w
}

func testBackendHandler(t *testing.T, h http.HandlerFunc) func(string) http.Handler {
	return func(_ string) http.Handler {
		t.Helper()
		return h
	}
}

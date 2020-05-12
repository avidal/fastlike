package fastlike_test

import (
	"bytes"
	"errors"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
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
		r, _ := http.NewRequest("GET", "localhost:1337/simple-response", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.SubrequestHandlerOption(failingSubrequestHandler(st)))
		i.ServeHTTP(w, r)

		if w.Body.String() != "Hello, world!" {
			t.Fail()
		}

		if w.Code != http.StatusOK {
			t.Fail()
		}
	})

	t.Run("no-body", func(st *testing.T) {
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "localhost:1337/no-body", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.SubrequestHandlerOption(failingSubrequestHandler(st)))
		i.ServeHTTP(w, r)

		if w.Body.String() != "" {
			t.Fail()
		}

		if w.Code != http.StatusNoContent {
			t.Fail()
		}
	})

	t.Run("proxy", func(st *testing.T) {
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "localhost:1337/proxy", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.SubrequestHandlerOption(testSubrequestHandler(st, &http.Response{
			StatusCode: http.StatusTeapot,
			Body:       ioutil.NopCloser(bytes.NewBuffer([]byte("i am a teapot"))),
		})))
		i.ServeHTTP(w, r)

		if w.Body.String() != "i am a teapot" {
			t.Fail()
		}

		if w.Code != http.StatusTeapot {
			t.Fail()
		}
	})

	t.Run("append-header", func(st *testing.T) {
		// Assert that we can carry headers via subrequests
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("GET", "localhost:1337/append-header", ioutil.NopCloser(bytes.NewBuffer(nil)))
		i := f.Instantiate(fastlike.SubrequestHandlerOption(func(_ string, r *http.Request) (*http.Response, error) {
			if r.Header.Get("test-header") != "test-value" {
				st.Fail()
			}

			return &http.Response{
				StatusCode: http.StatusNoContent, Body: ioutil.NopCloser(bytes.NewBuffer(nil)),
			}, nil
		}))
		i.ServeHTTP(w, r)
	})
}

func failingSubrequestHandler(t *testing.T) fastlike.SubrequestHandler {
	return func(_ string, _ *http.Request) (*http.Response, error) {
		t.Helper()
		t.Fail()
		return nil, errors.New("No subrequests allowed!")
	}
}

func testSubrequestHandler(t *testing.T, w *http.Response) fastlike.SubrequestHandler {
	return func(_ string, _ *http.Request) (*http.Response, error) {
		t.Helper()
		return w, nil
	}
}

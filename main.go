package main

import (
	"net/http"

	"github.com/khan/fastlike/fastlike"
)

func main() {
	fastlike := fastlike.New("./bin/main.wasm")
	fastlike.Transport(transportFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Host = "localhost:8000"
		return http.DefaultClient.Do(r)
	}))
	http.ListenAndServe("localhost:5001", fastlike)
}

type transportFunc func(*http.Request) (*http.Response, error)

func (t transportFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return t(r)
}

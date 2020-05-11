package main

import (
	"net/http"

	"github.com/khan/fastlike/fastlike"
)

func main() {
	fastlike := fastlike.New("./bin/main.wasm")

	// Send all subrequests to localhost:8000, regardless of backend
	fastlike.SubrequestHandler(func(_ string, r *http.Request) (*http.Response, error) {
		r.URL.Host = "localhost:8000"
		return http.DefaultClient.Do(r)
	})
	http.ListenAndServe("localhost:5001", fastlike)
}

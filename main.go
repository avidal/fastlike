package main

import (
	"net/http"

	"github.com/khan/fastlike/fastlike"
)

func main() {
	fastlike := fastlike.New("./bin/main.wasm")
	http.ListenAndServe("localhost:5001", fastlike)
}

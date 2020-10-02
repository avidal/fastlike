package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"fastlike.dev"
)

func main() {
	var wasm = flag.String("wasm", "", "wasm program to execute")
	var bind = flag.String("b", "localhost:5000", "address to bind to")
	var proxyTo = flag.String("proxy-to", "", "(required) override to send all subrequests to")
	flag.Parse()

	if *wasm == "" {
		fmt.Fprintf(flag.CommandLine.Output(), "-wasm argument is required\n")
		flag.Usage()
		os.Exit(1)
	}

	httpbin := httputil.NewSingleHostReverseProxy(parse("http://httpbin.org"))
	proxy := httputil.NewSingleHostReverseProxy(parse(*proxyTo))

	var opts = []fastlike.InstanceOption{}

	// NOTE: You probably want to specify a proxy-to, otherwise any requests that get proxied
	// without changing the hostname will loop and be blocked.
	if *proxyTo != "" {
		opts = append(opts, fastlike.BackendHandlerOption(func(be string) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if be == "httpbin" {
					httpbin.ServeHTTP(w, r)
				} else {
					proxy.ServeHTTP(w, r)
				}
			})
		}))
	}

	fl := fastlike.New(*wasm, opts...)

	fmt.Printf("Listening on %s\n", *bind)
	if err := http.ListenAndServe(*bind, fl); err != nil {
		fmt.Printf("Error starting server, got %s\n", err.Error())
	}
}

func parse(u string) *url.URL {
	if !strings.HasPrefix(u, "http") {
		u = fmt.Sprintf("http://%s", u)
	}

	out, err := url.Parse(u)
	if err != nil {
		panic(err)
	}

	return out
}

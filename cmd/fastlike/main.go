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
	var bind = flag.String("bind", "localhost:5000", "address to bind to")

	var backends = make(backendFlags)
	flag.Var(&backends, "backend", "<name=address> specifying backends. Use an empty name to specify a catch-all backend (ex: -backend localhost:2000)")
	flag.Var(&backends, "b", "alias for -backend")

	flag.Parse()

	if *wasm == "" {
		fmt.Fprintf(flag.CommandLine.Output(), "-wasm argument is required\n")
		flag.Usage()
		os.Exit(1)
	}

	if len(backends) == 0 {
		fmt.Fprintf(flag.CommandLine.Output(), "at least one -backend is required\n")
		flag.Usage()
		os.Exit(1)
	}

	var opts = []fastlike.InstanceOption{}

	opts = append(opts, fastlike.BackendHandlerOption(func(be string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			backend, exists := backends[be]
			if exists {
				backend.proxy.ServeHTTP(w, r)
				return
			}

			fallback, exists := backends[""]
			if exists {
				fallback.proxy.ServeHTTP(w, r)
				return
			}

			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(fmt.Sprintf("Unknown backend %s.", be)))
		})
	}))

	fl := fastlike.New(*wasm, opts...)

	fmt.Printf("Listening on %s\n", *bind)
	if err := http.ListenAndServe(*bind, fl); err != nil {
		fmt.Printf("Error starting server, got %s\n", err.Error())
	}
}

type backend struct {
	address string
	proxy   http.Handler
}
type backendFlags map[string]backend

func (f *backendFlags) String() string {
	rv := make([]string, len(*f))
	for name, b := range *f {
		rv = append(rv, fmt.Sprintf("%s=%s", name, b.address))
	}
	return strings.Join(rv, ", ")
}
func (f *backendFlags) Set(v string) error {
	parts := strings.Split(v, "=")
	name, addr := "", ""
	if len(parts) == 2 {
		name = parts[0]
		addr = parts[1]
	} else if len(parts) == 1 {
		name = ""
		addr = parts[0]
	} else {
		return fmt.Errorf("invalid backend %s specified", v)
	}

	// turn the address into an http/https url
	if !strings.HasPrefix(addr, "http") {
		addr = fmt.Sprintf("http://%s", addr)
	}

	dest, err := url.Parse(addr)
	if err != nil {
		return err
	}

	proxy := httputil.NewSingleHostReverseProxy(dest)

	(*f)[name] = backend{address: addr, proxy: proxy}
	return nil
}

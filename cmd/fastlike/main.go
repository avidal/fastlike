package main

import (
	"encoding/json"
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
	var verbosity = flag.Int("v", 0, "verbosity level (0, 1, 2)")

	var backends = make(backendFlags)
	flag.Var(&backends, "backend", "<name=address> specifying backends. Use an empty name to specify a catch-all backend (ex: -backend localhost:2000)")
	flag.Var(&backends, "b", "alias for -backend")

	var dictionaries = make(dictionaryFlags)
	flag.Var(&dictionaries, "dictionary", "<name=file.json> specifying dictionaries. The JSON file supplied must only contain string values.")
	flag.Var(&dictionaries, "d", "alias for -dictionary")

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

	var opts = []fastlike.Option{}

	for name, backend := range backends {
		if name == "" {
			opts = append(opts, fastlike.WithDefaultBackend(func(_ string) http.Handler {
				return backend.proxy
			}))
		} else {
			opts = append(opts, fastlike.WithBackend(name, backend.proxy))
		}
	}

	for name, dictionary := range dictionaries {
		opts = append(opts, fastlike.WithDictionary(name, dictionary.fn))
	}

	opts = append(opts, fastlike.WithVerbosity(*verbosity))

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

type dictionary struct {
	name     string
	filename string
	fn       fastlike.LookupFunc
}
type dictionaryFlags map[string]dictionary

func (f *dictionaryFlags) String() string {
	rv := make([]string, len(*f))
	for name, b := range *f {
		rv = append(rv, fmt.Sprintf("%s=%s", name, b.filename))
	}
	return strings.Join(rv, ", ")
}
func (f *dictionaryFlags) Set(v string) error {
	parts := strings.Split(v, "=")
	if len(parts) != 2 {
		return fmt.Errorf("invalid dictionary %s specified", v)
	}

	name := parts[0]
	filename := parts[1]

	// read in the contents of the file
	fd, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("error opening dictionary file %s, got %s", filename, err.Error())
	}

	content := map[string]string{}
	if err := json.NewDecoder(fd).Decode(&content); err != nil {
		return fmt.Errorf("error parsing dictionary file %s, got %s", filename, err.Error())
	}

	(*f)[name] = dictionary{name: name, filename: filename, fn: func(key string) string {
		if v, ok := content[key]; !ok {
			return ""
		} else {
			return v
		}
	}}
	return nil
}

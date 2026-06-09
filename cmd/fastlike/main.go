package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"fastlike.dev/profile"

	"fastlike.dev"
)

// cliTransport is the *http.Transport every CLI-registered named backend
// shares. Sharing one transport across backends is intentional: connection
// pooling is amortised, and the profile recorder installs its
// httptrace.ClientTrace per request via context rather than per transport,
// so the trace events stay correctly attributed.
var cliTransport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          100,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

func main() {
	bind := flag.String("bind", "localhost:8000", "address to bind to")
	verbosity := flag.Int("v", 0, "verbosity level (0, 1, 2)")
	reloadOnSIGHUP := flag.Bool("reload", false, "enable SIGHUP handler for hot-reloading wasm module")
	complianceRegion := flag.String("compliance-region", "", "compliance region identifier (e.g., 'none', 'us-eu', 'us')")

	backends := make(backendFlags)
	flag.Var(&backends, "backend", "<name=address[@uptime%]> specifying backends. Use an empty name to specify a catch-all backend (ex: -backend localhost:2000). Append @N (0..100) to simulate reliability, e.g. -backend api=localhost:2000@50.")
	flag.Var(&backends, "b", "alias for -backend")

	dictionaries := make(dictionaryFlags)
	flag.Var(&dictionaries, "dictionary", "<name=file.json> specifying dictionaries. The JSON file supplied must only contain string values.")
	flag.Var(&dictionaries, "d", "alias for -dictionary")

	kvStores := make(kvStoreFlags)
	flag.Var(&kvStores, "kv", "<name=file.json> or <name> specifying KV stores. Use name=file.json to load data, or just name for an empty store.")

	configStores := make(configStoreFlags)
	flag.Var(&configStores, "config-store", "<name=file.json> specifying config stores. The JSON file must contain string key-value pairs.")

	secretStores := make(secretStoreFlags)
	flag.Var(&secretStores, "secret-store", "<name=file.json> specifying secret stores. The JSON file must contain string key-value pairs.")

	acls := make(aclFlags)
	flag.Var(&acls, "acl", "<name=file.json> specifying ACLs. The JSON file must contain an 'entries' array with prefix/action objects.")

	loggers := make(loggerFlags)
	flag.Var(&loggers, "logger", "<name=file> or <name> specifying log endpoints. Use name=file to log to a file, or just name to log to stdout.")

	var fastlyKeys stringListFlags
	flag.Var(&fastlyKeys, "fake-fastly-key", "a Fastly-Key header value that fastly_key_is_valid should treat as valid (repeatable)")

	geoFile := flag.String("geo", "", "JSON file containing IP to geo mapping for geolocation lookups")

	profileMode := flag.String("profile", "trace", "profiling mode: off, trace, native, combined, deep. Defaults to trace; collection runs even without a UI.")
	profileUI := flag.String("profile-ui", "", "address to bind the profiler UI on. Empty disables the UI listener entirely. Loopback binds require no auth; non-loopback binds require -profile-auth or -profile-insecure-ui.")
	profileAuth := flag.String("profile-auth", "", "bearer token required on every profiler UI request. Required for non-loopback -profile-ui unless -profile-insecure-ui is set.")
	profileInsecure := flag.Bool("profile-insecure-ui", false, "allow a non-loopback -profile-ui without -profile-auth. Intended only for environments with externalized auth (mTLS, authenticating reverse proxy). Logs a prominent startup warning.")
	profileRetain := flag.Int("profile-retain", 0, "per-Fastlike LRU size for completed traces. <=0 keeps the package default.")
	profileBackendCap := flag.Int("profile-backend-cap", 0, "per-request cap on recorded backend calls. <=0 keeps the package default.")
	profileAsyncGrace := flag.Duration("profile-async-grace", 0, "max time finalize waits for in-flight async backends. <=0 keeps the package default; pass 0s explicitly to disable via the default.")
	profileDir := flag.String("profile-dir", "", "directory to write per-process profile artifacts (wasm-symbols-{pid}.json and, in a future step, completed traces). Empty writes the symbol manifest to the working directory.")

	flag.Usage = func() {
		out := flag.CommandLine.Output()
		_, _ = fmt.Fprintf(out, "Usage: %s [OPTIONS] <wasm-file> [OPTIONS]\n\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
	}

	// Go's flag.Parse stops at the first non-flag argument. To accept flags
	// on either side of the positional <wasm-file> (viceroy-style), parse,
	// consume the wasm path, then keep parsing what's left.
	args := os.Args[1:]
	wasmPath := ""
	for {
		if err := flag.CommandLine.Parse(args); err != nil {
			os.Exit(2)
		}
		if flag.NArg() == 0 {
			break
		}
		if wasmPath != "" {
			_, _ = fmt.Fprintf(flag.CommandLine.Output(), "unexpected extra positional argument %q (only one <wasm-file> allowed)\n", flag.Arg(0))
			flag.Usage()
			os.Exit(1)
		}
		wasmPath = flag.Arg(0)
		args = flag.Args()[1:]
	}

	if wasmPath == "" {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "a positional <wasm-file> argument is required\n")
		flag.Usage()
		os.Exit(1)
	}

	if len(backends) == 0 {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "at least one -backend is required\n")
		flag.Usage()
		os.Exit(1)
	}

	opts := []fastlike.Option{}

	for name, backend := range backends {
		proxy := backend.proxy
		if name == "" {
			// Catch-all backends remain plain (no traced transport); the
			// default-backend factory pattern does not surface a transport
			// fastlike can observe.
			if backend.uptime != nil {
				opts = append(opts, fastlike.WithUnreliableDefaultBackend(func(_ string) http.Handler {
					return proxy
				}, *backend.uptime))
			} else {
				opts = append(opts, fastlike.WithDefaultBackend(func(_ string) http.Handler {
					return proxy
				}))
			}
		} else {
			// Named backends register via WithBackendTraced so the profile
			// recorder gets DNS / connect / TLS / TTFB phase data from
			// httptrace.ClientTrace. The reliability wrapper still applies;
			// addBackend wraps the handler before the recorder observes it.
			if backend.uptime != nil {
				// WithBackendConfig does NOT synthesize a URL from the
				// name the way WithBackend / WithBackendTraced do, so we
				// have to set it explicitly here. Without it, every
				// fastly_backend_get_host / _get_port / _is_ssl hostcall
				// crashes on a nil *url.URL dereference. Mirror the
				// http://name parse from options.go's other Backend
				// constructors so the fallback string ('localhost') is
				// identical across paths.
				u, err := url.Parse("http://" + name)
				if err != nil {
					u, _ = url.Parse("http://localhost")
				}
				opts = append(opts, fastlike.WithBackendConfig(&fastlike.Backend{
					Name:          name,
					URL:           u,
					Handler:       proxy,
					Transport:     cliTransport,
					UptimePercent: backend.uptime,
				}))
			} else {
				opts = append(opts, fastlike.WithBackendTraced(name, proxy, cliTransport))
			}
		}
	}

	for name, dictionary := range dictionaries {
		opts = append(opts, fastlike.WithDictionary(name, dictionary.fn))
	}

	for name, kvStore := range kvStores {
		opts = append(opts, fastlike.WithKVStoreData(name, kvStore.store))
	}

	for name, configStore := range configStores {
		opts = append(opts, fastlike.WithConfigStore(name, configStore.fn))
	}

	for name, secretStore := range secretStores {
		opts = append(opts, fastlike.WithSecretStore(name, secretStore.fn))
	}

	for name, acl := range acls {
		opts = append(opts, fastlike.WithACL(name, acl.acl))
	}

	for name, logger := range loggers {
		opts = append(opts, fastlike.WithLogger(name, logger.writer))
	}

	if *geoFile != "" {
		geoLookup, err := loadGeoFile(*geoFile)
		if err != nil {
			fmt.Printf("Error loading geo file: %s\n", err.Error())
			os.Exit(1)
		}
		opts = append(opts, fastlike.WithGeo(geoLookup))
	}

	if *complianceRegion != "" {
		opts = append(opts, fastlike.WithComplianceRegion(*complianceRegion))
	}

	if len(fastlyKeys) > 0 {
		opts = append(opts, fastlike.WithFakeValidFastlyKeys(fastlyKeys...))
	}

	opts = append(opts, fastlike.WithVerbosity(*verbosity))

	mode := profile.ProfileMode(*profileMode)
	switch mode {
	case profile.ProfileModeOff, profile.ProfileModeTrace, profile.ProfileModeNative, profile.ProfileModeCombined, profile.ProfileModeDeep:
	default:
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "invalid -profile mode %q; valid: off, trace, native, combined, deep\n", *profileMode)
		os.Exit(1)
	}

	if err := profile.ValidateProfileUIAuth(*profileUI, *profileAuth, *profileInsecure); err != nil {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "%s\n", err)
		os.Exit(1)
	}

	flOpts := []fastlike.FastlikeOption{
		fastlike.WithInstanceOptions(opts...),
		fastlike.WithProfileMode(mode),
	}
	if *profileUI != "" {
		flOpts = append(flOpts, fastlike.WithProfileUI(*profileUI))
	}
	if *profileAuth != "" {
		flOpts = append(flOpts, fastlike.WithProfileAuth(*profileAuth))
	}
	if *profileInsecure {
		flOpts = append(flOpts, fastlike.WithProfileInsecureUI())
	}
	if *profileRetain > 0 {
		flOpts = append(flOpts, fastlike.WithProfileRetain(*profileRetain))
	}
	if *profileBackendCap > 0 {
		flOpts = append(flOpts, fastlike.WithProfileBackendCap(*profileBackendCap))
	}
	if *profileAsyncGrace > 0 {
		flOpts = append(flOpts, fastlike.WithProfileAsyncGrace(*profileAsyncGrace))
	}
	if *profileDir != "" {
		flOpts = append(flOpts, fastlike.WithProfileDir(*profileDir))
	}

	fl := fastlike.New(wasmPath, flOpts...)

	if *reloadOnSIGHUP {
		fl.EnableReloadOnSIGHUP()
		fmt.Printf("SIGHUP reload enabled. Send SIGHUP signal to reload wasm module.\n")
	}

	if *profileUI != "" {
		startProfileUI(fl, *profileUI, *profileAuth, *profileInsecure)
	}

	fmt.Printf("Listening on %s\n", *bind)
	if err := http.ListenAndServe(*bind, fl); err != nil {
		fmt.Printf("Error starting server, got %s\n", err.Error())
	}
}

// stringListFlags implements flag.Value for a repeatable string flag,
// accumulating one value per occurrence. Empty values are dropped so an
// accidental empty flag does not register a value that can never match.
type stringListFlags []string

func (f *stringListFlags) String() string { return strings.Join(*f, ", ") }

func (f *stringListFlags) Set(v string) error {
	if v != "" {
		*f = append(*f, v)
	}
	return nil
}

// backend represents a configured backend with its address and reverse proxy handler
type backend struct {
	address string
	proxy   http.Handler
	uptime  *uint8
}

// backendFlags implements flag.Value for parsing -backend flags
type backendFlags map[string]backend

func (f *backendFlags) String() string {
	results := make([]string, 0, len(*f))
	for name, b := range *f {
		results = append(results, fmt.Sprintf("%s=%s", name, b.address))
	}
	return strings.Join(results, ", ")
}

// splitReliabilitySuffix peels a trailing "@N" reliability suffix off addr
// where N is purely digits in [0, 100]. The suffix is the substring after the
// last '@' in the address. Anything else (basic-auth in a URL, an "@abc" tail,
// or no '@' at all) is left attached so URLs that legitimately contain '@'
// keep working unchanged.
func splitReliabilitySuffix(addr string) (string, *uint8, error) {
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return addr, nil, nil
	}
	suffix := addr[at+1:]
	if suffix == "" {
		return addr, nil, nil
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return addr, nil, nil
		}
	}
	n, err := strconv.Atoi(suffix)
	if err != nil {
		return addr, nil, nil
	}
	if n < 0 || n > 100 {
		return "", nil, fmt.Errorf("backend uptime suffix %q out of range, must be 0..100", suffix)
	}
	uptime := uint8(n)
	return addr[:at], &uptime, nil
}

func (f *backendFlags) Set(v string) error {
	parts := strings.SplitN(v, "=", 2)
	name, addr := "", ""
	if len(parts) == 2 {
		name = parts[0]
		addr = parts[1]
	} else {
		addr = parts[0]
	}

	addr, uptime, err := splitReliabilitySuffix(addr)
	if err != nil {
		return err
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
	proxy.Transport = cliTransport

	(*f)[name] = backend{address: addr, proxy: proxy, uptime: uptime}
	return nil
}

// dictionary represents a configured dictionary with its lookup function
type dictionary struct {
	name     string
	filename string
	fn       fastlike.LookupFunc
}

// dictionaryFlags implements flag.Value for parsing -dictionary flags
type dictionaryFlags map[string]dictionary

func (f *dictionaryFlags) String() string {
	results := make([]string, 0, len(*f))
	for name, dict := range *f {
		results = append(results, fmt.Sprintf("%s=%s", name, dict.filename))
	}
	return strings.Join(results, ", ")
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
	defer func() { _ = fd.Close() }()

	content := map[string]string{}
	if err := json.NewDecoder(fd).Decode(&content); err != nil {
		return fmt.Errorf("error parsing dictionary file %s, got %s", filename, err.Error())
	}

	// Create a lookup function that returns the value for a key, or empty string if not found
	lookupFunc := func(key string) string {
		return content[key]
	}

	(*f)[name] = dictionary{name: name, filename: filename, fn: lookupFunc}
	return nil
}

// kvStoreEntry represents a configured KV store
type kvStoreEntry struct {
	name     string
	filename string
	store    *fastlike.KVStore
}

// kvStoreFlags implements flag.Value for parsing -kv flags
type kvStoreFlags map[string]kvStoreEntry

func (f *kvStoreFlags) String() string {
	results := make([]string, 0, len(*f))
	for name, kv := range *f {
		if kv.filename != "" {
			results = append(results, fmt.Sprintf("%s=%s", name, kv.filename))
		} else {
			results = append(results, name)
		}
	}
	return strings.Join(results, ", ")
}

func (f *kvStoreFlags) Set(v string) error {
	parts := strings.SplitN(v, "=", 2)
	name := parts[0]
	filename := ""

	store := fastlike.NewKVStore(name)

	if len(parts) == 2 {
		filename = parts[1]

		// Read and parse the JSON file
		fd, err := os.Open(filename)
		if err != nil {
			return fmt.Errorf("error opening KV store file %s: %s", filename, err.Error())
		}
		defer func() { _ = fd.Close() }()

		// Parse as map of string to any (value can be string or object with body/metadata)
		var content map[string]json.RawMessage
		if err := json.NewDecoder(fd).Decode(&content); err != nil {
			return fmt.Errorf("error parsing KV store file %s: %s", filename, err.Error())
		}

		// Populate the store
		for key, rawValue := range content {
			// Try to parse as simple string first
			var strValue string
			if err := json.Unmarshal(rawValue, &strValue); err == nil {
				if _, err := store.Insert(key, []byte(strValue), "", nil, fastlike.InsertModeOverwrite, nil); err != nil {
					return fmt.Errorf("error inserting key %s: %s", key, err.Error())
				}
				continue
			}

			// Try to parse as object with body and optional metadata
			var objValue struct {
				Body     string `json:"body"`
				Metadata string `json:"metadata"`
			}
			if err := json.Unmarshal(rawValue, &objValue); err == nil {
				if _, err := store.Insert(key, []byte(objValue.Body), objValue.Metadata, nil, fastlike.InsertModeOverwrite, nil); err != nil {
					return fmt.Errorf("error inserting key %s: %s", key, err.Error())
				}
				continue
			}

			return fmt.Errorf("invalid value for key %s: must be a string or object with body/metadata", key)
		}
	}

	(*f)[name] = kvStoreEntry{name: name, filename: filename, store: store}
	return nil
}

// configStoreEntry represents a configured config store
type configStoreEntry struct {
	name     string
	filename string
	fn       fastlike.LookupFunc
}

// configStoreFlags implements flag.Value for parsing -config-store flags
type configStoreFlags map[string]configStoreEntry

func (f *configStoreFlags) String() string {
	results := make([]string, 0, len(*f))
	for name, cs := range *f {
		results = append(results, fmt.Sprintf("%s=%s", name, cs.filename))
	}
	return strings.Join(results, ", ")
}

func (f *configStoreFlags) Set(v string) error {
	parts := strings.SplitN(v, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid config store %s specified, expected name=file.json", v)
	}

	name := parts[0]
	filename := parts[1]

	fd, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("error opening config store file %s: %s", filename, err.Error())
	}
	defer func() { _ = fd.Close() }()

	content := map[string]string{}
	if err := json.NewDecoder(fd).Decode(&content); err != nil {
		return fmt.Errorf("error parsing config store file %s: %s", filename, err.Error())
	}

	lookupFunc := func(key string) string {
		return content[key]
	}

	(*f)[name] = configStoreEntry{name: name, filename: filename, fn: lookupFunc}
	return nil
}

// secretStoreEntry represents a configured secret store
type secretStoreEntry struct {
	name     string
	filename string
	fn       fastlike.SecretLookupFunc
}

// secretStoreFlags implements flag.Value for parsing -secret-store flags
type secretStoreFlags map[string]secretStoreEntry

func (f *secretStoreFlags) String() string {
	results := make([]string, 0, len(*f))
	for name, ss := range *f {
		results = append(results, fmt.Sprintf("%s=%s", name, ss.filename))
	}
	return strings.Join(results, ", ")
}

func (f *secretStoreFlags) Set(v string) error {
	parts := strings.SplitN(v, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid secret store %s specified, expected name=file.json", v)
	}

	name := parts[0]
	filename := parts[1]

	fd, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("error opening secret store file %s: %s", filename, err.Error())
	}
	defer func() { _ = fd.Close() }()

	content := map[string]string{}
	if err := json.NewDecoder(fd).Decode(&content); err != nil {
		return fmt.Errorf("error parsing secret store file %s: %s", filename, err.Error())
	}

	lookupFunc := func(key string) ([]byte, bool) {
		if value, exists := content[key]; exists {
			return []byte(value), true
		}
		return nil, false
	}

	(*f)[name] = secretStoreEntry{name: name, filename: filename, fn: lookupFunc}
	return nil
}

// aclEntry represents a configured ACL
type aclEntry struct {
	name     string
	filename string
	acl      *fastlike.Acl
}

// aclFlags implements flag.Value for parsing -acl flags
type aclFlags map[string]aclEntry

func (f *aclFlags) String() string {
	results := make([]string, 0, len(*f))
	for name, a := range *f {
		results = append(results, fmt.Sprintf("%s=%s", name, a.filename))
	}
	return strings.Join(results, ", ")
}

func (f *aclFlags) Set(v string) error {
	parts := strings.SplitN(v, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid ACL %s specified, expected name=file.json", v)
	}

	name := parts[0]
	filename := parts[1]

	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("error reading ACL file %s: %s", filename, err.Error())
	}

	acl, err := fastlike.ParseACL(data)
	if err != nil {
		return fmt.Errorf("error parsing ACL file %s: %s", filename, err.Error())
	}

	(*f)[name] = aclEntry{name: name, filename: filename, acl: acl}
	return nil
}

// loggerEntry represents a configured logger
type loggerEntry struct {
	name     string
	filename string
	writer   *os.File
}

// loggerFlags implements flag.Value for parsing -logger flags
type loggerFlags map[string]loggerEntry

func (f *loggerFlags) String() string {
	results := make([]string, 0, len(*f))
	for name, l := range *f {
		if l.filename != "" {
			results = append(results, fmt.Sprintf("%s=%s", name, l.filename))
		} else {
			results = append(results, name)
		}
	}
	return strings.Join(results, ", ")
}

func (f *loggerFlags) Set(v string) error {
	parts := strings.SplitN(v, "=", 2)
	name := parts[0]
	filename := ""
	var writer *os.File

	if len(parts) == 2 {
		filename = parts[1]
		fd, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("error opening logger file %s: %s", filename, err.Error())
		}
		writer = fd
	} else {
		writer = os.Stdout
	}

	(*f)[name] = loggerEntry{name: name, filename: filename, writer: writer}
	return nil
}

// loadGeoFile loads a JSON file mapping IP addresses/CIDRs to Geo data
func loadGeoFile(filename string) (func(ip net.IP) fastlike.Geo, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("error reading geo file: %s", err.Error())
	}

	// Parse as map of IP/CIDR string to Geo struct
	var geoData map[string]fastlike.Geo
	if err := json.Unmarshal(data, &geoData); err != nil {
		return nil, fmt.Errorf("error parsing geo file: %s", err.Error())
	}

	// Pre-parse CIDRs for efficient lookup
	type geoEntry struct {
		network *net.IPNet
		ip      net.IP
		geo     fastlike.Geo
	}

	entries := make([]geoEntry, 0, len(geoData))
	for key, geo := range geoData {
		// Try parsing as CIDR first
		_, network, err := net.ParseCIDR(key)
		if err == nil {
			entries = append(entries, geoEntry{network: network, geo: geo})
			continue
		}

		// Try parsing as plain IP
		ip := net.ParseIP(key)
		if ip != nil {
			entries = append(entries, geoEntry{ip: ip, geo: geo})
			continue
		}

		return nil, fmt.Errorf("invalid IP or CIDR in geo file: %s", key)
	}

	// Default geo for unknown IPs
	defaultGeo := fastlike.Geo{
		ASName:       "fastlike",
		ASNumber:     64496,
		AreaCode:     512,
		City:         "Austin",
		CountryCode:  "US",
		CountryCode3: "USA",
		CountryName:  "United States of America",
		Continent:    "NA",
		Region:       "TX",
		ConnSpeed:    "satellite",
		ConnType:     "satellite",
	}

	return func(ip net.IP) fastlike.Geo {
		// Check exact IP matches first
		for _, entry := range entries {
			if entry.ip != nil && entry.ip.Equal(ip) {
				return entry.geo
			}
		}

		// Check CIDR matches (most specific wins)
		var bestMatch *geoEntry
		var bestMaskSize int

		for i := range entries {
			entry := &entries[i]
			if entry.network != nil && entry.network.Contains(ip) {
				maskSize, _ := entry.network.Mask.Size()
				if bestMatch == nil || maskSize > bestMaskSize {
					bestMatch = entry
					bestMaskSize = maskSize
				}
			}
		}

		if bestMatch != nil {
			return bestMatch.geo
		}

		return defaultGeo
	}, nil
}

// startProfileUI binds the profiler UI on a separate socket from the wasm
// listener. The two never share a listener regardless of -profile-ui, per
// the security policy in plans/guest-profiling.md. Auth and the
// loopback-vs-non-loopback gate were already validated by
// ValidateProfileUIAuth before this function ran; here we bind synchronously
// so the user only sees the "UI available" log line after the socket
// actually came up. A bind failure exits the process so the operator does
// not silently lose observability.
func startProfileUI(fl *fastlike.Fastlike, addr, token string, insecure bool) {
	store := fl.ProfileStore()
	if store == nil {
		fmt.Printf("profiler UI requested at %s but profiling is disabled; not binding listener\n", addr)
		return
	}

	// Detect mode=off explicitly so the operator gets a clear message
	// instead of a UI that always shows zero traces.
	if fl.ProfileMode() == profile.ProfileModeOff {
		fmt.Printf("profiler UI requested at %s but -profile=off disables collection; not binding listener\n", addr)
		return
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Printf("profiler UI listener failed to bind %s: %s\n", addr, err)
		os.Exit(1)
	}

	handler := profile.WrapProfileUIAuth(profile.NewProfileUI(store), token)
	loopback := profile.IsLoopbackBindAddress(addr)
	switch {
	case loopback && token == "":
		fmt.Printf("profiler UI at http://%s/\n", listener.Addr())
	case token != "":
		fmt.Printf("profiler UI at http://%s/ (bearer auth required)\n", listener.Addr())
	case insecure:
		fmt.Printf("WARNING: profiler UI at http://%s/ has no authentication; -profile-insecure-ui exposes every captured trace to anyone who can reach this bind\n", listener.Addr())
	}

	srv := &http.Server{Handler: handler}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Printf("profiler UI listener exited: %s\n", err)
		}
	}()
}

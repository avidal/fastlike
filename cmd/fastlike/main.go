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
	"strings"

	"fastlike.dev"
)

func main() {
	wasm := flag.String("wasm", "", "wasm program to execute")
	bind := flag.String("bind", "localhost:5000", "address to bind to")
	verbosity := flag.Int("v", 0, "verbosity level (0, 1, 2)")
	reloadOnSIGHUP := flag.Bool("reload", false, "enable SIGHUP handler for hot-reloading wasm module")
	complianceRegion := flag.String("compliance-region", "", "compliance region identifier (e.g., 'none', 'us-eu', 'us')")

	backends := make(backendFlags)
	flag.Var(&backends, "backend", "<name=address> specifying backends. Use an empty name to specify a catch-all backend (ex: -backend localhost:2000)")
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

	geoFile := flag.String("geo", "", "JSON file containing IP to geo mapping for geolocation lookups")

	flag.Parse()

	if *wasm == "" {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "-wasm argument is required\n")
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

	opts = append(opts, fastlike.WithVerbosity(*verbosity))

	fl := fastlike.New(*wasm, opts...)

	if *reloadOnSIGHUP {
		fl.EnableReloadOnSIGHUP()
		fmt.Printf("SIGHUP reload enabled. Send SIGHUP signal to reload wasm module.\n")
	}

	fmt.Printf("Listening on %s\n", *bind)
	if err := http.ListenAndServe(*bind, fl); err != nil {
		fmt.Printf("Error starting server, got %s\n", err.Error())
	}
}

// backend represents a configured backend with its address and reverse proxy handler
type backend struct {
	address string
	proxy   http.Handler
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
	defer fd.Close()

	content := map[string]string{}
	if err := json.NewDecoder(fd).Decode(&content); err != nil {
		return fmt.Errorf("error parsing dictionary file %s, got %s", filename, err.Error())
	}

	// Create a lookup function that returns the value for a key, or empty string if not found
	lookupFunc := func(key string) string {
		if value, exists := content[key]; exists {
			return value
		}
		return ""
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
		defer fd.Close()

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
	defer fd.Close()

	content := map[string]string{}
	if err := json.NewDecoder(fd).Decode(&content); err != nil {
		return fmt.Errorf("error parsing config store file %s: %s", filename, err.Error())
	}

	lookupFunc := func(key string) string {
		if value, exists := content[key]; exists {
			return value
		}
		return ""
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
	defer fd.Close()

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

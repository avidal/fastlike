package fastlike

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// CachedObject represents a cached entry with metadata and body
type CachedObject struct {
	Body                   *bytes.Buffer
	MaxAgeNs               uint64
	InitialAgeNs           uint64
	StaleWhileRevalidateNs uint64
	EdgeMaxAgeNs           uint64
	VaryRule               string
	SurrogateKeys          []string
	UserMetadata           []byte
	Length                 *uint64 // nil if unknown
	RequestHeaders         []byte  // serialized headers used for vary
	InsertTime             time.Time
	HitCount               uint64
	WriteComplete          bool
	WriteCond              *sync.Cond // for streaming concurrent reads
	SensitiveData          bool       // whether data is sensitive (PCI, etc)
}

// CacheState represents the state flags for a cache lookup
type CacheState struct {
	Found              bool
	Usable             bool
	Stale              bool
	MustInsertOrUpdate bool
}

// CacheEntry holds a cache entry and its state
type CacheEntry struct {
	Object *CachedObject
	State  CacheState
}

// CacheTransaction represents an ongoing cache transaction with request collapsing
type CacheTransaction struct {
	Key            []byte
	Entry          *CacheEntry
	RequestHeaders []byte
	VaryRule       string
	Options        *CacheLookupOptions
	RequestURL     string        // The original request URL for suggested backend requests
	RequestMethod  string        // The original request method for suggested backend requests
	ready          chan struct{} // closed when lookup completes
}

// CacheLookupOptions holds options for cache lookup
type CacheLookupOptions struct {
	RequestHeaders          []byte
	AlwaysUseRequestedRange bool
}

// CacheWriteOptions holds options for cache insertion
type CacheWriteOptions struct {
	MaxAgeNs               uint64
	RequestHeaders         []byte
	VaryRule               string
	InitialAgeNs           *uint64
	StaleWhileRevalidateNs *uint64
	SurrogateKeys          []string
	Length                 *uint64
	UserMetadata           []byte
	EdgeMaxAgeNs           *uint64
	SensitiveData          bool
}

// CacheReplaceStrategy defines how to handle cache replacement
type CacheReplaceStrategy uint32

const (
	CacheReplaceImmediate          CacheReplaceStrategy = 1
	CacheReplaceImmediateForceMiss CacheReplaceStrategy = 2
	CacheReplaceWait               CacheReplaceStrategy = 3
)

// CacheReplaceOptions holds options for cache replace operations
type CacheReplaceOptions struct {
	RequestHeaders          []byte
	ReplaceStrategy         CacheReplaceStrategy
	AlwaysUseRequestedRange bool
}

// Cache is an in-memory cache with request collapsing support
type Cache struct {
	mu             sync.RWMutex
	objects        map[string][]*CachedObject   // key -> variants (for vary support)
	transactions   map[string]*CacheTransaction // key -> pending transaction
	surrogateIndex map[string][]string          // surrogate_key -> cache_keys
}

// NewCache creates a new cache instance
func NewCache() *Cache {
	return &Cache{
		objects:        make(map[string][]*CachedObject),
		transactions:   make(map[string]*CacheTransaction),
		surrogateIndex: make(map[string][]string),
	}
}

// cacheKey converts a byte slice key into a string suitable for use as a Go map key.
func cacheKey(key []byte) string {
	return string(key)
}

// extractVaryHeaders extracts only the headers named in the vary rule from serialized request headers.
// The vary rule is a comma-separated list of header names (e.g., "Accept-Encoding, Accept-Language").
// The request headers are in HTTP wire format as produced by http.Header.Write().
// Returns a normalized representation suitable for comparison.
func extractVaryHeaders(varyRule string, requestHeaders []byte) []byte {
	if varyRule == "" || len(requestHeaders) == 0 {
		return nil
	}

	varyHeaders := make(map[string]bool)
	for _, name := range strings.Split(varyRule, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			varyHeaders[strings.ToLower(name)] = true
		}
	}

	if len(varyHeaders) == 0 {
		return nil
	}

	extracted := make(map[string][]string)
	lines := strings.Split(string(requestHeaders), "\r\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		colonIdx := strings.Index(line, ":")
		if colonIdx == -1 {
			continue
		}
		name := strings.TrimSpace(line[:colonIdx])
		value := strings.TrimSpace(line[colonIdx+1:])
		nameLower := strings.ToLower(name)
		if varyHeaders[nameLower] {
			extracted[nameLower] = append(extracted[nameLower], value)
		}
	}

	var sortedNames []string
	for name := range extracted {
		sortedNames = append(sortedNames, name)
	}
	sort.Strings(sortedNames)

	var result bytes.Buffer
	for _, name := range sortedNames {
		values := extracted[name]
		sort.Strings(values)
		for _, v := range values {
			result.WriteString(name)
			result.WriteByte(':')
			result.WriteString(v)
			result.WriteByte('\n')
		}
	}
	return result.Bytes()
}

// varyKey creates a variant-specific cache key that incorporates the vary rule and request headers.
// If no vary rule is specified, returns the base cache key unchanged.
func (c *Cache) varyKey(baseKey []byte, varyRule string, requestHeaders []byte) string {
	if varyRule == "" {
		return cacheKey(baseKey)
	}

	varyHeaderValues := extractVaryHeaders(varyRule, requestHeaders)

	h := sha256.New()
	h.Write(baseKey)
	h.Write([]byte(varyRule))
	h.Write(varyHeaderValues)
	return hex.EncodeToString(h.Sum(nil))
}

// findMatchingVariant finds a cached object that matches the vary rule and request headers.
//
// Behavior:
//   - If no vary rule is provided: returns the most recently inserted variant
//   - If vary rule is provided: returns the variant whose vary key matches the request
//   - Returns nil if no matching variant is found
func (c *Cache) findMatchingVariant(key []byte, varyRule string, requestHeaders []byte) *CachedObject {
	keyStr := cacheKey(key)
	variants, ok := c.objects[keyStr]
	if !ok {
		return nil
	}

	// If no vary rule in lookup, use most recent entry
	if varyRule == "" && requestHeaders == nil {
		// Find most recent variant
		var newest *CachedObject
		for _, v := range variants {
			if newest == nil || v.InsertTime.After(newest.InsertTime) {
				newest = v
			}
		}
		return newest
	}

	// Match based on vary rule and headers
	vKey := c.varyKey(key, varyRule, requestHeaders)
	for _, v := range variants {
		if c.varyKey(key, v.VaryRule, v.RequestHeaders) == vKey {
			return v
		}
	}

	return nil
}

// Lookup performs a non-transactional cache lookup
func (c *Cache) Lookup(key []byte, options *CacheLookupOptions) *CacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var requestHeaders []byte
	if options != nil {
		requestHeaders = options.RequestHeaders
	}

	obj := c.findMatchingVariant(key, "", requestHeaders)
	if obj == nil {
		return &CacheEntry{
			State: CacheState{
				Found:              false,
				Usable:             false,
				Stale:              false,
				MustInsertOrUpdate: false,
			},
		}
	}

	age := time.Since(obj.InsertTime).Nanoseconds()
	if obj.InitialAgeNs > 0 {
		age += int64(obj.InitialAgeNs)
	}

	isStale := uint64(age) > obj.MaxAgeNs
	isUsable := !isStale || (obj.StaleWhileRevalidateNs > 0 && uint64(age) <= obj.MaxAgeNs+obj.StaleWhileRevalidateNs)

	return &CacheEntry{
		Object: obj,
		State: CacheState{
			Found:              true,
			Usable:             isUsable,
			Stale:              isStale,
			MustInsertOrUpdate: false,
		},
	}
}

// TransactionLookup performs a transactional cache lookup with request collapsing support.
// If another transaction is already in progress for the same key, this call blocks until
// that transaction completes, preventing thundering herd on cache misses.
func (c *Cache) TransactionLookup(key []byte, options *CacheLookupOptions) *CacheTransaction {
	keyStr := cacheKey(key)

	c.mu.Lock()

	// Check if there's already a pending transaction for this key
	if tx, exists := c.transactions[keyStr]; exists {
		c.mu.Unlock()
		// Wait for the existing transaction
		<-tx.ready
		return tx
	}

	// Create new transaction
	tx := &CacheTransaction{
		Key:     key,
		Options: options,
		ready:   make(chan struct{}),
	}

	var requestHeaders []byte
	if options != nil {
		requestHeaders = options.RequestHeaders
	}

	// Perform lookup
	obj := c.findMatchingVariant(key, "", requestHeaders)

	if obj == nil {
		tx.Entry = &CacheEntry{
			State: CacheState{
				Found:              false,
				Usable:             false,
				Stale:              false,
				MustInsertOrUpdate: true,
			},
		}
	} else {
		age := time.Since(obj.InsertTime).Nanoseconds()
		if obj.InitialAgeNs > 0 {
			age += int64(obj.InitialAgeNs)
		}

		isStale := uint64(age) > obj.MaxAgeNs
		isUsable := !isStale || (obj.StaleWhileRevalidateNs > 0 && uint64(age) <= obj.MaxAgeNs+obj.StaleWhileRevalidateNs)
		mustUpdate := isStale && !isUsable

		tx.Entry = &CacheEntry{
			Object: obj,
			State: CacheState{
				Found:              true,
				Usable:             isUsable,
				Stale:              isStale,
				MustInsertOrUpdate: mustUpdate,
			},
		}
	}

	// Register transaction (for request collapsing)
	c.transactions[keyStr] = tx
	c.mu.Unlock()

	// Mark as ready immediately (for now, could be async later)
	close(tx.ready)

	return tx
}

// Insert inserts an object into the cache
func (c *Cache) Insert(key []byte, options *CacheWriteOptions) *CachedObject {
	c.mu.Lock()
	defer c.mu.Unlock()

	obj := &CachedObject{
		Body:           &bytes.Buffer{},
		MaxAgeNs:       options.MaxAgeNs,
		VaryRule:       options.VaryRule,
		SurrogateKeys:  options.SurrogateKeys,
		UserMetadata:   options.UserMetadata,
		Length:         options.Length,
		RequestHeaders: options.RequestHeaders,
		InsertTime:     time.Now(),
		HitCount:       0,
		WriteComplete:  false,
		WriteCond:      sync.NewCond(&sync.Mutex{}),
		SensitiveData:  options.SensitiveData,
	}

	if options.InitialAgeNs != nil {
		obj.InitialAgeNs = *options.InitialAgeNs
	}
	if options.StaleWhileRevalidateNs != nil {
		obj.StaleWhileRevalidateNs = *options.StaleWhileRevalidateNs
	}
	if options.EdgeMaxAgeNs != nil {
		obj.EdgeMaxAgeNs = *options.EdgeMaxAgeNs
	}

	keyStr := cacheKey(key)
	c.objects[keyStr] = append(c.objects[keyStr], obj)

	// Index by surrogate keys
	for _, skey := range options.SurrogateKeys {
		c.surrogateIndex[skey] = append(c.surrogateIndex[skey], keyStr)
	}

	return obj
}

// TransactionUpdate updates metadata for an existing cached object
func (c *Cache) TransactionUpdate(tx *CacheTransaction, options *CacheWriteOptions) error {
	if tx.Entry == nil || tx.Entry.Object == nil {
		return fmt.Errorf("no object to update")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	obj := tx.Entry.Object

	// Update metadata
	obj.MaxAgeNs = options.MaxAgeNs
	if options.InitialAgeNs != nil {
		obj.InitialAgeNs = *options.InitialAgeNs
	}
	if options.StaleWhileRevalidateNs != nil {
		obj.StaleWhileRevalidateNs = *options.StaleWhileRevalidateNs
	}
	if options.EdgeMaxAgeNs != nil {
		obj.EdgeMaxAgeNs = *options.EdgeMaxAgeNs
	}
	if options.UserMetadata != nil {
		obj.UserMetadata = options.UserMetadata
	}

	// Reset age
	obj.InsertTime = time.Now()
	obj.InitialAgeNs = 0

	return nil
}

// TransactionCancel cancels a cache transaction
func (c *Cache) TransactionCancel(tx *CacheTransaction) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	keyStr := cacheKey(tx.Key)
	delete(c.transactions, keyStr)

	return nil
}

// CompleteTransaction marks a cache transaction as complete and removes it from the
// pending transactions map, allowing new transactions for the same key to proceed.
func (c *Cache) CompleteTransaction(tx *CacheTransaction) {
	c.mu.Lock()
	defer c.mu.Unlock()

	keyStr := cacheKey(tx.Key)
	delete(c.transactions, keyStr)
}

// PurgeSurrogateKey performs a hard purge of all cache entries tagged with the surrogate key.
// Surrogate keys enable bulk cache invalidation by tagging related cache entries.
// Returns the number of cache keys that were purged.
func (c *Cache) PurgeSurrogateKey(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if keys, ok := c.surrogateIndex[key]; ok {
		count := len(keys)
		for _, k := range keys {
			delete(c.objects, k)
		}
		delete(c.surrogateIndex, key)
		return count
	}
	return 0
}

// SoftPurgeSurrogateKey performs a soft purge by marking entries as stale without removing them.
// Stale entries can still be served with stale-while-revalidate, allowing graceful revalidation.
// Returns the number of cached objects that were marked stale.
func (c *Cache) SoftPurgeSurrogateKey(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	count := 0
	if keys, ok := c.surrogateIndex[key]; ok {
		for _, k := range keys {
			if variants, ok := c.objects[k]; ok {
				for _, obj := range variants {
					// Set to already expired
					obj.InitialAgeNs = obj.MaxAgeNs + 1
					obj.InsertTime = time.Now().Add(-time.Duration(obj.MaxAgeNs+1) * time.Nanosecond)
					count++
				}
			}
		}
	}
	return count
}

// GetAge returns the age of a cached object in nanoseconds
func (obj *CachedObject) GetAge() uint64 {
	age := uint64(time.Since(obj.InsertTime).Nanoseconds())
	if obj.InitialAgeNs > 0 {
		age += obj.InitialAgeNs
	}
	return age
}

// ReadBody reads from the cached body at the specified offset.
// For streaming cache writes, this will block until data becomes available at the requested offset.
// This enables concurrent readers during cache insertion.
func (obj *CachedObject) ReadBody(p []byte, offset int64) (int, error) {
	obj.WriteCond.L.Lock()
	defer obj.WriteCond.L.Unlock()

	for {
		// Check if we have data available
		if offset < int64(obj.Body.Len()) {
			// Read available data
			data := obj.Body.Bytes()[offset:]
			n := copy(p, data)
			return n, nil
		}

		// If write is complete and no more data, return EOF
		if obj.WriteComplete {
			return 0, io.EOF
		}

		// Wait for more data
		obj.WriteCond.Wait()
	}
}

// WriteBody appends data to the cached body and notifies waiting readers.
// This enables streaming cache insertion with concurrent reads.
func (obj *CachedObject) WriteBody(p []byte) (int, error) {
	obj.WriteCond.L.Lock()
	n, err := obj.Body.Write(p)
	obj.WriteCond.L.Unlock()
	obj.WriteCond.Broadcast() // wake up waiting readers
	return n, err
}

// FinishWrite marks the write as complete and wakes all waiting readers
func (obj *CachedObject) FinishWrite() {
	obj.WriteCond.L.Lock()
	obj.WriteComplete = true
	obj.WriteCond.L.Unlock()
	obj.WriteCond.Broadcast()
}

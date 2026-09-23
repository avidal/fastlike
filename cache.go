package fastlike

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

// CachedObject represents a cached entry with metadata and body
type CachedObject struct {
	Body                   *bytes.Buffer
	MaxAgeNs               uint64
	InitialAgeNs           uint64
	StaleWhileRevalidateNs uint64
	StaleIfErrorNs         uint64
	EdgeMaxAgeNs           uint64
	VaryRule               string
	SurrogateKeys          []string
	UserMetadata           []byte
	Length                 *uint64 // nil if unknown
	RequestHeaders         []byte  // serialized headers used for vary
	InsertTime             time.Time
	HitCount               atomic.Uint64
	WriteComplete          bool
	WriteFailed            bool       // the writer gave up before finishing
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
	ready          chan struct{} // closed when lookup completes
	owner          any
	done           chan struct{} // closed once the transaction is completed or cancelled
	finished       bool
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
	StaleIfErrorNs         *uint64
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

// CacheReplace is a replace operation in progress.
// It exposes the object found under the key until the replacement is provided
// or the operation is abandoned.
type CacheReplace struct {
	Key      []byte
	Existing *CachedObject
	Options  *CacheReplaceOptions
	owner    any
	done     chan struct{}
	finished bool
}

// State reports the existing object as the replace accessors expose it.
// Found and usable always go together here because early SDKs inferred
// usability from the found bit alone.
func (r *CacheReplace) State() CacheState {
	if r.Existing == nil {
		return CacheState{}
	}
	return CacheState{
		Found:  true,
		Usable: true,
		Stale:  r.Existing.GetAge() > r.Existing.MaxAgeNs,
	}
}

// Cache is an in-memory cache with request collapsing support
type Cache struct {
	mu             sync.RWMutex
	objects        map[string][]*CachedObject   // key -> variants (for vary support)
	transactions   map[string]*CacheTransaction // key -> pending transaction
	replaces       map[string][]*CacheReplace   // key -> pending replaces
	surrogateIndex map[string][]string          // surrogate_key -> cache_keys
}

// NewCache creates a new cache instance
func NewCache() *Cache {
	return &Cache{
		objects:        make(map[string][]*CachedObject),
		transactions:   make(map[string]*CacheTransaction),
		replaces:       make(map[string][]*CacheReplace),
		surrogateIndex: make(map[string][]string),
	}
}

// cacheKey converts a byte slice key into a string suitable for use as a Go map key.
func cacheKey(key []byte) string {
	return string(key)
}

// extractVaryHeaders extracts only the headers named in the vary rule from serialized request headers.
// The ABI separates the header names with spaces; commas are accepted as well.
// The request headers are in HTTP wire format as produced by http.Header.Write().
// Returns a normalized representation suitable for comparison.
func extractVaryHeaders(varyRule string, requestHeaders []byte) []byte {
	if varyRule == "" || len(requestHeaders) == 0 {
		return nil
	}

	varyHeaders := make(map[string]bool)
	for _, name := range strings.FieldsFunc(varyRule, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		varyHeaders[strings.ToLower(name)] = true
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

// findMatchingVariant returns the most recently inserted variant of key whose
// vary rule is satisfied by requestHeaders.
// Each stored variant decides for itself which request headers have to match,
// so a variant without a vary rule matches any request.
func (c *Cache) findMatchingVariant(key []byte, requestHeaders []byte) *CachedObject {
	var newest *CachedObject
	for _, v := range c.objects[cacheKey(key)] {
		if v.VaryRule != "" && !bytes.Equal(extractVaryHeaders(v.VaryRule, requestHeaders), extractVaryHeaders(v.VaryRule, v.RequestHeaders)) {
			continue
		}
		if newest == nil || v.InsertTime.After(newest.InsertTime) {
			newest = v
		}
	}
	return newest
}

// Lookup performs a non-transactional cache lookup
func (c *Cache) Lookup(key []byte, options *CacheLookupOptions) *CacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var requestHeaders []byte
	if options != nil {
		requestHeaders = options.RequestHeaders
	}

	obj := c.findMatchingVariant(key, requestHeaders)
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
	obj.HitCount.Add(1)

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
// A lookup that finds nothing usable while another owner has a replace or a
// transaction pending waits for that work to finish and then looks again,
// instead of fetching on its own.
// The caller's own pending transaction is joined rather than waited for.
func (c *Cache) TransactionLookup(key []byte, options *CacheLookupOptions, owner any) *CacheTransaction {
	keyStr := cacheKey(key)

	var requestHeaders []byte
	if options != nil {
		requestHeaders = options.RequestHeaders
	}

	c.mu.Lock()

	var obj *CachedObject
	for {
		obj = c.findMatchingVariant(key, requestHeaders)
		usable := obj != nil && obj.usable()

		var wait chan struct{}
		if pending := c.pendingReplaceFrom(keyStr, owner); pending != nil && !usable {
			wait = pending.done
		} else if pending := c.pendingTransactionFrom(keyStr, owner); pending != nil && !usable {
			wait = pending.done
		} else if own := c.transactions[keyStr]; own != nil && own.owner == owner {
			c.mu.Unlock()
			<-own.ready
			return own
		}
		if wait == nil {
			break
		}
		c.mu.Unlock()
		<-wait
		c.mu.Lock()
	}

	// Create new transaction
	tx := &CacheTransaction{
		Key:     key,
		Options: options,
		owner:   owner,
		ready:   make(chan struct{}),
		done:    make(chan struct{}),
	}

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
		obj.HitCount.Add(1)

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

	// Register transaction (for request collapsing) unless another owner
	// is already fetching and we were served a usable object meanwhile.
	if c.transactions[keyStr] == nil {
		c.transactions[keyStr] = tx
	}
	c.mu.Unlock()

	// Mark as ready immediately (for now, could be async later)
	close(tx.ready)

	return tx
}

// Insert inserts an object into the cache
func (c *Cache) Insert(key []byte, options *CacheWriteOptions) *CachedObject {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.store(cacheKey(key), options)
}

// store files a new object under keyStr.
// The caller holds the lock.
func (c *Cache) store(keyStr string, options *CacheWriteOptions) *CachedObject {
	obj := &CachedObject{
		Body:           &bytes.Buffer{},
		MaxAgeNs:       options.MaxAgeNs,
		VaryRule:       options.VaryRule,
		SurrogateKeys:  options.SurrogateKeys,
		UserMetadata:   options.UserMetadata,
		Length:         options.Length,
		RequestHeaders: options.RequestHeaders,
		InsertTime:     time.Now(),
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
	if options.StaleIfErrorNs != nil {
		obj.StaleIfErrorNs = *options.StaleIfErrorNs
	}

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
	if options.StaleIfErrorNs != nil {
		obj.StaleIfErrorNs = *options.StaleIfErrorNs
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

	c.finishTransaction(tx)

	return nil
}

// CompleteTransaction marks a cache transaction as complete and removes it from the
// pending transactions map, allowing new transactions for the same key to proceed.
func (c *Cache) CompleteTransaction(tx *CacheTransaction) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.finishTransaction(tx)
}

// AbandonTransactions completes every pending transaction started by owner.
// Transactions whose handles were closed without an insert or a cancel would
// otherwise keep their key pending forever.
func (c *Cache) AbandonTransactions(owner any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, tx := range c.transactions {
		if tx.owner == owner {
			c.finishTransaction(tx)
		}
	}
}

// finishTransaction wakes anyone waiting on tx and unregisters it.
// The caller holds the lock.
func (c *Cache) finishTransaction(tx *CacheTransaction) {
	keyStr := cacheKey(tx.Key)
	if c.transactions[keyStr] == tx {
		delete(c.transactions, keyStr)
	}
	if tx.finished || tx.done == nil {
		return
	}
	tx.finished = true
	close(tx.done)
}

// pendingTransactionFrom returns a pending transaction on keyStr started by
// another owner, or nil.
// The caller holds the lock.
func (c *Cache) pendingTransactionFrom(keyStr string, owner any) *CacheTransaction {
	tx := c.transactions[keyStr]
	if tx == nil || tx.owner == owner || tx.done == nil {
		return nil
	}
	return tx
}

// TransactionChooseStale resolves a cache transaction with a stale object if one is available
// within the stale-if-error window. Returns true if a stale object was chosen.
func (c *Cache) TransactionChooseStale(tx *CacheTransaction) bool {
	if tx.Entry == nil || tx.Entry.Object == nil {
		return false
	}

	obj := tx.Entry.Object
	if obj.StaleIfErrorNs == 0 {
		return false
	}

	age := obj.GetAge()
	// Object must be stale (past max_age) but within the stale-if-error window
	if age > obj.MaxAgeNs && age <= obj.MaxAgeNs+obj.StaleIfErrorNs {
		// Resolve the transaction with the stale object
		tx.Entry.State.MustInsertOrUpdate = false
		tx.Entry.State.Usable = true
		return true
	}

	return false
}

// Replace starts a replace operation for key on behalf of owner.
// The existing object is exposed whatever its age; the force miss strategy also
// drops it from the cache right away.
// The wait strategy queues behind pending replaces and transactional inserts,
// but only those of other owners, since a guest runs one hostcall at a time
// and could never resolve its own.
func (c *Cache) Replace(key []byte, options *CacheReplaceOptions, owner any) *CacheReplace {
	keyStr := cacheKey(key)

	c.mu.Lock()
	if options.ReplaceStrategy == CacheReplaceWait {
		for {
			var wait chan struct{}
			if pending := c.pendingReplaceFrom(keyStr, owner); pending != nil {
				wait = pending.done
			} else if pending := c.pendingTransactionFrom(keyStr, owner); pending != nil {
				wait = pending.done
			} else {
				break
			}
			c.mu.Unlock()
			<-wait
			c.mu.Lock()
		}
	}

	existing := c.findMatchingVariant(key, options.RequestHeaders)
	if existing != nil && options.ReplaceStrategy == CacheReplaceImmediateForceMiss {
		c.removeObject(keyStr, existing)
	}

	r := &CacheReplace{
		Key:      key,
		Existing: existing,
		Options:  options,
		owner:    owner,
		done:     make(chan struct{}),
	}
	c.replaces[keyStr] = append(c.replaces[keyStr], r)
	c.mu.Unlock()

	return r
}

// pendingReplaceFrom returns a pending replace on keyStr started by another
// owner, or nil.
// The caller holds the lock.
func (c *Cache) pendingReplaceFrom(keyStr string, owner any) *CacheReplace {
	for _, pending := range c.replaces[keyStr] {
		if pending.owner != owner {
			return pending
		}
	}
	return nil
}

// ReplaceInsert stores the replacement for r and drops the object it replaces.
func (c *Cache) ReplaceInsert(r *CacheReplace, options *CacheWriteOptions) *CachedObject {
	c.mu.Lock()
	defer c.mu.Unlock()

	keyStr := cacheKey(r.Key)
	if r.Existing != nil {
		c.removeObject(keyStr, r.Existing)
	}
	obj := c.store(keyStr, options)
	c.finishReplace(keyStr, r)

	return obj
}

// Discard drops an object whose write was abandoned and fails its readers.
func (c *Cache) Discard(key []byte, obj *CachedObject) {
	c.mu.Lock()
	c.removeObject(cacheKey(key), obj)
	c.mu.Unlock()

	obj.AbortWrite()
}

// ReplaceAbandon ends r without providing a replacement.
// An object dropped by the ImmediateForceMiss strategy stays gone.
func (c *Cache) ReplaceAbandon(r *CacheReplace) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.finishReplace(cacheKey(r.Key), r)
}

// finishReplace wakes anyone waiting on r and unregisters it.
// The caller holds the lock.
func (c *Cache) finishReplace(keyStr string, r *CacheReplace) {
	if r.finished {
		return
	}
	r.finished = true
	close(r.done)
	pending := c.replaces[keyStr]
	if idx := slices.Index(pending, r); idx >= 0 {
		pending = slices.Delete(pending, idx, idx+1)
	}
	if len(pending) == 0 {
		delete(c.replaces, keyStr)
		return
	}
	c.replaces[keyStr] = pending
}

// removeObject drops one variant of keyStr and its surrogate index entries.
// The caller holds the lock.
func (c *Cache) removeObject(keyStr string, obj *CachedObject) {
	variants := c.objects[keyStr]
	idx := slices.Index(variants, obj)
	if idx < 0 {
		return
	}
	variants = slices.Delete(variants, idx, idx+1)
	if len(variants) == 0 {
		delete(c.objects, keyStr)
	} else {
		c.objects[keyStr] = variants
	}

	for _, skey := range obj.SurrogateKeys {
		keys := c.surrogateIndex[skey]
		if at := slices.Index(keys, keyStr); at >= 0 {
			keys = slices.Delete(keys, at, at+1)
		}
		if len(keys) == 0 {
			delete(c.surrogateIndex, skey)
		} else {
			c.surrogateIndex[skey] = keys
		}
	}
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

// usable reports whether the object is fresh or within its stale-while-revalidate window.
func (obj *CachedObject) usable() bool {
	age := obj.GetAge()
	return age <= obj.MaxAgeNs || (obj.StaleWhileRevalidateNs > 0 && age <= obj.MaxAgeNs+obj.StaleWhileRevalidateNs)
}

// KnownLength reports the object size once the writer announced it or finished
// streaming.
func (obj *CachedObject) KnownLength() (int64, bool) {
	if obj.Length != nil && *obj.Length <= math.MaxInt64 {
		return int64(*obj.Length), true
	}

	obj.WriteCond.L.Lock()
	defer obj.WriteCond.L.Unlock()

	if obj.WriteComplete {
		return int64(obj.Body.Len()), true
	}
	return 0, false
}

// ReadBody reads from the cached body at the specified offset.
// For streaming cache writes, this will block until data becomes available at the requested offset.
// This enables concurrent readers during cache insertion.
func (obj *CachedObject) ReadBody(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, io.ErrUnexpectedEOF
	}

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
			if obj.WriteFailed {
				return 0, io.ErrUnexpectedEOF
			}
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

// AbortWrite ends an abandoned write so that waiting readers fail instead of
// treating the bytes written so far as the whole object.
func (obj *CachedObject) AbortWrite() {
	obj.WriteCond.L.Lock()
	obj.WriteComplete = true
	obj.WriteFailed = true
	obj.WriteCond.L.Unlock()
	obj.WriteCond.Broadcast()
}

// waitForWriteComplete blocks until the writer is done and returns the final
// size along with whether the writer gave up.
func (obj *CachedObject) waitForWriteComplete() (int64, bool) {
	obj.WriteCond.L.Lock()
	defer obj.WriteCond.L.Unlock()

	for !obj.WriteComplete {
		obj.WriteCond.Wait()
	}
	return int64(obj.Body.Len()), obj.WriteFailed
}

func (obj *CachedObject) writeFailed() bool {
	obj.WriteCond.L.Lock()
	defer obj.WriteCond.L.Unlock()

	return obj.WriteFailed
}

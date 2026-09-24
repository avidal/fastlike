package fastlike

import (
	"bytes"
	"errors"
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
	VaryHeaders            []byte  // the inserting request's values for VaryRule
	InsertTime             time.Time
	HitCount               atomic.Uint64
	WriteComplete          bool
	WriteFailed            bool       // the writer gave up before finishing
	WriteCond              *sync.Cond // for streaming concurrent reads
	SensitiveData          bool       // whether data is sensitive (PCI, etc)

	// Response is the head of HTTP cache objects.
	// Updates replace it, so handles keep the head they found.
	Response *storedResponse

	// When a revalidation was last offered, as a cacheInstant.
	lastRevalidation atomic.Int64

	// When the object was first soft purged, as a cacheInstant.
	softPurgedAt atomic.Int64
}

// cachePeriod mirrors CacheD's stale status.
type cachePeriod int

const (
	periodFresh        cachePeriod = iota
	periodStale                    // within stale-while-revalidate
	periodStaleIfError             // within stale-if-error
	periodExpired
)

// revalidationInterval is CacheD's delay between two revalidation offers.
const revalidationInterval = time.Second

var cacheClockBase = time.Now()

// cacheInstant turns t into monotonic nanoseconds, where 0 means never.
func cacheInstant(t time.Time) int64 {
	return int64(t.Sub(cacheClockBase)) + 1
}

// CacheState represents the state flags for a cache lookup, as CacheD
// reports them.
type CacheState struct {
	Found              bool
	Usable             bool
	Stale              bool
	MustInsertOrUpdate bool
	UsableIfError      bool // within the stale-if-error period
	RevalidationFailed bool // served stale after a failed revalidation
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

func (o *CacheLookupOptions) requestHeaders() []byte {
	if o == nil {
		return nil
	}
	return o.RequestHeaders
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
	Response               *storedResponse
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
		Stale:  r.Existing.periodAt(time.Now()) != periodFresh,
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

	// Repeated values keep their order, as in production.
	var result bytes.Buffer
	for _, name := range sortedNames {
		for _, v := range extracted[name] {
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
		if v.VaryRule != "" && !bytes.Equal(extractVaryHeaders(v.VaryRule, requestHeaders), v.VaryHeaders) {
			continue
		}
		if newest == nil || v.InsertTime.After(newest.InsertTime) {
			newest = v
		}
	}
	return newest
}

// settledTransaction wraps an entry for a handle that is not part of a
// pending transaction.
func settledTransaction(key []byte, entry *CacheEntry, options *CacheLookupOptions) *CacheTransaction {
	tx := &CacheTransaction{Key: key, Entry: entry, Options: options, ready: make(chan struct{})}
	close(tx.ready)
	return tx
}

// holdsObligation tells whether tx still owes the cache an object.
func (tx *CacheTransaction) holdsObligation() bool {
	return tx.Entry != nil && tx.Entry.State.MustInsertOrUpdate
}

// usableTransaction serves obj whatever its age, as production does for the
// handles returned by stream-back inserts and fresh updates.
func usableTransaction(key []byte, obj *CachedObject) *CacheTransaction {
	return settledTransaction(key, &CacheEntry{Object: obj, State: CacheState{Found: true, Usable: true}}, nil)
}

// Lookup performs a non-transactional cache lookup, which only finds usable
// objects.
func (c *Cache) Lookup(key []byte, options *CacheLookupOptions) *CacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now()
	obj, period := c.findVariantPeriod(key, options.requestHeaders(), now)
	if period == periodExpired {
		return &CacheEntry{}
	}
	state, _ := obj.hit(period, now)
	return &CacheEntry{Object: obj, State: state}
}

// findVariantPeriod treats a missing variant as expired.
func (c *Cache) findVariantPeriod(key, requestHeaders []byte, now time.Time) (*CachedObject, cachePeriod) {
	obj := c.findMatchingVariant(key, requestHeaders)
	if obj == nil {
		return nil, periodExpired
	}
	return obj, obj.periodAt(now)
}

// TransactionLookup performs a transactional cache lookup with request collapsing support.
// A lookup that finds nothing usable while another owner has a replace or a
// transaction pending waits for that work to finish and then looks again,
// instead of fetching on its own.
// The caller's own pending transaction is joined rather than waited for.
func (c *Cache) TransactionLookup(key []byte, options *CacheLookupOptions, owner any) *CacheTransaction {
	keyStr := cacheKey(key)

	c.mu.Lock()

	var obj *CachedObject
	var now time.Time
	var period cachePeriod
	for {
		now = time.Now()
		obj, period = c.findVariantPeriod(key, options.requestHeaders(), now)
		if period != periodExpired {
			break
		}

		wait, _ := c.pendingWorkFrom(keyStr, owner)
		if wait == nil {
			if own := c.transactions[keyStr]; own != nil && own.owner == owner {
				c.mu.Unlock()
				<-own.ready
				return own
			}
			break
		}
		c.mu.Unlock()
		<-wait
		c.mu.Lock()
	}

	tx := &CacheTransaction{
		Key:     key,
		Options: options,
		owner:   owner,
		ready:   make(chan struct{}),
		done:    make(chan struct{}),
	}

	if period == periodExpired {
		// The expired object is kept for its metadata, which revalidation needs.
		tx.Entry = &CacheEntry{Object: obj, State: CacheState{MustInsertOrUpdate: true}}
	} else {
		state, offered := obj.hit(period, now)
		// Nobody revalidates while the key is busy.
		state.MustInsertOrUpdate = offered && c.transactions[keyStr] == nil && len(c.replaces[keyStr]) == 0
		tx.Entry = &CacheEntry{Object: obj, State: state}
	}
	if tx.Entry.State.MustInsertOrUpdate {
		c.transactions[keyStr] = tx
	}
	c.mu.Unlock()

	// Mark as ready immediately (for now, could be async later)
	close(tx.ready)

	return tx
}

// TransactionRefresh waits for the pending revalidation of the stale object
// tx found, like production's transaction_refresh.
// A guest waiting for its own revalidation joins it instead.
func (c *Cache) TransactionRefresh(tx *CacheTransaction) *CacheTransaction {
	keyStr := cacheKey(tx.Key)

	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		now := time.Now()
		if obj, period := c.findVariantPeriod(tx.Key, tx.Options.requestHeaders(), now); period == periodFresh {
			state, _ := obj.hit(period, now)
			tx.Entry = &CacheEntry{Object: obj, State: state}
			return tx
		}

		wait, leader := c.pendingWorkFrom(keyStr, tx.owner)
		if wait == nil {
			if own := c.transactions[keyStr]; own != nil && own.owner == tx.owner {
				return own
			}
			tx.Entry.State.MustInsertOrUpdate = true
			c.transactions[keyStr] = tx
			return tx
		}
		c.mu.Unlock()
		<-wait
		c.mu.Lock()

		// CacheD only cancels the waiters of the same variant.
		if leader != nil && leader.Entry.State.RevalidationFailed && sameVariant(leader.Entry.Object, tx.Entry.Object) {
			tx.Entry.State.RevalidationFailed = true
			return tx
		}
	}
}

// sameVariant tells whether a and b answer the same requests.
func sameVariant(a, b *CachedObject) bool {
	return a.VaryRule == b.VaryRule && bytes.Equal(a.VaryHeaders, b.VaryHeaders)
}

// Insert inserts an object into the cache
func (c *Cache) Insert(key []byte, options *CacheWriteOptions) *CachedObject {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.store(cacheKey(key), options)
}

var errNoObligation = errors.New("the transaction does not owe the cache an object")

// TransactionInsert stores a new object for tx, which uses up its obligation.
func (c *Cache) TransactionInsert(tx *CacheTransaction, options *CacheWriteOptions) (*CachedObject, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !tx.holdsObligation() {
		return nil, errNoObligation
	}
	obj := c.store(cacheKey(tx.Key), options)
	c.takeObligation(tx)
	return obj, nil
}

// store files a new object under keyStr.
// The caller holds the lock.
func (c *Cache) store(keyStr string, options *CacheWriteOptions) *CachedObject {
	obj := &CachedObject{
		Body:          &bytes.Buffer{},
		MaxAgeNs:      options.MaxAgeNs,
		VaryRule:      options.VaryRule,
		SurrogateKeys: options.SurrogateKeys,
		UserMetadata:  options.UserMetadata,
		Length:        options.Length,
		VaryHeaders:   extractVaryHeaders(options.VaryRule, options.RequestHeaders),
		InsertTime:    time.Now(),
		WriteComplete: false,
		WriteCond:     sync.NewCond(&sync.Mutex{}),
		SensitiveData: options.SensitiveData,
		Response:      options.Response,
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

// TransactionUpdate freshens the object tx found, which uses up its
// obligation, and returns it.
func (c *Cache) TransactionUpdate(tx *CacheTransaction, options *CacheWriteOptions) (*CachedObject, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !tx.holdsObligation() {
		return nil, errNoObligation
	}
	obj := tx.Entry.Object
	if obj == nil {
		return nil, errors.New("no object to update")
	}

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
	if options.Response != nil {
		obj.Response = options.Response
	}

	// Reset age
	obj.InsertTime = time.Now()
	obj.InitialAgeNs = 0
	obj.softPurgedAt.Store(0)

	c.takeObligation(tx)
	return obj, nil
}

// TransactionCancel gives up the obligation of a transaction, and reports
// whether it had one.
func (c *Cache) TransactionCancel(tx *CacheTransaction) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.takeObligation(tx)
}

// takeObligation ends the obligation of tx like production's take_go_get,
// and reports whether it had one.
// The caller holds the lock.
func (c *Cache) takeObligation(tx *CacheTransaction) bool {
	c.finishTransaction(tx)
	if !tx.holdsObligation() {
		return false
	}
	tx.Entry.State.MustInsertOrUpdate = false
	if !tx.Entry.State.Found {
		tx.Entry.Object = nil
	}
	return true
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

// pendingWorkFrom returns what a lookup by owner must wait for, and the
// transaction behind it if any.
// The caller holds the lock.
func (c *Cache) pendingWorkFrom(keyStr string, owner any) (<-chan struct{}, *CacheTransaction) {
	if pending := c.pendingReplaceFrom(keyStr, owner); pending != nil {
		return pending.done, nil
	}
	if pending := c.pendingTransactionFrom(keyStr, owner); pending != nil {
		return pending.done, pending
	}
	return nil, nil
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

// TransactionChooseStale serves the found object instead of revalidating it,
// to the lookups waiting for that revalidation too, and reports whether tx
// held the obligation for a found object.
func (c *Cache) TransactionChooseStale(tx *CacheTransaction) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !tx.holdsObligation() || !tx.Entry.State.Found {
		return false
	}
	tx.Entry.State.RevalidationFailed = true
	c.takeObligation(tx)

	return true
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
			wait, _ := c.pendingWorkFrom(keyStr, owner)
			if wait == nil {
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

	now := cacheInstant(time.Now())
	count := 0
	if keys, ok := c.surrogateIndex[key]; ok {
		for _, k := range keys {
			if variants, ok := c.objects[k]; ok {
				for _, obj := range variants {
					// CacheD keeps the earliest soft purge.
					obj.softPurgedAt.CompareAndSwap(0, now)
					count++
				}
			}
		}
	}
	return count
}

// GetAge returns the age of a cached object in nanoseconds
func (obj *CachedObject) GetAge() uint64 {
	return obj.ageAt(time.Now())
}

func (obj *CachedObject) ageAt(now time.Time) uint64 {
	return uint64(now.Sub(obj.InsertTime).Nanoseconds()) + obj.InitialAgeNs
}

// periodAt follows CacheD, soft purges included.
func (obj *CachedObject) periodAt(now time.Time) cachePeriod {
	age := obj.ageAt(now)
	fresh := age < obj.MaxAgeNs
	var staleFor uint64
	if !fresh {
		staleFor = age - obj.MaxAgeNs
	}
	if purged := obj.softPurgedAt.Load(); purged != 0 {
		if sincePurge := uint64(max(cacheInstant(now)-purged, 0)); fresh || sincePurge > staleFor {
			staleFor = sincePurge
		}
		fresh = false
	}
	switch {
	case fresh:
		return periodFresh
	case staleFor < obj.StaleWhileRevalidateNs:
		return periodStale
	case staleFor < obj.StaleIfErrorNs:
		return periodStaleIfError
	default:
		return periodExpired
	}
}

// hit counts a lookup of a usable object and reports whether it gets the
// revalidation offer, which plain lookups use up too, as in CacheD.
func (obj *CachedObject) hit(period cachePeriod, now time.Time) (CacheState, bool) {
	obj.HitCount.Add(1)
	stale := period != periodFresh
	state := CacheState{Found: true, Usable: true, Stale: stale, UsableIfError: period == periodStaleIfError}
	return state, stale && obj.offerRevalidation(now)
}

// offerRevalidation applies CacheD's revalidation throttle.
func (obj *CachedObject) offerRevalidation(now time.Time) bool {
	if obj.streaming() {
		return false
	}
	at := cacheInstant(now)
	for {
		last := obj.lastRevalidation.Load()
		if last != 0 && at-last < int64(revalidationInterval) {
			return false
		}
		if obj.lastRevalidation.CompareAndSwap(last, at) {
			return true
		}
	}
}

func (obj *CachedObject) streaming() bool {
	obj.WriteCond.L.Lock()
	defer obj.WriteCond.L.Unlock()

	return !obj.WriteComplete
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

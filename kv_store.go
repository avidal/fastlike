package fastlike

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ObjectValue represents a value stored in the KV store
type ObjectValue struct {
	Body       []byte     // The actual value bytes
	Metadata   string     // Optional metadata string
	Generation uint64     // Generation number (nanoseconds since epoch)
	Expiration *time.Time // Optional expiration time (nil = never expires)
}

// IsExpired checks if the value has expired
func (ov *ObjectValue) IsExpired() bool {
	if ov.Expiration == nil {
		return false
	}
	return time.Now().After(*ov.Expiration)
}

// KVStore represents a key-value store
type KVStore struct {
	name    string
	mu      sync.RWMutex
	objects map[string]*ObjectValue
}

// NewKVStore creates a new KV store with the given name
func NewKVStore(name string) *KVStore {
	return &KVStore{
		name:    name,
		objects: make(map[string]*ObjectValue),
	}
}

// ValidateKey checks if a key meets Fastly's validation rules
func ValidateKey(key string) error {
	if len(key) == 0 || len(key) > 1024 {
		return fmt.Errorf("key length must be 1-1024 bytes")
	}

	// Cannot contain certain characters
	forbidden := []string{"\r", "\n", "#", ";", "?", "^", "|"}
	for _, char := range forbidden {
		if strings.Contains(key, char) {
			return fmt.Errorf("key cannot contain %q", char)
		}
	}

	// Cannot start with .well-known/acme-challenge/
	if strings.HasPrefix(key, ".well-known/acme-challenge/") {
		return fmt.Errorf("key cannot start with .well-known/acme-challenge/")
	}

	// Cannot be . or ..
	if key == "." || key == ".." {
		return fmt.Errorf("key cannot be \".\" or \"..\"")
	}

	// Cannot be single forbidden characters
	if len(key) == 1 {
		r := []rune(key)[0]
		if r <= 0x20 || r == 0xFFFE || r == 0xFFFF {
			return fmt.Errorf("invalid single-character key")
		}
	}

	return nil
}

// Lookup retrieves a value from the store
func (kvs *KVStore) Lookup(key string) (*ObjectValue, error) {
	kvs.mu.RLock()
	defer kvs.mu.RUnlock()

	obj, exists := kvs.objects[key]
	if !exists {
		return nil, nil // Key not found
	}

	// Check if expired
	if obj.IsExpired() {
		// Remove expired entry (upgrade to write lock)
		kvs.mu.RUnlock()
		kvs.mu.Lock()
		delete(kvs.objects, key)
		kvs.mu.Unlock()
		kvs.mu.RLock()
		return nil, nil
	}

	return obj, nil
}

// InsertMode represents the mode for insert operations
type InsertMode uint32

const (
	InsertModeOverwrite InsertMode = 0
	InsertModeAdd       InsertMode = 1
	InsertModeAppend    InsertMode = 2
	InsertModePrepend   InsertMode = 3
)

// Insert stores a value in the store
func (kvs *KVStore) Insert(key string, value []byte, metadata string, ttl *time.Duration, mode InsertMode, ifGenerationMatch *uint64) (uint64, error) {
	if err := ValidateKey(key); err != nil {
		return 0, err
	}

	kvs.mu.Lock()
	defer kvs.mu.Unlock()

	existing, exists := kvs.objects[key]

	// Handle if_generation_match
	if ifGenerationMatch != nil {
		if !exists {
			return 0, fmt.Errorf("precondition failed: key does not exist")
		}
		if existing.Generation != *ifGenerationMatch {
			return 0, fmt.Errorf("precondition failed: generation mismatch")
		}
	}

	// Handle insert modes
	var finalValue []byte
	switch mode {
	case InsertModeOverwrite:
		finalValue = value
	case InsertModeAdd:
		if exists && !existing.IsExpired() {
			return 0, fmt.Errorf("key already exists")
		}
		finalValue = value
	case InsertModeAppend:
		if exists && !existing.IsExpired() {
			finalValue = append(existing.Body, value...)
		} else {
			finalValue = value
		}
	case InsertModePrepend:
		if exists && !existing.IsExpired() {
			finalValue = append(value, existing.Body...)
		} else {
			finalValue = value
		}
	default:
		return 0, fmt.Errorf("invalid insert mode")
	}

	// Generate new generation number (nanoseconds since epoch)
	generation := uint64(time.Now().UnixNano())

	// Calculate expiration time
	var expiration *time.Time
	if ttl != nil {
		exp := time.Now().Add(*ttl)
		expiration = &exp
	}

	// Store the value
	kvs.objects[key] = &ObjectValue{
		Body:       finalValue,
		Metadata:   metadata,
		Generation: generation,
		Expiration: expiration,
	}

	return generation, nil
}

// Delete removes a key from the store
func (kvs *KVStore) Delete(key string) error {
	kvs.mu.Lock()
	defer kvs.mu.Unlock()

	delete(kvs.objects, key)
	return nil
}

// ListResult represents the result of a list operation
type ListResult struct {
	Data []string       `json:"data"`
	Meta ListMetaResult `json:"meta"`
}

// ListMetaResult represents metadata about a list operation
type ListMetaResult struct {
	Limit      uint32  `json:"limit"`
	Prefix     string  `json:"prefix,omitempty"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

// List returns a list of keys in the store
func (kvs *KVStore) List(prefix string, limit uint32, cursor *string) (*ListResult, error) {
	kvs.mu.RLock()
	defer kvs.mu.RUnlock()

	// Collect all non-expired keys matching prefix
	var keys []string
	for key, obj := range kvs.objects {
		// Skip expired
		if obj.IsExpired() {
			continue
		}

		// Filter by prefix
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			continue
		}

		keys = append(keys, key)
	}

	// Sort keys for consistent pagination
	sort.Strings(keys)

	// Handle cursor-based pagination
	startIdx := 0
	if cursor != nil && *cursor != "" {
		// Decode cursor (base64 encoded key)
		cursorKey, err := base64.StdEncoding.DecodeString(*cursor)
		if err != nil {
			return nil, fmt.Errorf("invalid cursor")
		}

		// Find the starting position
		for i, key := range keys {
			if key > string(cursorKey) {
				startIdx = i
				break
			}
		}
	}

	// Apply limit
	if limit == 0 {
		limit = 100 // Default limit
	}

	endIdx := startIdx + int(limit)
	if endIdx > len(keys) {
		endIdx = len(keys)
	}

	resultKeys := keys[startIdx:endIdx]

	// Generate next cursor if there are more results
	var nextCursor *string
	if endIdx < len(keys) {
		cursorStr := base64.StdEncoding.EncodeToString([]byte(keys[endIdx-1]))
		nextCursor = &cursorStr
	}

	// Build result
	result := &ListResult{
		Data: resultKeys,
		Meta: ListMetaResult{
			Limit:      limit,
			Prefix:     prefix,
			NextCursor: nextCursor,
		},
	}

	return result, nil
}

// KVStoreHandle represents a handle to an opened KV store
type KVStoreHandle struct {
	Store *KVStore
}

// KVStoreHandles manages KV store handles
type KVStoreHandles struct {
	handles []*KVStoreHandle
}

// Get returns the KVStoreHandle identified by id or nil if one does not exist
func (kvsh *KVStoreHandles) Get(id int) *KVStoreHandle {
	if id < 0 || id >= len(kvsh.handles) {
		return nil
	}
	return kvsh.handles[id]
}

// New creates a new KVStoreHandle and returns its handle id
func (kvsh *KVStoreHandles) New(store *KVStore) int {
	handle := &KVStoreHandle{Store: store}
	kvsh.handles = append(kvsh.handles, handle)
	return len(kvsh.handles) - 1
}

// KVStoreLookupHandle represents a pending lookup operation
type KVStoreLookupHandle struct {
	done   chan struct{}
	result *ObjectValue
	err    error
}

// Wait blocks until the lookup completes
func (h *KVStoreLookupHandle) Wait() (*ObjectValue, error) {
	<-h.done
	return h.result, h.err
}

// Complete marks the lookup as complete
func (h *KVStoreLookupHandle) Complete(result *ObjectValue, err error) {
	h.result = result
	h.err = err
	close(h.done)
}

// KVStoreLookupHandles manages pending lookup operations
type KVStoreLookupHandles struct {
	handles []*KVStoreLookupHandle
}

// Get returns the handle identified by id or nil
func (h *KVStoreLookupHandles) Get(id int) *KVStoreLookupHandle {
	if id < 0 || id >= len(h.handles) {
		return nil
	}
	return h.handles[id]
}

// New creates a new pending lookup handle
func (h *KVStoreLookupHandles) New() (int, *KVStoreLookupHandle) {
	handle := &KVStoreLookupHandle{done: make(chan struct{})}
	h.handles = append(h.handles, handle)
	return len(h.handles) - 1, handle
}

// KVStoreInsertHandle represents a pending insert operation
type KVStoreInsertHandle struct {
	done       chan struct{}
	generation uint64
	err        error
}

// Wait blocks until the insert completes
func (h *KVStoreInsertHandle) Wait() (uint64, error) {
	<-h.done
	return h.generation, h.err
}

// Complete marks the insert as complete
func (h *KVStoreInsertHandle) Complete(generation uint64, err error) {
	h.generation = generation
	h.err = err
	close(h.done)
}

// KVStoreInsertHandles manages pending insert operations
type KVStoreInsertHandles struct {
	handles []*KVStoreInsertHandle
}

// Get returns the handle identified by id or nil
func (h *KVStoreInsertHandles) Get(id int) *KVStoreInsertHandle {
	if id < 0 || id >= len(h.handles) {
		return nil
	}
	return h.handles[id]
}

// New creates a new pending insert handle
func (h *KVStoreInsertHandles) New() (int, *KVStoreInsertHandle) {
	handle := &KVStoreInsertHandle{done: make(chan struct{})}
	h.handles = append(h.handles, handle)
	return len(h.handles) - 1, handle
}

// KVStoreDeleteHandle represents a pending delete operation
type KVStoreDeleteHandle struct {
	done chan struct{}
	err  error
}

// Wait blocks until the delete completes
func (h *KVStoreDeleteHandle) Wait() error {
	<-h.done
	return h.err
}

// Complete marks the delete as complete
func (h *KVStoreDeleteHandle) Complete(err error) {
	h.err = err
	close(h.done)
}

// KVStoreDeleteHandles manages pending delete operations
type KVStoreDeleteHandles struct {
	handles []*KVStoreDeleteHandle
}

// Get returns the handle identified by id or nil
func (h *KVStoreDeleteHandles) Get(id int) *KVStoreDeleteHandle {
	if id < 0 || id >= len(h.handles) {
		return nil
	}
	return h.handles[id]
}

// New creates a new pending delete handle
func (h *KVStoreDeleteHandles) New() (int, *KVStoreDeleteHandle) {
	handle := &KVStoreDeleteHandle{done: make(chan struct{})}
	h.handles = append(h.handles, handle)
	return len(h.handles) - 1, handle
}

// KVStoreListHandle represents a pending list operation
type KVStoreListHandle struct {
	done   chan struct{}
	result *ListResult
	err    error
}

// Wait blocks until the list completes
func (h *KVStoreListHandle) Wait() (*ListResult, error) {
	<-h.done
	return h.result, h.err
}

// Complete marks the list as complete
func (h *KVStoreListHandle) Complete(result *ListResult, err error) {
	h.result = result
	h.err = err
	close(h.done)
}

// KVStoreListHandles manages pending list operations
type KVStoreListHandles struct {
	handles []*KVStoreListHandle
}

// Get returns the handle identified by id or nil
func (h *KVStoreListHandles) Get(id int) *KVStoreListHandle {
	if id < 0 || id >= len(h.handles) {
		return nil
	}
	return h.handles[id]
}

// New creates a new pending list handle
func (h *KVStoreListHandles) New() (int, *KVStoreListHandle) {
	handle := &KVStoreListHandle{done: make(chan struct{})}
	h.handles = append(h.handles, handle)
	return len(h.handles) - 1, handle
}

// ToJSON converts a ListResult to JSON bytes for serialization.
func (lr *ListResult) ToJSON() ([]byte, error) {
	return json.Marshal(lr)
}

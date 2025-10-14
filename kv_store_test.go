package fastlike

import (
	"testing"
	"time"
)

func TestKVStore_Insert_and_Lookup(t *testing.T) {
	store := NewKVStore("test")

	// Insert a value
	value := []byte("hello world")
	metadata := "test metadata"
	generation, err := store.Insert("key1", value, metadata, nil, InsertModeOverwrite, nil)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	if generation == 0 {
		t.Error("Expected non-zero generation")
	}

	// Lookup the value
	result, err := store.Lookup("key1")
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result, got nil")
	}

	if string(result.Body) != string(value) {
		t.Errorf("Expected body %q, got %q", value, result.Body)
	}

	if result.Metadata != metadata {
		t.Errorf("Expected metadata %q, got %q", metadata, result.Metadata)
	}

	if result.Generation != generation {
		t.Errorf("Expected generation %d, got %d", generation, result.Generation)
	}
}

func TestKVStore_Delete(t *testing.T) {
	store := NewKVStore("test")

	// Insert a value
	_, err := store.Insert("key1", []byte("value"), "", nil, InsertModeOverwrite, nil)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Delete it
	err = store.Delete("key1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Lookup should return nil
	result, err := store.Lookup("key1")
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}

	if result != nil {
		t.Error("Expected nil result after delete")
	}
}

func TestKVStore_InsertMode_Add(t *testing.T) {
	store := NewKVStore("test")

	// Insert a value
	_, err := store.Insert("key1", []byte("value1"), "", nil, InsertModeAdd, nil)
	if err != nil {
		t.Fatalf("First insert failed: %v", err)
	}

	// Try to insert again with Add mode - should fail
	_, err = store.Insert("key1", []byte("value2"), "", nil, InsertModeAdd, nil)
	if err == nil {
		t.Error("Expected error when adding existing key")
	}
}

func TestKVStore_InsertMode_Append(t *testing.T) {
	store := NewKVStore("test")

	// Insert a value
	_, err := store.Insert("key1", []byte("hello"), "", nil, InsertModeOverwrite, nil)
	if err != nil {
		t.Fatalf("First insert failed: %v", err)
	}

	// Append to it
	_, err = store.Insert("key1", []byte(" world"), "", nil, InsertModeAppend, nil)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Lookup
	result, _ := store.Lookup("key1")
	if string(result.Body) != "hello world" {
		t.Errorf("Expected 'hello world', got %q", result.Body)
	}
}

func TestKVStore_InsertMode_Prepend(t *testing.T) {
	store := NewKVStore("test")

	// Insert a value
	_, err := store.Insert("key1", []byte("world"), "", nil, InsertModeOverwrite, nil)
	if err != nil {
		t.Fatalf("First insert failed: %v", err)
	}

	// Prepend to it
	_, err = store.Insert("key1", []byte("hello "), "", nil, InsertModePrepend, nil)
	if err != nil {
		t.Fatalf("Prepend failed: %v", err)
	}

	// Lookup
	result, _ := store.Lookup("key1")
	if string(result.Body) != "hello world" {
		t.Errorf("Expected 'hello world', got %q", result.Body)
	}
}

func TestKVStore_TTL(t *testing.T) {
	store := NewKVStore("test")

	// Insert a value with 100ms TTL
	ttl := 100 * time.Millisecond
	_, err := store.Insert("key1", []byte("value"), "", &ttl, InsertModeOverwrite, nil)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Should be able to lookup immediately
	result, _ := store.Lookup("key1")
	if result == nil {
		t.Fatal("Expected result immediately after insert")
	}

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Should return nil after expiration
	result, _ = store.Lookup("key1")
	if result != nil {
		t.Error("Expected nil result after TTL expiration")
	}
}

func TestKVStore_List(t *testing.T) {
	store := NewKVStore("test")

	// Insert some values
	keys := []string{"apple", "banana", "cherry", "apricot"}
	for _, key := range keys {
		_, err := store.Insert(key, []byte("value"), "", nil, InsertModeOverwrite, nil)
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	// List all keys
	result, err := store.List("", 100, nil)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(result.Data) != 4 {
		t.Errorf("Expected 4 keys, got %d", len(result.Data))
	}

	// List with prefix
	result, err = store.List("ap", 100, nil)
	if err != nil {
		t.Fatalf("List with prefix failed: %v", err)
	}

	if len(result.Data) != 2 {
		t.Errorf("Expected 2 keys with prefix 'ap', got %d", len(result.Data))
	}

	// Verify keys are sorted
	if result.Data[0] != "apple" || result.Data[1] != "apricot" {
		t.Errorf("Expected sorted keys [apple, apricot], got %v", result.Data)
	}
}

func TestKVStore_List_Pagination(t *testing.T) {
	store := NewKVStore("test")

	// Insert 10 keys
	for i := 0; i < 10; i++ {
		key := string(rune('a' + i))
		_, err := store.Insert(key, []byte("value"), "", nil, InsertModeOverwrite, nil)
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	// List with limit of 5
	result, err := store.List("", 5, nil)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(result.Data) != 5 {
		t.Errorf("Expected 5 keys, got %d", len(result.Data))
	}

	// Should have a next cursor
	if result.Meta.NextCursor == nil {
		t.Error("Expected next cursor, got nil")
	}

	// List with cursor to get next page
	if result.Meta.NextCursor != nil {
		result2, err := store.List("", 5, result.Meta.NextCursor)
		if err != nil {
			t.Fatalf("List with cursor failed: %v", err)
		}

		if len(result2.Data) != 5 {
			t.Errorf("Expected 5 keys on second page, got %d", len(result2.Data))
		}

		// Should not have a next cursor (all items retrieved)
		if result2.Meta.NextCursor != nil {
			t.Error("Expected no next cursor on last page")
		}
	}
}

func TestValidateKey(t *testing.T) {
	tests := []struct {
		key     string
		wantErr bool
	}{
		{"valid-key", false},
		{"valid_key_123", false},
		{"", true},                                // Empty key
		{"key\nwith\nnewline", true},              // Contains newline
		{"key#with#hash", true},                   // Contains #
		{"key;with;semicolon", true},              // Contains ;
		{".well-known/acme-challenge/test", true}, // Forbidden prefix
		{".", true},                               // Just dot
		{"..", true},                              // Just double dot
		{string(make([]byte, 1025)), true},        // Too long (>1024)
		{string(make([]byte, 1024)), false},       // Exactly 1024
	}

	for _, tt := range tests {
		err := ValidateKey(tt.key)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateKey(%q) error = %v, wantErr %v", tt.key, err, tt.wantErr)
		}
	}
}

func TestKVStore_GenerationMatch(t *testing.T) {
	store := NewKVStore("test")

	// Insert initial value
	gen1, err := store.Insert("key1", []byte("value1"), "", nil, InsertModeOverwrite, nil)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Sleep a tiny bit to ensure generation is different
	time.Sleep(time.Microsecond)

	// Update with matching generation - should succeed
	gen2, err := store.Insert("key1", []byte("value2"), "", nil, InsertModeOverwrite, &gen1)
	if err != nil {
		t.Fatalf("Insert with matching generation failed: %v", err)
	}

	if gen2 <= gen1 {
		t.Errorf("Expected generation %d to be greater than %d", gen2, gen1)
	}

	// Try to update with old generation - should fail
	_, err = store.Insert("key1", []byte("value3"), "", nil, InsertModeOverwrite, &gen1)
	if err == nil {
		t.Error("Expected error when generation doesn't match")
	}
}

package fastlike

import (
	"testing"
)

func TestSecretStore(t *testing.T) {
	// Create a simple secret lookup function
	secretLookup := func(key string) ([]byte, bool) {
		secrets := map[string][]byte{
			"api_key":     []byte("secret-api-key-12345"),
			"db_password": []byte("super-secret-password"),
		}
		value, found := secrets[key]
		return value, found
	}

	// Create minimal wasm bytes (just a valid wasm module header)
	// This is enough to test instance creation without running it
	wasmBytes := []byte{
		0x00, 0x61, 0x73, 0x6d, // wasm magic number
		0x01, 0x00, 0x00, 0x00, // version
	}

	// Create an instance with a secret store
	instance := NewInstance(wasmBytes,
		WithSecretStore("my_secrets", secretLookup),
	)

	if instance == nil {
		t.Fatal("Failed to create instance")
	}

	// Verify the secret store was registered
	if len(instance.secretStores) != 1 {
		t.Fatalf("Expected 1 secret store, got %d", len(instance.secretStores))
	}

	if instance.secretStores[0].name != "my_secrets" {
		t.Errorf("Expected secret store name 'my_secrets', got '%s'", instance.secretStores[0].name)
	}

	// Test secret lookup directly
	value, found := instance.secretStores[0].lookup("api_key")
	if !found {
		t.Error("Expected to find 'api_key' secret")
	}
	if string(value) != "secret-api-key-12345" {
		t.Errorf("Expected 'secret-api-key-12345', got '%s'", string(value))
	}

	// Test non-existent secret
	_, found = instance.secretStores[0].lookup("nonexistent")
	if found {
		t.Error("Should not find 'nonexistent' secret")
	}
}

func TestSecretHandles(t *testing.T) {
	handles := &SecretHandles{}

	// Test creating secrets
	plaintext1 := []byte("secret-value-1")
	handle1 := handles.New(plaintext1)

	plaintext2 := []byte("secret-value-2")
	handle2 := handles.New(plaintext2)

	if handle1 == handle2 {
		t.Error("Expected different handles for different secrets")
	}

	// Test retrieving secrets
	secret1 := handles.Get(handle1)
	if secret1 == nil {
		t.Fatal("Expected to retrieve secret1")
	}
	if string(secret1.Plaintext()) != string(plaintext1) {
		t.Errorf("Expected '%s', got '%s'", string(plaintext1), string(secret1.Plaintext()))
	}

	secret2 := handles.Get(handle2)
	if secret2 == nil {
		t.Fatal("Expected to retrieve secret2")
	}
	if string(secret2.Plaintext()) != string(plaintext2) {
		t.Errorf("Expected '%s', got '%s'", string(plaintext2), string(secret2.Plaintext()))
	}

	// Test invalid handle
	invalid := handles.Get(999)
	if invalid != nil {
		t.Error("Expected nil for invalid handle")
	}
}

func TestSecretStoreHandles(t *testing.T) {
	handles := &SecretStoreHandles{}

	// Test creating store handles
	handle1 := handles.New("store1")
	handle2 := handles.New("store2")

	if handle1 == handle2 {
		t.Error("Expected different handles for different stores")
	}

	// Test retrieving store handles
	store1 := handles.Get(handle1)
	if store1 == nil {
		t.Fatal("Expected to retrieve store1")
	}
	if store1.name != "store1" {
		t.Errorf("Expected 'store1', got '%s'", store1.name)
	}

	store2 := handles.Get(handle2)
	if store2 == nil {
		t.Fatal("Expected to retrieve store2")
	}
	if store2.name != "store2" {
		t.Errorf("Expected 'store2', got '%s'", store2.name)
	}

	// Test invalid handle
	invalid := handles.Get(999)
	if invalid != nil {
		t.Error("Expected nil for invalid handle")
	}
}

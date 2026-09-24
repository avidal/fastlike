package fastlike

import (
	"strings"
	"testing"
)

// The guest re-exports the cache override imports, so the tests go through the
// same linker and wrappers as real guests, and can observe traps.
const cacheOverrideGuestWat = `(module
  (import "fastly_http_req" "cache_override_set" (func $v1 (param i32 i32 i32 i32) (result i32)))
  (import "fastly_http_req" "cache_override_v2_set" (func $v2 (param i32 i32 i32 i32 i32 i32) (result i32)))
  (import "fastly_http_req" "cache_override_v3_set" (func $v3 (param i32 i32 i32) (result i32)))
  (import "env" "xqd_req_cache_override_set" (func $legacy_v1 (param i32 i32 i32 i32) (result i32)))
  (import "env" "xqd_req_cache_override_v2_set" (func $legacy_v2 (param i32 i32 i32 i32 i32 i32) (result i32)))
  (memory (export "memory") 1)
  (func (export "_start"))
  (export "v1" (func $v1))
  (export "v2" (func $v2))
  (export "v3" (func $v3))
  (export "legacy_v1" (func $legacy_v1))
  (export "legacy_v2" (func $legacy_v2)))`

const (
	overrideGuestMemorySize = 65536
	overrideRecordAddr      = 64
	overrideKeysAddr        = 256
	overrideBadHandle       = 99
)

type cacheOverrideGuest struct {
	instance *Instance
	handle   int32
}

func newCacheOverrideGuest(t *testing.T) *cacheOverrideGuest {
	t.Helper()
	i := newWatInstance(t, cacheOverrideGuestWat)
	if _, err := i.setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	handle, _ := i.requests.New()
	return &cacheOverrideGuest{instance: i, handle: int32(handle)}
}

// call returns the status of a hostcall, or the trap it raised.
func (g *cacheOverrideGuest) call(name string, args ...int32) (int32, error) {
	params := make([]interface{}, len(args))
	for idx, arg := range args {
		params[idx] = arg
	}
	ret, err := g.instance.wasm.GetFunc(g.instance.store, name).Call(g.instance.store, params...)
	if err != nil {
		return 0, err
	}
	return ret.(int32), nil
}

func (g *cacheOverrideGuest) writeRecord(addr int64, ttl, swr, keysAddr, keysLen, lookupTimeoutMs uint32) {
	for idx, v := range []uint32{ttl, swr, keysAddr, keysLen, lookupTimeoutMs} {
		g.instance.memory.PutUint32(v, addr+int64(idx*4))
	}
}

func expectStatus(t *testing.T, what string, got int32, err error, want int32) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected trap: %v", what, err)
	}
	if got != want {
		t.Errorf("%s: status %d, want %d", what, got, want)
	}
}

func expectTagTrap(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: returned instead of trapping", what)
	}
	if !strings.Contains(err.Error(), "invalid cache_override_tag flags") {
		t.Errorf("%s: unexpected trap: %v", what, err)
	}
}

func TestCacheOverrideUnknownTagBitsTrap(t *testing.T) {
	g := newCacheOverrideGuest(t)

	for _, tag := range []int32{1 << 5, 1 << 30, -1} {
		// The tag is converted before anything else, so an invalid handle
		// or record does not change the outcome.
		_, err := g.call("v1", overrideBadHandle, tag, 0, 0)
		expectTagTrap(t, "v1", err)
		_, err = g.call("v2", overrideBadHandle, tag, 0, 0, 0, 0)
		expectTagTrap(t, "v2", err)
		_, err = g.call("v3", overrideBadHandle, tag, overrideGuestMemorySize)
		expectTagTrap(t, "v3", err)
		_, err = g.call("legacy_v1", overrideBadHandle, tag, 0, 0)
		expectTagTrap(t, "legacy_v1", err)
		_, err = g.call("legacy_v2", overrideBadHandle, tag, 0, 0, 0, 0)
		expectTagTrap(t, "legacy_v2", err)
	}
}

func TestCacheOverrideAcceptsKnownTags(t *testing.T) {
	g := newCacheOverrideGuest(t)
	g.writeRecord(overrideRecordAddr, 60, 30, 0, 0, 500)

	for _, tag := range []uint32{
		0,
		CacheOverrideTagPass,
		CacheOverrideTagTTL | CacheOverrideTagStaleWhileRevalidate,
		CacheOverrideTagPCI,
		CacheOverrideTagLookupTimeout,
		cacheOverrideTagKnown,
	} {
		got, err := g.call("v1", g.handle, int32(tag), 60, 30)
		expectStatus(t, "v1", got, err, XqdStatusOK)
		got, err = g.call("v2", g.handle, int32(tag), 60, 30, 0, 0)
		expectStatus(t, "v2", got, err, XqdStatusOK)
		got, err = g.call("v3", g.handle, int32(tag), overrideRecordAddr)
		expectStatus(t, "v3", got, err, XqdStatusOK)
	}
}

func TestCacheOverrideV3Record(t *testing.T) {
	g := newCacheOverrideGuest(t)
	tag := int32(CacheOverrideTagTTL)

	tests := []struct {
		name string
		addr int32
		want int32
	}{
		{"aligned and in bounds", overrideRecordAddr, XqdStatusOK},
		{"last record that fits", overrideGuestMemorySize - cacheOverrideSize, XqdStatusOK},
		{"misaligned", overrideRecordAddr + 1, XqdErrBadAlignment},
		{"misaligned and out of bounds", overrideGuestMemorySize - 1, XqdErrInvalidArgument},
		{"first field in bounds, record past the end", overrideGuestMemorySize - 8, XqdErrInvalidArgument},
		{"past the end", overrideGuestMemorySize, XqdErrInvalidArgument},
		{"beyond 2 GiB", -4, XqdErrInvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := g.call("v3", g.handle, tag, tt.addr)
			expectStatus(t, "v3", got, err, tt.want)
		})
	}
}

func TestCacheOverrideSurrogateKeys(t *testing.T) {
	g := newCacheOverrideGuest(t)
	tag := int32(CacheOverrideTagTTL)

	tests := []struct {
		name     string
		keys     string
		keysAddr int32
		keysLen  int32
		want     int32
	}{
		{name: "valid keys", keys: "key-a key-b\tkey-\x80", want: XqdStatusOK},
		{name: "control character", keys: "key-a\nkey-b", want: XqdErrInvalidArgument},
		{name: "delete character", keys: "key\x7f", want: XqdErrInvalidArgument},
		{name: "out of bounds", keysAddr: overrideGuestMemorySize - 2, keysLen: 4, want: XqdErrInvalidArgument},
		{name: "empty keys are not read", keysAddr: overrideGuestMemorySize + 16, keysLen: 0, want: XqdStatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keysAddr, keysLen := tt.keysAddr, tt.keysLen
			if tt.keys != "" {
				keysAddr, keysLen = writeStr(t, g.instance, overrideKeysAddr, tt.keys)
			}
			got, err := g.call("v2", g.handle, tag, 60, 0, keysAddr, keysLen)
			expectStatus(t, "v2", got, err, tt.want)

			g.writeRecord(overrideRecordAddr, 60, 0, uint32(keysAddr), uint32(keysLen), 0)
			got, err = g.call("v3", g.handle, tag, overrideRecordAddr)
			expectStatus(t, "v3", got, err, tt.want)
		})
	}
}

func TestCacheOverrideChecksHandleLast(t *testing.T) {
	g := newCacheOverrideGuest(t)
	tag := int32(CacheOverrideTagTTL)

	got, err := g.call("v1", overrideBadHandle, tag, 60, 0)
	expectStatus(t, "v1 bad handle", got, err, XqdErrInvalidHandle)

	_, keysLen := writeStr(t, g.instance, overrideKeysAddr, "valid")
	got, err = g.call("v2", overrideBadHandle, tag, 60, 0, overrideKeysAddr, keysLen)
	expectStatus(t, "v2 bad handle", got, err, XqdErrInvalidHandle)

	g.writeRecord(overrideRecordAddr, 60, 0, overrideKeysAddr, uint32(keysLen), 0)
	got, err = g.call("v3", overrideBadHandle, tag, overrideRecordAddr)
	expectStatus(t, "v3 bad handle", got, err, XqdErrInvalidHandle)

	// Argument errors win over the handle check.
	_, keysLen = writeStr(t, g.instance, overrideKeysAddr, "bad\x00key")
	got, err = g.call("v2", overrideBadHandle, tag, 60, 0, overrideKeysAddr, keysLen)
	expectStatus(t, "v2 bad keys and handle", got, err, XqdErrInvalidArgument)
	got, err = g.call("v3", overrideBadHandle, tag, overrideRecordAddr+2)
	expectStatus(t, "v3 misaligned record and bad handle", got, err, XqdErrBadAlignment)

	// A closed handle is as invalid as one that never existed.
	g.instance.requests.Take(int(g.handle))
	got, err = g.call("v1", g.handle, tag, 60, 0)
	expectStatus(t, "v1 closed handle", got, err, XqdErrInvalidHandle)
}

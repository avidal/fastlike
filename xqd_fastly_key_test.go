package fastlike

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newKeyTestInstance(keys ...string) *Instance {
	inst := &Instance{
		requests: &RequestHandles{},
		memory:   &Memory{ByteMemory(make([]byte, 4096))},
		abilog:   log.New(io.Discard, "", 0),
	}
	WithFakeValidFastlyKeys(keys...)(inst)
	return inst
}

func TestReqFastlyKeyIsValid(t *testing.T) {
	cases := []struct {
		name      string
		configure []string
		header    string // "" means no Fastly-Key header
		want      uint32
	}{
		{"matching key is valid", []string{"secret"}, "secret", 1},
		{"wrong key is invalid", []string{"secret"}, "nope", 0},
		{"missing header is invalid", []string{"secret"}, "", 0},
		{"no keys configured is invalid", nil, "secret", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inst := newKeyTestInstance(tc.configure...)
			req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
			if tc.header != "" {
				req.Header.Set("Fastly-Key", tc.header)
			}
			inst.ds_request = req

			const out int32 = 0
			if status := inst.xqd_req_fastly_key_is_valid(out); status != XqdStatusOK {
				t.Fatalf("status = %d, want %d", status, XqdStatusOK)
			}
			if got := inst.memory.Uint32(int64(out)); got != tc.want {
				t.Errorf("is_valid = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestHttpDownstreamFastlyKeyIsValid(t *testing.T) {
	cases := []struct {
		name      string
		configure []string
		header    string
		want      uint32
	}{
		{"matching key is valid", []string{"secret"}, "secret", 1},
		{"wrong key is invalid", []string{"secret"}, "nope", 0},
		{"missing header is invalid", []string{"secret"}, "", 0},
		{"no keys configured is invalid", nil, "secret", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inst := newKeyTestInstance(tc.configure...)
			rhid, rh := inst.requests.New()
			if tc.header != "" {
				rh.Header.Set("Fastly-Key", tc.header)
			}

			const out int32 = 0
			if status := inst.xqd_http_downstream_fastly_key_is_valid(int32(rhid), out); status != XqdStatusOK {
				t.Fatalf("status = %d, want %d", status, XqdStatusOK)
			}
			if got := inst.memory.Uint32(int64(out)); got != tc.want {
				t.Errorf("is_valid = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestHttpDownstreamFastlyKeyIsValid_InvalidHandle(t *testing.T) {
	inst := newKeyTestInstance("secret")
	if status := inst.xqd_http_downstream_fastly_key_is_valid(999, 0); status != XqdErrInvalidHandle {
		t.Errorf("status = %d, want %d", status, XqdErrInvalidHandle)
	}
}

func TestWithFakeValidFastlyKeys_DropsEmptyAndAccumulates(t *testing.T) {
	inst := &Instance{}
	WithFakeValidFastlyKeys("a", "")(inst)
	WithFakeValidFastlyKeys("b")(inst)

	if _, ok := inst.fakeValidFastlyKeys[""]; ok {
		t.Fatal("empty key should not be stored")
	}
	for _, k := range []string{"a", "b"} {
		if _, ok := inst.fakeValidFastlyKeys[k]; !ok {
			t.Fatalf("expected key %q to be configured", k)
		}
	}
	if len(inst.fakeValidFastlyKeys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(inst.fakeValidFastlyKeys))
	}
}

func TestWithFakeValidFastlyKeys_AllEmptyLeavesMapNil(t *testing.T) {
	inst := &Instance{}
	WithFakeValidFastlyKeys("", "")(inst)
	if inst.fakeValidFastlyKeys != nil {
		t.Fatalf("expected nil map when only empty keys supplied, got %v", inst.fakeValidFastlyKeys)
	}
}

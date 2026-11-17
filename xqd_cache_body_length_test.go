package fastlike

import (
	"testing"
	"time"
)

func getCacheBody(t *testing.T, i *Instance, handle int32, r byteRange) int {
	t.Helper()
	mask := uint32(0)
	if r.hasFirst {
		mask |= CacheGetBodyOptionsMaskFrom
		i.memory.WriteUint64(replaceTestBodyOptsPtr, r.first)
	}
	if r.hasLast {
		mask |= CacheGetBodyOptionsMaskTo
		i.memory.WriteUint64(replaceTestBodyOptsPtr+8, r.last)
	}
	if status := i.xqd_cache_get_body(handle, mask, replaceTestBodyOptsPtr, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("cache_get_body status = %d, want %d", status, XqdStatusOK)
	}
	return int(i.memory.Uint32(replaceTestHandleOut))
}

// bodyKnownLength is what body_known_length reports, or -1 for none.
func bodyKnownLength(t *testing.T, i *Instance, bodyID int) int64 {
	t.Helper()
	switch status := i.xqd_body_known_length(int32(bodyID), replaceTestValueOut); status {
	case XqdStatusOK:
		return int64(i.memory.Uint64(replaceTestValueOut))
	case XqdErrNone:
		return -1
	default:
		t.Fatalf("body_known_length status = %d", status)
		return 0
	}
}

func fromTo(first, last uint64) byteRange {
	return byteRange{first: first, last: last, hasFirst: true, hasLast: true}
}

// A Core Cache body knows its length whenever production does, which is what
// known_length and automatic framing use.
func TestCacheBodyLengthIsWhatProductionSends(t *testing.T) {
	const content = "0123456789"
	const forced = CacheLookupOptionsMaskAlwaysUseRequestedRange
	tests := []struct {
		name string
		// declared sets known_length on the insert, finished ends it before
		// the lookup, and finishedLater after it.
		declared, finished, finishedLater bool
		lookupMask                        uint32
		r                                 byteRange
		want                              int64
		wantBody                          string
	}{
		{name: "declared length", declared: true, want: 10, wantBody: content},
		{name: "range of a known length", finished: true, r: fromTo(2, 4), want: 3, wantBody: "234"},
		{name: "suffix of a known length", finished: true, r: byteRange{last: 4, hasLast: true}, want: 4, wantBody: "6789"},
		{name: "range past the end", finished: true, r: fromTo(2, 10), want: 10, wantBody: content},
		{name: "insert finished after the lookup", finishedLater: true, want: -1, wantBody: content},
		{name: "forced range whose start arrived", lookupMask: forced, r: fromTo(2, 4), want: 3, wantBody: "234"},
		{name: "forced range whose start has not", lookupMask: forced, r: fromTo(10, 12), want: -1, wantBody: content},
		{name: "forced open range", lookupMask: forced, r: byteRange{first: 2, hasFirst: true}, want: -1, wantBody: "23456789"},
		{name: "forced suffix of a running insert", lookupMask: forced, r: byteRange{last: 4, hasLast: true}, want: -1, wantBody: "6789"},
		{name: "forced whole insert finished after the lookup", lookupMask: forced, finishedLater: true, want: 10, wantBody: content},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := newCacheReplaceTestInstance()
			insertTestObject(t, i, "key", replaceTestObject{content: content, maxAge: time.Minute, length: tt.declared, finish: tt.finished})
			writer := i.bodies.Get(int(i.memory.Uint32(replaceTestHandleOut)))
			handle := lookupWithOptions(t, i, "key", tt.lookupMask)
			if tt.finishedLater {
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
			}

			body := getCacheBody(t, i, handle, tt.r)
			if got := bodyKnownLength(t, i, body); got != tt.want {
				t.Errorf("known length = %d, want %d", got, tt.want)
			}
			if !tt.finished && !tt.finishedLater {
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if got := readTestBody(t, i, body); got != tt.wantBody {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}
		})
	}
}

// Like in production, the handle keeps the transaction's range strategy.
func TestCacheStreamBackKeepsTheLookupRangeStrategy(t *testing.T) {
	i := newCacheReplaceTestInstance()
	keyPtr, keyLen := writeCacheKey(i, "key")
	if status := i.xqd_cache_transaction_lookup(keyPtr, keyLen, CacheLookupOptionsMaskAlwaysUseRequestedRange, 0, replaceTestHandleOut); status != XqdStatusOK {
		t.Fatalf("transaction_lookup status = %d", status)
	}
	handle := int32(i.memory.Uint32(replaceTestHandleOut))
	mask := writeCacheWriteOptions(i, replaceTestObject{maxAge: time.Minute})
	if status := i.xqd_cache_transaction_insert_and_stream_back(handle, mask, replaceTestWriteOptsPtr, replaceTestHandleOut, replaceTestValueOut); status != XqdStatusOK {
		t.Fatalf("insert_and_stream_back status = %d", status)
	}
	writer := i.bodies.Get(int(i.memory.Uint32(replaceTestHandleOut)))
	readback := int32(i.memory.Uint32(replaceTestValueOut))
	if _, err := writer.Write([]byte("01234")); err != nil {
		t.Fatal(err)
	}

	body := getCacheBody(t, i, readback, fromTo(1, 2))
	if got := bodyKnownLength(t, i, body); got != 2 {
		t.Errorf("known length = %d, want 2", got)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readTestBody(t, i, body); got != "12" {
		t.Errorf("body = %q, want %q", got, "12")
	}
}

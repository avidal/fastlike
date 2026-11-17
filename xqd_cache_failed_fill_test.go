package fastlike

import (
	"io"
	"testing"
	"time"
)

// A fill that fails must not stay in the cache as an object nobody can read.
// Lookups miss right away, and the next transaction lookup gets to fill it.
//
// Like in production, a fill fails when it is abandoned, or when it ends
// without the length it declared.
func TestCacheFailedFillIsNotServed(t *testing.T) {
	const declared = 10
	tests := []struct {
		name     string
		written  string
		abandon  bool
		appended bool
		stored   bool
	}{
		{name: "short and abandoned", written: "01234", abandon: true},
		{name: "long and abandoned", written: "0123456789abcde", abandon: true},
		{name: "short and finished", written: "01234"},
		{name: "long and finished", written: "0123456789abcde"},
		{name: "exact and finished", written: "0123456789", stored: true},
		{name: "short, appended and finished", written: "01234", appended: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := newCacheReplaceTestInstance()
			leader := transactionLookup(t, i, "key")
			i.memory.WriteUint64(replaceTestWriteOptsPtr, uint64(time.Minute))
			i.memory.WriteUint64(replaceTestWriteOptsPtr+replaceTestWriteLength, declared)
			if status := i.xqd_cache_transaction_insert_and_stream_back(leader, CacheWriteOptionsMaskLength, replaceTestWriteOptsPtr, replaceTestHandleOut, replaceTestValueOut); status != XqdStatusOK {
				t.Fatalf("cache_transaction_insert_and_stream_back status = %d", status)
			}
			writer := int32(i.memory.Uint32(replaceTestHandleOut))
			found := int32(i.memory.Uint32(replaceTestValueOut))
			obj := onlyObject(t, i, "key")
			body := i.bodies.Get(getCacheBody(t, i, found, byteRange{}))

			source, feed := io.Pipe()
			if tt.appended {
				appendSource(t, i, writer, source)
			} else if _, err := i.bodies.Get(int(writer)).Write([]byte(tt.written)); err != nil {
				t.Fatal(err)
			}
			end := i.xqd_body_close
			if tt.abandon {
				end = i.xqd_body_abandon
			}
			if status := end(writer); status != XqdStatusOK {
				t.Fatalf("ending the fill: status %d", status)
			}
			if tt.appended {
				// The fill only ends once the copy is done.
				if _, err := feed.Write([]byte(tt.written)); err != nil {
					t.Fatal(err)
				}
				_ = feed.Close()
				obj.waitForWriteComplete()
			}

			wantLookup, wantTransaction := uint32(0), CacheLookupStateMustInsertOrUpdate
			if tt.stored {
				wantLookup, wantTransaction = foundState, foundState
			}
			if state := lookupState(t, i, "key"); state != wantLookup {
				t.Errorf("lookup state = %#x, want %#x", state, wantLookup)
			}
			if state := handleState(t, i, transactionLookup(t, i, "key")); state != wantTransaction {
				t.Errorf("transaction lookup state = %#x, want %#x", state, wantTransaction)
			}

			// A reader that already had the fill gets the declared bytes, or
			// an error when they never came.
			got, err := io.ReadAll(body)
			if len(tt.written) < declared {
				if err == nil {
					t.Errorf("read of a short fill returned %q without an error", got)
				}
			} else if err != nil || string(got) != tt.written[:declared] {
				t.Errorf("read = %q, %v, want %q", got, err, tt.written[:declared])
			}
		})
	}
}

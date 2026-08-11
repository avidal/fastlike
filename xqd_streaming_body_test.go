package fastlike

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"testing"
	"time"
)

type streamingErrorReader struct {
	done bool
}

func (r *streamingErrorReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, "partial"), errors.New("initial body read failed")
}

func (*streamingErrorReader) Close() error { return nil }

func TestStreamingBodyCloseQueuesFinishAfterFullBuffer(t *testing.T) {
	body := &BodyHandle{
		isStreaming:   true,
		streamingChan: make(chan []byte, 1),
		streamingDone: make(chan struct{}),
	}
	body.streamingChan <- []byte("chunk")

	closed := make(chan error, 1)
	go func() {
		closed <- body.Close()
	}()

	// With no capacity available, Close must retain responsibility for
	// delivering the finish marker instead of silently dropping it.
	closeReturned := false
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
		closeReturned = true
	case <-time.After(20 * time.Millisecond):
	}

	if got := string(<-body.streamingChan); got != "chunk" {
		t.Fatalf("first queued chunk = %q, want %q", got, "chunk")
	}

	if !closeReturned {
		select {
		case err := <-closed:
			if err != nil {
				t.Fatalf("Close returned error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Close did not finish after buffer capacity became available")
		}
	}

	select {
	case chunk := <-body.streamingChan:
		if chunk != nil {
			t.Fatalf("termination item = %q, want nil finish marker", chunk)
		}
	case <-time.After(time.Second):
		t.Fatal("Close returned without queueing a finish marker")
	}
}

func TestBodyAbandonCancelsStreamingRequest(t *testing.T) {
	i := &Instance{
		requests:        &RequestHandles{},
		bodies:          &BodyHandles{},
		pendingRequests: &PendingRequestHandles{},
		backends:        map[string]*Backend{},
		memory:          &Memory{ByteMemory(make([]byte, 1024))},
		abilog:          log.New(io.Discard, "", 0),
		ds_context:      context.Background(),
	}
	backendCalled := make(chan struct{}, 1)
	i.addBackend("origin", &Backend{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		backendCalled <- struct{}{}
	})})

	reqHandle, _ := i.requests.New()
	bodyHandle, _ := i.bodies.NewBuffer()
	backendAddr, backendSize := writeStr(t, i, 100, "origin")
	const pendingOut = int32(200)
	if status := i.xqd_req_send_async_streaming(int32(reqHandle), int32(bodyHandle), backendAddr, backendSize, pendingOut); status != XqdStatusOK {
		t.Fatalf("req_send_async_streaming status = %d, want %d", status, XqdStatusOK)
	}
	pending := i.pendingRequests.Get(int(i.memory.Uint32(int64(pendingOut))))
	if pending == nil {
		t.Fatal("pending request handle was not created")
	}

	if status := i.xqd_body_abandon(int32(bodyHandle)); status != XqdStatusOK {
		t.Fatalf("body_abandon status = %d, want %d", status, XqdStatusOK)
	}

	select {
	case <-pending.done:
	case <-time.After(time.Second):
		t.Fatal("abandoned streaming request did not complete")
	}
	if pending.err == nil {
		t.Fatal("abandoned streaming request completed successfully")
	}
	select {
	case <-backendCalled:
		t.Fatal("abandoned streaming request was dispatched to the backend")
	default:
	}
}

func TestAsyncIOIsReadyReportsStreamingWriteCapacity(t *testing.T) {
	i := &Instance{
		bodies:          &BodyHandles{},
		pendingRequests: &PendingRequestHandles{},
		asyncItems:      &AsyncItemHandles{},
		memory:          &Memory{ByteMemory(make([]byte, 1024))},
		abilog:          log.New(io.Discard, "", 0),
	}

	handle, body := i.bodies.NewBuffer()
	body.isStreaming = true
	body.streamingChan = make(chan []byte, 1)
	body.streamingDone = make(chan struct{})
	body.streamingAbandon = make(chan struct{})
	_, _ = i.pendingRequests.New()

	const readyOut = int32(100)
	if status := i.xqd_async_io_is_ready(int32(handle), readyOut); status != XqdStatusOK {
		t.Fatalf("async_io_is_ready status = %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint32(int64(readyOut)); got != 1 {
		t.Fatalf("streaming body with write capacity ready = %d, want 1", got)
	}

	body.streamingChan <- []byte("full")
	if status := i.xqd_async_io_is_ready(int32(handle), readyOut); status != XqdStatusOK {
		t.Fatalf("async_io_is_ready for full body status = %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint32(int64(readyOut)); got != 0 {
		t.Fatalf("full streaming body ready = %d, want 0", got)
	}

	i.memory.PutUint32(uint32(handle), 0)
	drained := make(chan struct{})
	go func() {
		<-body.streamingChan
		body.signalStreamingSpace()
		close(drained)
	}()
	const readyIndexOut = int32(200)
	if status := i.xqd_async_io_select(0, 1, 1000, readyIndexOut); status != XqdStatusOK {
		t.Fatalf("async_io_select status = %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint32(int64(readyIndexOut)); got != 0 {
		t.Fatalf("ready index after streaming buffer drain = %d, want 0", got)
	}
	<-drained
}

func TestAsyncIOResolvesKVLookupHandle(t *testing.T) {
	i := &Instance{
		bodies:           &BodyHandles{},
		pendingRequests:  &PendingRequestHandles{},
		kvLookups:        &KVStoreLookupHandles{},
		kvInserts:        &KVStoreInsertHandles{},
		kvDeletes:        &KVStoreDeleteHandles{},
		kvLists:          &KVStoreListHandles{},
		cacheBusyHandles: &CacheBusyHandles{},
		requestPromises:  &RequestPromiseHandles{},
		asyncItems:       &AsyncItemHandles{},
		memory:           &Memory{ByteMemory(make([]byte, 1024))},
		abilog:           log.New(io.Discard, "", 0),
	}
	handle, lookup := i.kvLookups.New()
	lookup.Complete(nil, nil)

	if status := i.xqd_async_io_is_ready(int32(handle), 100); status != XqdStatusOK {
		t.Fatalf("async_io_is_ready status = %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint32(100); got != 1 {
		t.Fatalf("completed KV lookup ready = %d, want 1", got)
	}
}

func TestStreamingRequestContextCancellationCompletesPendingRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	i := &Instance{
		requests:        &RequestHandles{},
		bodies:          &BodyHandles{},
		pendingRequests: &PendingRequestHandles{},
		backends:        map[string]*Backend{},
		memory:          &Memory{ByteMemory(make([]byte, 1024))},
		abilog:          log.New(io.Discard, "", 0),
		ds_context:      ctx,
	}
	backendCalled := make(chan struct{}, 1)
	i.addBackend("origin", &Backend{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		backendCalled <- struct{}{}
	})})

	reqHandle, _ := i.requests.New()
	bodyHandle, _ := i.bodies.NewBuffer()
	backendAddr, backendSize := writeStr(t, i, 100, "origin")
	if status := i.xqd_req_send_async_streaming(int32(reqHandle), int32(bodyHandle), backendAddr, backendSize, 200); status != XqdStatusOK {
		t.Fatalf("req_send_async_streaming status = %d, want %d", status, XqdStatusOK)
	}
	pending := i.pendingRequests.Get(int(i.memory.Uint32(200)))
	cancel()

	select {
	case <-pending.done:
	case <-time.After(time.Second):
		t.Fatal("cancelled streaming request did not complete")
	}
	if !errors.Is(pending.err, context.Canceled) {
		t.Fatalf("pending error = %v, want context.Canceled", pending.err)
	}
	select {
	case <-backendCalled:
		t.Fatal("cancelled streaming request was dispatched to the backend")
	default:
	}
}

func TestStreamingRequestDoesNotIgnoreInitialBodyReadError(t *testing.T) {
	i := &Instance{
		requests:        &RequestHandles{},
		bodies:          &BodyHandles{},
		pendingRequests: &PendingRequestHandles{},
		backends:        map[string]*Backend{},
		memory:          &Memory{ByteMemory(make([]byte, 1024))},
		abilog:          log.New(io.Discard, "", 0),
		ds_context:      context.Background(),
	}
	backendCalled := make(chan struct{}, 1)
	i.addBackend("origin", &Backend{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		backendCalled <- struct{}{}
	})})
	reqHandle, _ := i.requests.New()
	bodyHandle, _ := i.bodies.NewReader(&streamingErrorReader{})
	backendAddr, backendSize := writeStr(t, i, 100, "origin")
	if status := i.xqd_req_send_async_streaming(int32(reqHandle), int32(bodyHandle), backendAddr, backendSize, 200); status != XqdStatusOK {
		t.Fatalf("req_send_async_streaming status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_body_close(int32(bodyHandle)); status != XqdStatusOK {
		t.Fatalf("body_close status = %d, want %d", status, XqdStatusOK)
	}
	pending := i.pendingRequests.Get(int(i.memory.Uint32(200)))
	select {
	case <-pending.done:
	case <-time.After(time.Second):
		t.Fatal("streaming request did not complete")
	}
	if pending.err == nil || pending.err.Error() != "initial body read failed" {
		t.Fatalf("pending error = %v, want initial body read failure", pending.err)
	}
	select {
	case <-backendCalled:
		t.Fatal("request with an initial body read error reached the backend")
	default:
	}
}

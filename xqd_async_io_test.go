package fastlike

import (
	"context"
	"io"
	"log"
	"testing"
	"time"
)

func newAsyncSelectTestInstance(ctx context.Context) *Instance {
	return &Instance{
		pendingRequests: &PendingRequestHandles{},
		memory:          &Memory{ByteMemory(make([]byte, 64))},
		abilog:          log.New(io.Discard, "", 0),
		ds_context:      ctx,
	}
}

func runAsyncSelect(t *testing.T, i *Instance, handlesLen int32, timeoutMs int32) int32 {
	t.Helper()
	done := make(chan int32, 1)
	go func() { done <- i.xqd_async_io_select(0, handlesLen, timeoutMs, 8) }()
	select {
	case status := <-done:
		return status
	case <-time.After(5 * time.Second):
		t.Fatal("async_io_select is still waiting")
		return 0
	}
}

func TestAsyncIoSelectStopsWhenDownstreamIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	i := newAsyncSelectTestInstance(ctx)
	handle, _ := i.pendingRequests.New()
	i.memory.PutUint32(uint32(handle), 0)

	time.AfterFunc(20*time.Millisecond, cancel)
	if status := runAsyncSelect(t, i, 1, 0); status != XqdError {
		t.Fatalf("status %d, want %d", status, XqdError)
	}
}

func TestAsyncIoSelectWithLiveDownstream(t *testing.T) {
	i := newAsyncSelectTestInstance(context.Background())
	idle, _ := i.pendingRequests.New()
	i.memory.PutUint32(uint32(idle), 0)

	if status := runAsyncSelect(t, i, 1, 10); status != XqdStatusOK {
		t.Fatalf("timeout: status %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint32(8); got != 0xFFFFFFFF {
		t.Fatalf("timeout: ready index %#x, want 0xffffffff", got)
	}

	ready, pr := i.pendingRequests.New()
	pr.Complete(nil, nil)
	i.memory.PutUint32(uint32(ready), 0)
	if status := runAsyncSelect(t, i, 1, 0); status != XqdStatusOK {
		t.Fatalf("ready: status %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint32(8); got != 0 {
		t.Fatalf("ready: ready index %d, want 0", got)
	}
}

func TestAsyncIoSelectEmptyListStopsWhenDownstreamIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	i := newAsyncSelectTestInstance(ctx)

	// A u32::MAX timeout would otherwise sleep for about 49 days.
	time.AfterFunc(20*time.Millisecond, cancel)
	if status := runAsyncSelect(t, i, 0, -1); status != XqdError {
		t.Fatalf("status %d, want %d", status, XqdError)
	}
}

func TestAsyncIoSelectEmptyListTimesOut(t *testing.T) {
	i := newAsyncSelectTestInstance(context.Background())
	if status := runAsyncSelect(t, i, 0, 10); status != XqdStatusOK {
		t.Fatalf("status %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint32(8); got != 0xFFFFFFFF {
		t.Fatalf("ready index %#x, want 0xffffffff", got)
	}
}

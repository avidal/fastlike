package fastlike

import (
	"time"
)

// neverReadyChan is a sentinel channel that never closes, reused to avoid memory leaks
var neverReadyChan = make(chan struct{})

// xqd_async_io_select waits for one of multiple async operations to complete.
// Monitors a list of async handles and returns the index of the first one that becomes ready.
// Returns the index (0-based) of the ready item, or u32::MAX (0xFFFFFFFF) on timeout.
//
// The function supports async items (KV operations, cache operations), pending requests, and body handles.
// It uses goroutines to monitor each handle's completion channel concurrently.
func (i *Instance) xqd_async_io_select(handles_addr int32, handles_len int32, timeout_ms int32, ready_idx_out int32) int32 {
	i.abilog.Printf("async_io_select: handles_len=%d timeout_ms=%d", handles_len, timeout_ms)

	// Special case: empty list with zero timeout is invalid
	if handles_len == 0 && timeout_ms == 0 {
		i.abilog.Printf("async_io_select: invalid argument (empty list with zero timeout)")
		return XqdErrInvalidArgument
	}

	// Special case: empty list with non-zero timeout - just wait for timeout
	if handles_len == 0 {
		i.abilog.Printf("async_io_select: empty list, waiting for timeout")
		// Pause CPU time tracking while waiting
		i.pauseExecution()
		time.Sleep(time.Duration(uint32(timeout_ms)) * time.Millisecond)
		i.resumeExecution()
		// Return u32::MAX to indicate timeout
		i.memory.PutUint32(0xFFFFFFFF, int64(ready_idx_out))
		return XqdStatusOK
	}

	// Read the list of async item handles from guest memory
	handles := make([]uint32, handles_len)
	for idx := int32(0); idx < handles_len; idx++ {
		handle := i.memory.Uint32(int64(handles_addr + idx*4))
		handles[idx] = handle
	}

	// Build a list of channels to select on
	type selectCase struct {
		index   int
		channel <-chan struct{}
	}

	cases := make([]selectCase, 0, len(handles))
	for idx, handle := range handles {
		ch := i.getHandleChannel(int(handle))
		if ch == nil {
			i.abilog.Printf("async_io_select: invalid handle=%d at index=%d", handle, idx)
			return XqdErrInvalidHandle
		}

		cases = append(cases, selectCase{
			index:   idx,
			channel: ch,
		})
	}

	// Check if any are already ready (non-blocking)
	for _, c := range cases {
		select {
		case <-c.channel:
			i.abilog.Printf("async_io_select: handle at index %d already ready", c.index)
			i.memory.PutUint32(uint32(c.index), int64(ready_idx_out))
			return XqdStatusOK
		default:
			// Not ready, continue
		}
	}

	// None are ready yet, use select with timeout.
	// We spawn a goroutine for each handle to monitor its channel.
	// The first one to complete sends its index to doneCh.
	doneCh := make(chan int, len(cases))

	// Spawn goroutines to monitor each channel
	for _, c := range cases {
		go func(idx int, ch <-chan struct{}) {
			<-ch
			doneCh <- idx
		}(c.index, c.channel)
	}

	// Pause CPU time tracking while waiting
	i.pauseExecution()
	defer i.resumeExecution()

	// Wait for first ready or timeout
	if timeout_ms == 0 {
		// No timeout, wait indefinitely
		doneIndex := <-doneCh
		i.abilog.Printf("async_io_select: handle at index %d completed", doneIndex)
		i.memory.PutUint32(uint32(doneIndex), int64(ready_idx_out))
		return XqdStatusOK
	} else {
		// Wait with timeout
		select {
		case doneIndex := <-doneCh:
			i.abilog.Printf("async_io_select: handle at index %d completed", doneIndex)
			i.memory.PutUint32(uint32(doneIndex), int64(ready_idx_out))
			return XqdStatusOK
		case <-time.After(time.Duration(uint32(timeout_ms)) * time.Millisecond):
			i.abilog.Printf("async_io_select: timeout expired")
			i.memory.PutUint32(0xFFFFFFFF, int64(ready_idx_out))
			return XqdStatusOK
		}
	}
}

// xqd_async_io_is_ready checks if an async operation is ready (non-blocking).
// Returns 1 if ready, 0 if not ready.
// Handles can be: async item handles, pending request handles, or body handles.
func (i *Instance) xqd_async_io_is_ready(handle int32, is_ready_out int32) int32 {
	i.abilog.Printf("async_io_is_ready: handle=%d", handle)

	ch := i.getHandleChannel(int(handle))
	if ch == nil {
		i.abilog.Printf("async_io_is_ready: invalid handle=%d (not found in any registry)", handle)
		return XqdErrInvalidHandle
	}

	// Non-blocking check if channel is ready
	select {
	case <-ch:
		i.abilog.Printf("async_io_is_ready: handle=%d ready=true", handle)
		i.memory.PutUint32(1, int64(is_ready_out))
	default:
		i.abilog.Printf("async_io_is_ready: handle=%d ready=false", handle)
		i.memory.PutUint32(0, int64(is_ready_out))
	}

	return XqdStatusOK
}

// getAsyncItemChannel returns a channel that closes when the async item is ready.
// The channel type depends on the async item type (pending request, KV operation, cache busy handle, or body).
func (i *Instance) getAsyncItemChannel(item *AsyncItemHandle) <-chan struct{} {
	switch item.Type {
	case AsyncItemTypePendingRequest:
		pr := i.pendingRequests.Get(item.HandleID)
		if pr == nil {
			return nil
		}
		return pr.done

	case AsyncItemTypeKVLookup:
		kv := i.kvLookups.Get(item.HandleID)
		if kv == nil {
			return nil
		}
		return kv.done

	case AsyncItemTypeKVInsert:
		kv := i.kvInserts.Get(item.HandleID)
		if kv == nil {
			return nil
		}
		return kv.done

	case AsyncItemTypeKVDelete:
		kv := i.kvDeletes.Get(item.HandleID)
		if kv == nil {
			return nil
		}
		return kv.done

	case AsyncItemTypeKVList:
		kv := i.kvLists.Get(item.HandleID)
		if kv == nil {
			return nil
		}
		return kv.done

	case AsyncItemTypeCacheBusy:
		// Cache busy handles complete when the transaction is ready
		cb := i.cacheBusyHandles.Get(item.HandleID)
		if cb == nil || cb.Transaction == nil {
			return nil
		}
		return cb.Transaction.ready

	case AsyncItemTypeBody:
		body := i.bodies.Get(item.HandleID)
		if body == nil {
			return nil
		}
		return i.getBodyChannel(body)

	default:
		return nil
	}
}

// isAsyncItemReady checks if an async item is ready without blocking.
// Returns true if the operation has completed, false otherwise.
func (i *Instance) isAsyncItemReady(item *AsyncItemHandle) bool {
	ch := i.getAsyncItemChannel(item)
	if ch == nil {
		return false
	}

	// Non-blocking check
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// getHandleChannel returns a completion channel for any type of handle.
// Tries to resolve the handle as: async item, pending request, or body handle.
// Returns nil if the handle is invalid or not recognized.
func (i *Instance) getHandleChannel(handle int) <-chan struct{} {
	// Try as async item handle first
	asyncItem := i.asyncItems.Get(handle)
	if asyncItem != nil {
		return i.getAsyncItemChannel(asyncItem)
	}

	// Try as pending request handle
	pr := i.pendingRequests.Get(handle)
	if pr != nil {
		return pr.done
	}

	// Try as body handle (for streaming writes)
	body := i.bodies.Get(handle)
	if body != nil {
		return i.getBodyChannel(body)
	}

	return nil
}

// getBodyChannel returns a completion channel for a body handle.
// For streaming bodies, the channel closes when the body has capacity.
// For non-streaming bodies, returns an already-closed channel (always ready).
func (i *Instance) getBodyChannel(body *BodyHandle) <-chan struct{} {
	if body.IsStreaming() {
		// For streaming bodies, check if channel has capacity
		if body.IsStreamingReady() {
			ch := make(chan struct{})
			close(ch)
			return ch
		}
		// Not ready - return a sentinel channel that never closes
		// In practice, the guest should poll is_ready
		return neverReadyChan
	}

	// Non-streaming bodies are always ready
	ch := make(chan struct{})
	close(ch)
	return ch
}

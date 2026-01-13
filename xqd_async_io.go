package fastlike

import (
	"reflect"
	"time"
)

// xqd_async_io_select waits for one of multiple async operations to complete.
// Monitors a list of async handles and returns the index of the first one that becomes ready.
// Returns the index (0-based) of the ready item, or u32::MAX (0xFFFFFFFF) on timeout.
//
// The function supports async items (KV operations, cache operations), pending requests, and body handles.
// Uses reflect.Select to properly wait on multiple channels without goroutine leaks.
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

	// Build select cases for reflect.Select
	// Map from select case index to original handle index
	handleIndexes := make([]int, 0, len(handles))
	selectCases := make([]reflect.SelectCase, 0, len(handles)+1)

	for idx, handle := range handles {
		ch := i.getHandleChannel(int(handle))
		if ch == nil {
			i.abilog.Printf("async_io_select: invalid handle=%d at index=%d", handle, idx)
			return XqdErrInvalidHandle
		}

		selectCases = append(selectCases, reflect.SelectCase{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(ch),
		})
		handleIndexes = append(handleIndexes, idx)
	}

	// Add timeout case if timeout is specified
	var timeoutCh <-chan time.Time
	if timeout_ms > 0 {
		timeoutCh = time.After(time.Duration(uint32(timeout_ms)) * time.Millisecond)
		selectCases = append(selectCases, reflect.SelectCase{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(timeoutCh),
		})
	}

	// Pause CPU time tracking while waiting
	i.pauseExecution()
	defer i.resumeExecution()

	// Use reflect.Select to wait on all channels without goroutine leaks
	chosen, _, _ := reflect.Select(selectCases)

	// Check if it was the timeout case
	if timeout_ms > 0 && chosen == len(handleIndexes) {
		i.abilog.Printf("async_io_select: timeout expired")
		i.memory.PutUint32(0xFFFFFFFF, int64(ready_idx_out))
		return XqdStatusOK
	}

	// Return the original handle index
	handleIdx := handleIndexes[chosen]
	i.abilog.Printf("async_io_select: handle at index %d completed", handleIdx)
	i.memory.PutUint32(uint32(handleIdx), int64(ready_idx_out))
	return XqdStatusOK
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

	// Non-blocking check if channel is ready (closed channels return immediately)
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
// For streaming bodies, returns the body's streaming done channel.
// For non-streaming bodies, returns an already-closed channel (always ready).
func (i *Instance) getBodyChannel(body *BodyHandle) <-chan struct{} {
	if body.IsStreaming() {
		// For streaming bodies, return the streaming done channel
		// This channel is closed when streaming completes or has capacity
		if body.streamingDone != nil {
			return body.streamingDone
		}
		// If no done channel, check if ready now
		if body.IsStreamingReady() {
			ch := make(chan struct{})
			close(ch)
			return ch
		}
		// Streaming body without done channel and not ready - return nil
		// This will cause the handle to be treated as invalid
		return nil
	}

	// Non-streaming bodies are always ready
	ch := make(chan struct{})
	close(ch)
	return ch
}

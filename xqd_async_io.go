package fastlike

import (
	"time"
)

// xqd_async_io_select waits for one of multiple async operations to complete
// Returns the index of the first ready item, or u32::MAX (0xFFFFFFFF) on timeout
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
		var ch <-chan struct{}

		// Try as async item handle first
		asyncItem := i.asyncItems.Get(int(handle))
		if asyncItem != nil {
			ch = i.getAsyncItemChannel(asyncItem)
		} else {
			// Try as pending request handle
			pr := i.pendingRequests.Get(int(handle))
			if pr != nil {
				ch = pr.done
			} else {
				// Try as body handle (for streaming writes)
				body := i.bodies.Get(int(handle))
				if body != nil {
					if body.IsStreaming() {
						// For streaming bodies, check if channel has capacity
						if body.IsStreamingReady() {
							bodyCh := make(chan struct{})
							close(bodyCh)
							ch = bodyCh
						} else {
							// Not ready - create a channel that never closes
							// In practice, the guest should poll is_ready
							ch = make(chan struct{})
						}
					} else {
						// Non-streaming bodies are always ready
						bodyCh := make(chan struct{})
						close(bodyCh)
						ch = bodyCh
					}
				}
			}
		}

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

	// None are ready yet, use select with timeout
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

// xqd_async_io_is_ready checks if an async operation is ready (non-blocking)
// Returns 1 if ready, 0 if not
// Handles can be: async item handles, pending request handles, or body handles
func (i *Instance) xqd_async_io_is_ready(handle int32, is_ready_out int32) int32 {
	i.abilog.Printf("async_io_is_ready: handle=%d", handle)

	// Try as async item handle first
	asyncItem := i.asyncItems.Get(int(handle))
	if asyncItem != nil {
		ready := i.isAsyncItemReady(asyncItem)
		i.abilog.Printf("async_io_is_ready: async item handle=%d ready=%v", handle, ready)
		if ready {
			i.memory.PutUint32(1, int64(is_ready_out))
		} else {
			i.memory.PutUint32(0, int64(is_ready_out))
		}
		return XqdStatusOK
	}

	// Try as pending request handle
	pr := i.pendingRequests.Get(int(handle))
	if pr != nil {
		ready := pr.IsReady()
		i.abilog.Printf("async_io_is_ready: pending request handle=%d ready=%v", handle, ready)
		if ready {
			i.memory.PutUint32(1, int64(is_ready_out))
		} else {
			i.memory.PutUint32(0, int64(is_ready_out))
		}
		return XqdStatusOK
	}

	// Try as body handle (for streaming writes)
	body := i.bodies.Get(int(handle))
	if body != nil {
		if body.IsStreaming() {
			// For streaming bodies, check if channel has capacity
			if body.IsStreamingReady() {
				i.abilog.Printf("async_io_is_ready: streaming body handle=%d ready=true", handle)
				i.memory.PutUint32(1, int64(is_ready_out))
			} else {
				i.abilog.Printf("async_io_is_ready: streaming body handle=%d ready=false (backpressure)", handle)
				i.memory.PutUint32(0, int64(is_ready_out))
			}
		} else {
			// Non-streaming bodies are always ready
			i.abilog.Printf("async_io_is_ready: body handle=%d ready=true (non-streaming)", handle)
			i.memory.PutUint32(1, int64(is_ready_out))
		}
		return XqdStatusOK
	}

	// Handle not found in any registry
	i.abilog.Printf("async_io_is_ready: invalid handle=%d (not found in any registry)", handle)
	return XqdErrInvalidHandle
}

// getAsyncItemChannel returns a channel that closes when the async item is ready
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
		if body.IsStreaming() {
			// For streaming bodies, check if channel has capacity
			// We create a channel that closes immediately if ready, or blocks if not
			if body.IsStreamingReady() {
				ch := make(chan struct{})
				close(ch)
				return ch
			} else {
				// Return a channel that never closes (indicating not ready)
				// In practice, the caller should re-check is_ready periodically
				return make(chan struct{})
			}
		}
		// Non-streaming bodies are always ready
		ch := make(chan struct{})
		close(ch)
		return ch

	default:
		return nil
	}
}

// isAsyncItemReady checks if an async item is ready (non-blocking)
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

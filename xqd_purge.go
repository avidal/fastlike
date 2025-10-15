package fastlike

import (
	"encoding/json"
	"fmt"
	"time"
)

// PurgeOptions represents the options structure for cache purge operations.
// It contains pointers and lengths for an optional JSON response buffer.
type PurgeOptions struct {
	RetBufPtr         int32
	RetBufLen         uint32
	RetBufNwrittenOut int32
}

// xqd_purge_surrogate_key purges all cache entries with the given surrogate key.
// This is the modern XQD ABI that takes an options mask and options struct.
// Supports both hard purge (immediate invalidation) and soft purge (mark as stale).
// Optionally returns a JSON response with purge ID and status.
func (i *Instance) xqd_purge_surrogate_key(
	surrogate_key_ptr int32,
	surrogate_key_len int32,
	options_mask uint32,
	options_ptr int32,
) int32 {
	i.abilog.Println("xqd_purge_surrogate_key")

	// Read surrogate key
	keyBuf := make([]byte, surrogate_key_len)
	_, _ = i.memory.ReadAt(keyBuf, int64(surrogate_key_ptr))
	key := string(keyBuf)

	// Check if soft purge flag is set (marks entries as stale vs immediate invalidation)
	softPurge := (options_mask & PurgeOptionsSoftPurge) != 0

	// Perform the appropriate purge operation
	if softPurge {
		i.cache.SoftPurgeSurrogateKey(key) // Mark as stale, allow stale-while-revalidate
	} else {
		i.cache.PurgeSurrogateKey(key) // Immediate invalidation
	}

	// If ret_buf flag is set, write JSON response to guest memory
	if (options_mask & PurgeOptionsRetBuf) != 0 {
		// Read options struct fields
		var opts PurgeOptions
		opts.RetBufPtr = int32(i.memory.ReadUint32(options_ptr))
		opts.RetBufLen = i.memory.ReadUint32(options_ptr + 4)
		opts.RetBufNwrittenOut = int32(i.memory.ReadUint32(options_ptr + 8))

		// Generate JSON response matching Fastly's purge API format
		// See: https://developer.fastly.com/reference/api/purging/#purge-tag
		response := map[string]interface{}{
			"id":     fmt.Sprintf("purge-%d", time.Now().UnixNano()),
			"status": "ok",
		}

		jsonBytes, err := json.Marshal(response)
		if err != nil {
			i.abilog.Printf("failed to marshal purge response: %v", err)
			return XqdError
		}

		// Check if buffer is large enough
		if uint32(len(jsonBytes)) > opts.RetBufLen {
			// Write required size
			i.memory.WriteUint32(opts.RetBufNwrittenOut, uint32(len(jsonBytes)))
			return XqdErrBufferLength
		}

		// Write JSON response to buffer
		_, _ = i.memory.WriteAt(jsonBytes, int64(opts.RetBufPtr))

		// Write number of bytes written
		i.memory.WriteUint32(opts.RetBufNwrittenOut, uint32(len(jsonBytes)))
	}

	return XqdStatusOK
}

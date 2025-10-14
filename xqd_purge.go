package fastlike

import (
	"encoding/json"
	"fmt"
	"time"
)

// PurgeOptions represents the options struct for purge operations
type PurgeOptions struct {
	RetBufPtr          int32
	RetBufLen          uint32
	RetBufNwrittenOut int32
}

// xqd_purge_surrogate_key purges all cache entries with the given surrogate key
// This is the modern API that takes options mask and options struct
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

	// Check if soft purge is requested
	softPurge := (options_mask & PurgeOptionsSoftPurge) != 0

	// Perform purge
	if softPurge {
		i.cache.SoftPurgeSurrogateKey(key)
	} else {
		i.cache.PurgeSurrogateKey(key)
	}

	// If ret_buf flag is set, return JSON response
	if (options_mask & PurgeOptionsRetBuf) != 0 {
		// Read options struct
		var opts PurgeOptions
		opts.RetBufPtr = int32(i.memory.ReadUint32(options_ptr))
		opts.RetBufLen = i.memory.ReadUint32(options_ptr + 4)
		opts.RetBufNwrittenOut = int32(i.memory.ReadUint32(options_ptr + 8))

		// Generate JSON response
		// Format matches Fastly's purge API: https://developer.fastly.com/reference/api/purging/#purge-tag
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

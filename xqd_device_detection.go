package fastlike

func (i *Instance) xqd_device_detection_lookup(
	user_agent_addr int32,
	user_agent_size int32,
	buf_addr int32,
	buf_len int32,
	nwritten_out int32,
) int32 {
	// Read user agent string from memory
	buf := make([]byte, user_agent_size)
	_, err := i.memory.ReadAt(buf, int64(user_agent_addr))
	if err != nil {
		i.abilog.Printf("device_detection_lookup: read user agent err, got %s", err.Error())
		return XqdError
	}

	userAgent := string(buf)
	i.abilog.Printf("device_detection_lookup: user_agent=%s\n", userAgent)

	// Call the configured device detection function
	result := i.deviceDetection(userAgent)

	// If no result, return None status
	if result == "" {
		i.abilog.Printf("device_detection_lookup: no data for user agent")
		return XqdErrNone
	}

	// Check if the result fits in the provided buffer
	if len(result) > int(buf_len) {
		i.memory.PutUint32(uint32(len(result)), int64(nwritten_out))
		i.abilog.Printf("device_detection_lookup: buffer too small, needed %d, got %d", len(result), buf_len)
		return XqdErrBufferLength
	}

	// Write the result to memory
	nwritten, err := i.memory.WriteAt([]byte(result), int64(buf_addr))
	if err != nil {
		i.abilog.Printf("device_detection_lookup: write err, got %s", err.Error())
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))

	return XqdStatusOK
}

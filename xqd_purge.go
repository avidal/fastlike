package fastlike

// xqd_purge_surrogate_key purges all cache entries with the given surrogate key
func (i *Instance) xqd_purge_surrogate_key(
	surrogate_key_ptr int32,
	surrogate_key_len int32,
) int32 {
	i.abilog.Println("xqd_purge_surrogate_key")

	keyBuf := make([]byte, surrogate_key_len)
	_, _ = i.memory.ReadAt(keyBuf, int64(surrogate_key_ptr))
	key := string(keyBuf)

	i.cache.PurgeSurrogateKey(key)

	return XqdStatusOK
}

// xqd_soft_purge_surrogate_key marks cache entries as stale without removing them
func (i *Instance) xqd_soft_purge_surrogate_key(
	surrogate_key_ptr int32,
	surrogate_key_len int32,
) int32 {
	i.abilog.Println("xqd_soft_purge_surrogate_key")

	keyBuf := make([]byte, surrogate_key_len)
	_, _ = i.memory.ReadAt(keyBuf, int64(surrogate_key_ptr))
	key := string(keyBuf)

	i.cache.SoftPurgeSurrogateKey(key)

	return XqdStatusOK
}

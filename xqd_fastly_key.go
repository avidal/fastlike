package fastlike

import "net/http"

// fastlyKeyHeader is the request header carrying the Fastly API token that
// fastly_key_is_valid validates against the configured fake key set.
const fastlyKeyHeader = "Fastly-Key"

// fastlyKeyValid reports whether r carries a Fastly-Key header whose value is
// in the configured fake key set. Both fastly_key_is_valid hostcalls share it.
// An empty header or an empty configured set is never valid; empty keys are
// dropped at configuration time, so an empty Fastly-Key cannot match.
func (i *Instance) fastlyKeyValid(r *http.Request) bool {
	if r == nil || len(i.fakeValidFastlyKeys) == 0 {
		return false
	}
	key := r.Header.Get(fastlyKeyHeader)
	if key == "" {
		return false
	}
	_, ok := i.fakeValidFastlyKeys[key]
	return ok
}

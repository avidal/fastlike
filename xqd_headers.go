package fastlike

const (
	maxHTTPHeaderNameLen   int32 = 1 << 15
	maxHTTPHeaderNameCount       = (1 << 14) - 1000
)

func validHTTPHeaderNameSize(size int32) bool {
	return size >= 0 && size <= maxHTTPHeaderNameLen
}

func httpHeaderNameCountAtLimit(count int) bool {
	return count >= maxHTTPHeaderNameCount
}

// validHTTPHeaderName matches the token grammar accepted by
// http::HeaderName::from_bytes in the server runtime.
func validHTTPHeaderName(name []byte) bool {
	if len(name) == 0 {
		return false
	}
	for _, b := range name {
		if !isTokenChar(b) {
			return false
		}
	}
	return true
}

func isTokenChar(b byte) bool {
	if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' {
		return true
	}
	switch b {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

// validHTTPHeaderValue matches http::HeaderValue::from_bytes: horizontal tab,
// bytes from space upward, except DEL, are accepted.
func validHTTPHeaderValue(value []byte) bool {
	for _, b := range value {
		if !isFieldValueChar(b) {
			return false
		}
	}
	return true
}

func isFieldValueChar(b byte) bool {
	return b == '\t' || b >= ' ' && b != 0x7f
}

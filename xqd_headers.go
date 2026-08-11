package fastlike

const maxHTTPHeaderNameLen int32 = 1 << 15

func validHTTPHeaderNameSize(size int32) bool {
	return size >= 0 && size <= maxHTTPHeaderNameLen
}

// validHTTPHeaderName matches the token grammar accepted by
// http::HeaderName::from_bytes in the server runtime.
func validHTTPHeaderName(name []byte) bool {
	if len(name) == 0 {
		return false
	}
	for _, b := range name {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' {
			continue
		}
		switch b {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

// validHTTPHeaderValue matches http::HeaderValue::from_bytes: horizontal tab,
// bytes from space upward, except DEL, are accepted.
func validHTTPHeaderValue(value []byte) bool {
	for _, b := range value {
		if b == '\t' || b >= ' ' && b != 0x7f {
			continue
		}
		return false
	}
	return true
}

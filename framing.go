package fastlike

import (
	"net/http"
	"strings"
)

// contentLengthIsValid checks if the Content-Length header is valid for manual framing.
// Returns true if there is exactly one Content-Length value and it contains only ASCII digits.
func contentLengthIsValid(headers http.Header) bool {
	values := headers.Values("Content-Length")
	if len(values) != 1 {
		return false
	}
	// Must have at least one digit
	if len(values[0]) == 0 {
		return false
	}
	for _, b := range values[0] {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

// transferEncodingIsSupported checks if the Transfer-Encoding header is supported for manual framing.
// Returns true if there is exactly one Transfer-Encoding value and it equals "chunked" (case-insensitive).
func transferEncodingIsSupported(headers http.Header) bool {
	values := headers.Values("Transfer-Encoding")
	if len(values) != 1 {
		return false
	}
	return strings.EqualFold(values[0], "chunked")
}

// filterFramingHeaders removes Content-Length and Transfer-Encoding headers from the header map.
// This is used when automatic framing mode is active to let the HTTP library set these headers.
func filterFramingHeaders(headers http.Header) {
	headers.Del("Content-Length")
	headers.Del("Transfer-Encoding")
}

// validateAndApplyFramingMode validates framing headers and determines if manual mode can be used.
// If manual mode is requested but headers are invalid, it falls back to automatic mode.
// Returns the effective framing mode and whether headers were filtered.
func validateAndApplyFramingMode(headers http.Header, mode FramingHeadersMode, logger func(format string, args ...interface{})) FramingHeadersMode {
	if mode != FramingHeadersModeManuallyFromHeaders {
		// Automatic mode: filter framing headers
		filterFramingHeaders(headers)
		return FramingHeadersModeAutomatic
	}

	// Manual mode requested: validate headers
	hasContentLength := headers.Get("Content-Length") != ""
	hasTransferEncoding := headers.Get("Transfer-Encoding") != ""

	if hasContentLength && !contentLengthIsValid(headers) {
		if logger != nil {
			logger("invalid Content-Length header, falling back to automatic framing")
		}
		filterFramingHeaders(headers)
		return FramingHeadersModeAutomatic
	}

	if hasTransferEncoding && !transferEncodingIsSupported(headers) {
		if logger != nil {
			logger("unsupported Transfer-Encoding header, falling back to automatic framing")
		}
		filterFramingHeaders(headers)
		return FramingHeadersModeAutomatic
	}

	if !hasContentLength && !hasTransferEncoding {
		if logger != nil {
			logger("missing Content-Length and Transfer-Encoding headers, falling back to automatic framing")
		}
		filterFramingHeaders(headers)
		return FramingHeadersModeAutomatic
	}

	// Manual mode with valid framing headers
	return FramingHeadersModeManuallyFromHeaders
}

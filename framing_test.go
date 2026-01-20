package fastlike

import (
	"net/http"
	"testing"
)

func TestContentLengthIsValid(t *testing.T) {
	tests := []struct {
		name     string
		headers  http.Header
		expected bool
	}{
		{
			name:     "valid single content-length",
			headers:  http.Header{"Content-Length": []string{"123"}},
			expected: true,
		},
		{
			name:     "valid zero content-length",
			headers:  http.Header{"Content-Length": []string{"0"}},
			expected: true,
		},
		{
			name:     "valid large content-length",
			headers:  http.Header{"Content-Length": []string{"9999999999"}},
			expected: true,
		},
		{
			name:     "invalid - multiple content-length values",
			headers:  http.Header{"Content-Length": []string{"123", "456"}},
			expected: false,
		},
		{
			name:     "invalid - contains non-digits",
			headers:  http.Header{"Content-Length": []string{"123abc"}},
			expected: false,
		},
		{
			name:     "invalid - contains spaces",
			headers:  http.Header{"Content-Length": []string{"123 "}},
			expected: false,
		},
		{
			name:     "invalid - negative number",
			headers:  http.Header{"Content-Length": []string{"-123"}},
			expected: false,
		},
		{
			name:     "invalid - empty value",
			headers:  http.Header{"Content-Length": []string{""}},
			expected: false,
		},
		{
			name:     "invalid - no content-length header",
			headers:  http.Header{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := contentLengthIsValid(tt.headers)
			if result != tt.expected {
				t.Errorf("contentLengthIsValid() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestTransferEncodingIsSupported(t *testing.T) {
	tests := []struct {
		name     string
		headers  http.Header
		expected bool
	}{
		{
			name:     "valid - chunked lowercase",
			headers:  http.Header{"Transfer-Encoding": []string{"chunked"}},
			expected: true,
		},
		{
			name:     "valid - chunked uppercase",
			headers:  http.Header{"Transfer-Encoding": []string{"CHUNKED"}},
			expected: true,
		},
		{
			name:     "valid - chunked mixed case",
			headers:  http.Header{"Transfer-Encoding": []string{"Chunked"}},
			expected: true,
		},
		{
			name:     "invalid - gzip encoding",
			headers:  http.Header{"Transfer-Encoding": []string{"gzip"}},
			expected: false,
		},
		{
			name:     "invalid - multiple values",
			headers:  http.Header{"Transfer-Encoding": []string{"chunked", "gzip"}},
			expected: false,
		},
		{
			name:     "invalid - chunked,gzip combined",
			headers:  http.Header{"Transfer-Encoding": []string{"chunked, gzip"}},
			expected: false,
		},
		{
			name:     "invalid - empty value",
			headers:  http.Header{"Transfer-Encoding": []string{""}},
			expected: false,
		},
		{
			name:     "invalid - no transfer-encoding header",
			headers:  http.Header{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := transferEncodingIsSupported(tt.headers)
			if result != tt.expected {
				t.Errorf("transferEncodingIsSupported() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestFilterFramingHeaders(t *testing.T) {
	tests := []struct {
		name            string
		headers         http.Header
		expectedHeaders http.Header
	}{
		{
			name: "removes content-length",
			headers: http.Header{
				"Content-Length": []string{"123"},
				"Content-Type":   []string{"text/plain"},
			},
			expectedHeaders: http.Header{
				"Content-Type": []string{"text/plain"},
			},
		},
		{
			name: "removes transfer-encoding",
			headers: http.Header{
				"Transfer-Encoding": []string{"chunked"},
				"Content-Type":      []string{"text/plain"},
			},
			expectedHeaders: http.Header{
				"Content-Type": []string{"text/plain"},
			},
		},
		{
			name: "removes both framing headers",
			headers: http.Header{
				"Content-Length":    []string{"123"},
				"Transfer-Encoding": []string{"chunked"},
				"Content-Type":      []string{"text/plain"},
			},
			expectedHeaders: http.Header{
				"Content-Type": []string{"text/plain"},
			},
		},
		{
			name: "preserves other headers",
			headers: http.Header{
				"X-Custom-Header": []string{"value"},
				"Content-Type":    []string{"application/json"},
			},
			expectedHeaders: http.Header{
				"X-Custom-Header": []string{"value"},
				"Content-Type":    []string{"application/json"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filterFramingHeaders(tt.headers)

			// Check that expected headers are present
			for key, values := range tt.expectedHeaders {
				if got := tt.headers[key]; len(got) != len(values) {
					t.Errorf("header %q: got %v, want %v", key, got, values)
				}
			}

			// Check that framing headers are removed
			if tt.headers.Get("Content-Length") != "" {
				t.Error("Content-Length header was not removed")
			}
			if tt.headers.Get("Transfer-Encoding") != "" {
				t.Error("Transfer-Encoding header was not removed")
			}
		})
	}
}

func TestValidateAndApplyFramingMode(t *testing.T) {
	tests := []struct {
		name                   string
		headers                http.Header
		mode                   FramingHeadersMode
		expectedMode           FramingHeadersMode
		expectContentLength    bool
		expectTransferEncoding bool
	}{
		{
			name: "automatic mode - filters framing headers",
			headers: http.Header{
				"Content-Length": []string{"123"},
				"Content-Type":   []string{"text/plain"},
			},
			mode:                   FramingHeadersModeAutomatic,
			expectedMode:           FramingHeadersModeAutomatic,
			expectContentLength:    false,
			expectTransferEncoding: false,
		},
		{
			name: "manual mode - valid content-length preserved",
			headers: http.Header{
				"Content-Length": []string{"123"},
				"Content-Type":   []string{"text/plain"},
			},
			mode:                   FramingHeadersModeManuallyFromHeaders,
			expectedMode:           FramingHeadersModeManuallyFromHeaders,
			expectContentLength:    true,
			expectTransferEncoding: false,
		},
		{
			name: "manual mode - valid transfer-encoding preserved",
			headers: http.Header{
				"Transfer-Encoding": []string{"chunked"},
				"Content-Type":      []string{"text/plain"},
			},
			mode:                   FramingHeadersModeManuallyFromHeaders,
			expectedMode:           FramingHeadersModeManuallyFromHeaders,
			expectContentLength:    false,
			expectTransferEncoding: true,
		},
		{
			name: "manual mode - invalid content-length falls back to automatic",
			headers: http.Header{
				"Content-Length": []string{"abc"},
				"Content-Type":   []string{"text/plain"},
			},
			mode:                   FramingHeadersModeManuallyFromHeaders,
			expectedMode:           FramingHeadersModeAutomatic,
			expectContentLength:    false,
			expectTransferEncoding: false,
		},
		{
			name: "manual mode - multiple content-length falls back to automatic",
			headers: http.Header{
				"Content-Length": []string{"123", "456"},
				"Content-Type":   []string{"text/plain"},
			},
			mode:                   FramingHeadersModeManuallyFromHeaders,
			expectedMode:           FramingHeadersModeAutomatic,
			expectContentLength:    false,
			expectTransferEncoding: false,
		},
		{
			name: "manual mode - unsupported transfer-encoding falls back to automatic",
			headers: http.Header{
				"Transfer-Encoding": []string{"gzip"},
				"Content-Type":      []string{"text/plain"},
			},
			mode:                   FramingHeadersModeManuallyFromHeaders,
			expectedMode:           FramingHeadersModeAutomatic,
			expectContentLength:    false,
			expectTransferEncoding: false,
		},
		{
			name: "manual mode - no framing headers falls back to automatic",
			headers: http.Header{
				"Content-Type": []string{"text/plain"},
			},
			mode:                   FramingHeadersModeManuallyFromHeaders,
			expectedMode:           FramingHeadersModeAutomatic,
			expectContentLength:    false,
			expectTransferEncoding: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clone headers so we don't modify the test case
			headers := tt.headers.Clone()

			var logMessages []string
			logger := func(format string, args ...interface{}) {
				logMessages = append(logMessages, format)
			}

			resultMode := validateAndApplyFramingMode(headers, tt.mode, logger)

			if resultMode != tt.expectedMode {
				t.Errorf("validateAndApplyFramingMode() mode = %v, want %v", resultMode, tt.expectedMode)
			}

			hasContentLength := headers.Get("Content-Length") != ""
			if hasContentLength != tt.expectContentLength {
				t.Errorf("Content-Length presence = %v, want %v", hasContentLength, tt.expectContentLength)
			}

			hasTransferEncoding := headers.Get("Transfer-Encoding") != ""
			if hasTransferEncoding != tt.expectTransferEncoding {
				t.Errorf("Transfer-Encoding presence = %v, want %v", hasTransferEncoding, tt.expectTransferEncoding)
			}

			// Check that fallback to automatic logs a message
			if tt.mode == FramingHeadersModeManuallyFromHeaders && tt.expectedMode == FramingHeadersModeAutomatic {
				if len(logMessages) == 0 {
					t.Error("Expected a log message when falling back to automatic mode")
				}
			}
		})
	}
}

func TestFramingHeadersModeOnHandles(t *testing.T) {
	t.Run("request handle stores framing mode", func(t *testing.T) {
		rhs := &RequestHandles{}
		_, rh := rhs.New()

		// Default should be automatic (0)
		if rh.framingHeadersMode != FramingHeadersModeAutomatic {
			t.Errorf("default framing mode = %v, want %v", rh.framingHeadersMode, FramingHeadersModeAutomatic)
		}

		// Set to manual
		rh.framingHeadersMode = FramingHeadersModeManuallyFromHeaders
		if rh.framingHeadersMode != FramingHeadersModeManuallyFromHeaders {
			t.Errorf("framing mode after set = %v, want %v", rh.framingHeadersMode, FramingHeadersModeManuallyFromHeaders)
		}
	})

	t.Run("response handle stores framing mode", func(t *testing.T) {
		rhs := &ResponseHandles{}
		_, rh := rhs.New()

		// Default should be automatic (0)
		if rh.framingHeadersMode != FramingHeadersModeAutomatic {
			t.Errorf("default framing mode = %v, want %v", rh.framingHeadersMode, FramingHeadersModeAutomatic)
		}

		// Set to manual
		rh.framingHeadersMode = FramingHeadersModeManuallyFromHeaders
		if rh.framingHeadersMode != FramingHeadersModeManuallyFromHeaders {
			t.Errorf("framing mode after set = %v, want %v", rh.framingHeadersMode, FramingHeadersModeManuallyFromHeaders)
		}
	})
}

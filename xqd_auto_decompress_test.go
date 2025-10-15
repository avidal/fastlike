package fastlike

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"testing"
)

func TestAutoDecompression(t *testing.T) {
	tests := []struct {
		name                  string
		encoding              string
		compressBody          bool
		autoDecompressEnabled bool
		expectedDecompressed  bool
	}{
		{
			name:                  "gzip with auto-decompress enabled",
			encoding:              "gzip",
			compressBody:          true,
			autoDecompressEnabled: true,
			expectedDecompressed:  true,
		},
		{
			name:                  "gzip with auto-decompress disabled",
			encoding:              "gzip",
			compressBody:          true,
			autoDecompressEnabled: false,
			expectedDecompressed:  false,
		},
		{
			name:                  "x-gzip with auto-decompress enabled",
			encoding:              "x-gzip",
			compressBody:          true,
			autoDecompressEnabled: true,
			expectedDecompressed:  true,
		},
		{
			name:                  "no encoding with auto-decompress enabled",
			encoding:              "",
			compressBody:          false,
			autoDecompressEnabled: true,
			expectedDecompressed:  false,
		},
		{
			name:                  "identity encoding with auto-decompress enabled",
			encoding:              "identity",
			compressBody:          false,
			autoDecompressEnabled: true,
			expectedDecompressed:  false,
		},
	}

	originalBody := []byte("Hello, this is the original body content that will be compressed!")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create the response body
			var body []byte
			if tt.compressBody {
				var buf bytes.Buffer
				gzWriter := gzip.NewWriter(&buf)
				_, err := gzWriter.Write(originalBody)
				if err != nil {
					t.Fatalf("Failed to compress body: %v", err)
				}
				_ = gzWriter.Close()
				body = buf.Bytes()
			} else {
				body = originalBody
			}

			// Create the response
			resp := &http.Response{
				StatusCode: 200,
				Header:     http.Header{},
				Body:       io.NopCloser(bytes.NewReader(body)),
			}

			if tt.encoding != "" {
				resp.Header.Set("Content-Encoding", tt.encoding)
			}
			resp.Header.Set("Content-Length", string(rune(len(body))))

			// Apply auto-decompression
			var encodings uint32 = 0
			if tt.autoDecompressEnabled {
				encodings = ContentEncodingsGzip
			}

			err := applyAutoDecompression(resp, encodings)
			if err != nil && tt.expectedDecompressed {
				t.Fatalf("applyAutoDecompression failed: %v", err)
			}

			// Read the response body
			resultBody, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("Failed to read response body: %v", err)
			}

			// Check if the body was decompressed as expected
			if tt.expectedDecompressed {
				if !bytes.Equal(resultBody, originalBody) {
					t.Errorf("Body was not properly decompressed. Expected %q, got %q", originalBody, resultBody)
				}

				// Check that Content-Encoding header was removed
				if resp.Header.Get("Content-Encoding") != "" {
					t.Errorf("Content-Encoding header was not removed after decompression")
				}

				// Check that Content-Length header was removed
				if resp.Header.Get("Content-Length") != "" {
					t.Errorf("Content-Length header was not removed after decompression")
				}
			} else {
				// Body should remain compressed or unchanged
				if bytes.Equal(resultBody, originalBody) && tt.compressBody {
					t.Errorf("Body was decompressed when it should have remained compressed")
				}
			}
		})
	}
}

func TestAutoDecompressionInvalidGzip(t *testing.T) {
	// Test that invalid gzip data is handled gracefully
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Encoding": []string{"gzip"}},
		Body:       io.NopCloser(bytes.NewReader([]byte("this is not valid gzip data"))),
	}

	// Apply auto-decompression
	err := applyAutoDecompression(resp, ContentEncodingsGzip)

	// Should return an error but not crash
	if err == nil {
		t.Error("Expected error for invalid gzip data")
	}

	// Response should still be readable
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	// Body should be the original (invalid) gzip data
	if string(body) != "this is not valid gzip data" {
		t.Errorf("Unexpected body content: %q", body)
	}

	// Content-Encoding header should be removed even if decompression failed
	if resp.Header.Get("Content-Encoding") != "" {
		t.Errorf("Content-Encoding header was not removed after failed decompression")
	}
}

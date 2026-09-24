package fastlike

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"testing"
	"testing/iotest"
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
			body := originalBody
			if tt.compressBody {
				body = gzipBytes(originalBody)
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
			resp.Header.Set("Content-Length", strconv.Itoa(len(body)))

			// Apply auto-decompression
			var encodings uint32 = 0
			if tt.autoDecompressEnabled {
				encodings = ContentEncodingsGzip
			}

			applyAutoDecompression(resp, encodings)

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
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Encoding": []string{"gzip"}},
		Body:       io.NopCloser(bytes.NewReader([]byte("this is not valid gzip data"))),
	}
	applyAutoDecompression(resp, ContentEncodingsGzip)

	if resp.Header.Get("Content-Encoding") != "" {
		t.Errorf("Content-Encoding header was not removed")
	}
	// Production reports corrupt data as a generic read error.
	_, err := io.ReadAll(resp.Body)
	if err == nil || bodyReadStatus(err) != XqdError {
		t.Fatalf("reading a corrupt gzip body: err = %v, want a generic error", err)
	}
}

// Production never checks the end of the gzip stream, so a truncated stream
// or a bad checksum just ends the body.
func TestAutoDecompressionIgnoresStreamEnd(t *testing.T) {
	compressed := gzipBytes(bytes.Repeat([]byte("abcdefgh"), 1024))

	badChecksum := bytes.Clone(compressed)
	badChecksum[len(badChecksum)-8] ^= 0xff
	for name, body := range map[string][]byte{
		"truncated":    compressed[:len(compressed)/2],
		"bad checksum": badChecksum,
		"empty":        nil,
	} {
		resp := &http.Response{
			Header: http.Header{"Content-Encoding": []string{"gzip"}},
			Body:   io.NopCloser(bytes.NewReader(body)),
		}
		applyAutoDecompression(resp, ContentEncodingsGzip)
		if _, err := io.ReadAll(resp.Body); err != nil {
			t.Errorf("%s: read error %v, want a clean end", name, err)
		}
	}
}

// A failure of the compressed body itself stays visible through the
// decompressor.
func TestAutoDecompressionKeepsSourceFailure(t *testing.T) {
	compressed := gzipBytes(bytes.Repeat([]byte("abcdefgh"), 1024))
	truncated := &backendBodyError{err: io.ErrUnexpectedEOF}
	resp := &http.Response{
		Header: http.Header{"Content-Encoding": []string{"x-gzip"}},
		Body:   io.NopCloser(io.MultiReader(bytes.NewReader(compressed[:len(compressed)/2]), iotest.ErrReader(truncated))),
	}
	applyAutoDecompression(resp, ContentEncodingsGzip)
	_, err := io.ReadAll(resp.Body)
	if bodyReadStatus(err) != XqdErrHttpIncomplete {
		t.Fatalf("read error %v, want the incomplete source", err)
	}
}

func gzipBytes(data []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(data)
	_ = zw.Close()
	return buf.Bytes()
}

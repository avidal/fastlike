package profile

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenPath returns the on-disk path for a named golden fixture.
// All goldens live under testdata/golden/.
func goldenPath(name string) string {
	return filepath.Join("testdata", "golden", name)
}

// assertGoldenJSON compares actual JSON bytes against the golden file at
// testdata/golden/{name}. When the environment variable UPDATE_GOLDEN=1
// is set, the file is rewritten with the actual bytes instead of
// failing — this is the documented escape hatch when an encoder is
// intentionally evolved. Without UPDATE_GOLDEN the test reports a unified
// diff hint via the failure message so the reviewer can spot the drift.
//
// Both sides are re-encoded through json.Indent so whitespace
// differences (and the JSON encoder's HTML escaping defaults) are
// normalised before comparison; the goldens themselves stay
// human-readable.
func assertGoldenJSON(t *testing.T, name string, actual []byte) {
	t.Helper()
	path := goldenPath(name)

	var actualPretty bytes.Buffer
	if err := json.Indent(&actualPretty, actual, "", "  "); err != nil {
		t.Fatalf("golden %s: actual is not valid JSON: %v\nraw=%s", name, err, actual)
	}
	// Trailing newline so the file ends cleanly when written through
	// os.WriteFile and read back by a text editor.
	actualPretty.WriteByte('\n')

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("golden %s: mkdir: %v", name, err)
		}
		if err := os.WriteFile(path, actualPretty.Bytes(), 0o644); err != nil {
			t.Fatalf("golden %s: write: %v", name, err)
		}
		t.Logf("golden %s: updated", name)
		return
	}

	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s: read: %v (run with UPDATE_GOLDEN=1 to create)", name, err)
	}
	if !bytes.Equal(actualPretty.Bytes(), expected) {
		t.Fatalf("golden %s: mismatch.\nFirst diff context:\n%s\n\n(re-run with UPDATE_GOLDEN=1 to overwrite if the change is intentional)",
			name, firstDiff(string(expected), actualPretty.String()))
	}
}

// firstDiff returns a small slice of context around the first byte that
// differs between expected and actual. Keeps test output readable when
// goldens drift, without dumping the entire file.
func firstDiff(expected, actual string) string {
	n := len(expected)
	if len(actual) < n {
		n = len(actual)
	}
	idx := -1
	for i := 0; i < n; i++ {
		if expected[i] != actual[i] {
			idx = i
			break
		}
	}
	if idx == -1 {
		// One side is a prefix of the other.
		return "files differ in length: expected=" +
			itoa(len(expected)) + " actual=" + itoa(len(actual))
	}
	start := idx - 40
	if start < 0 {
		start = 0
	}
	end := idx + 80
	if end > len(expected) {
		end = len(expected)
	}
	endA := idx + 80
	if endA > len(actual) {
		endA = len(actual)
	}
	var b strings.Builder
	b.WriteString("byte ")
	b.WriteString(itoa(idx))
	b.WriteString(":\n  expected: ")
	b.WriteString(quoteFragment(expected[start:end]))
	b.WriteString("\n  actual:   ")
	b.WriteString(quoteFragment(actual[start:endA]))
	return b.String()
}

func quoteFragment(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		switch r {
		case '\n':
			out = append(out, '\\', 'n')
		case '\t':
			out = append(out, '\\', 't')
		default:
			out = append(out, string(r)...)
		}
	}
	out = append(out, '"')
	return string(out)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

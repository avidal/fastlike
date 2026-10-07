package fastlike

import (
	"bytes"
	"testing"
)

type recordingWriter struct {
	calls [][]byte
}

func (w *recordingWriter) Write(data []byte) (int, error) {
	w.calls = append(w.calls, append([]byte(nil), data...))
	return len(data), nil
}

func TestLineWriterWritesEachMessageAsOneLineInOneCall(t *testing.T) {
	var out recordingWriter
	lw := LineWriter{&out}

	for _, msg := range []string{"first\n\n", "two\nlines", "third"} {
		n, err := lw.Write([]byte(msg))
		if err != nil || n != len(msg) {
			t.Fatalf("Write(%q) = %d, %v; want %d, nil", msg, n, err, len(msg))
		}
	}

	want := []string{"first\n", "two\\nlines\n", "third\n"}
	if len(out.calls) != len(want) {
		t.Fatalf("got %d writes, want one per message: %q", len(out.calls), out.calls)
	}
	for i, call := range out.calls {
		if !bytes.Equal(call, []byte(want[i])) {
			t.Errorf("write %d = %q, want %q", i, call, want[i])
		}
	}
}

package fastlike

import "testing"

func TestXqdMultivalueFillsAvailableBuffer(t *testing.T) {
	memory := &Memory{ByteMemory(make([]byte, 64))}

	status := xqd_multivalue(memory, []string{"one", "two", "three"}, 0, 8, 0, 32, 40)
	if status != XqdStatusOK {
		t.Fatalf("status = %d, want %d", status, XqdStatusOK)
	}
	if got := memory.Uint32(40); got != 8 {
		t.Fatalf("nwritten = %d, want 8", got)
	}
	if got := string(memory.Data()[:8]); got != "one\x00two\x00" {
		t.Fatalf("buffer = %q, want %q", got, "one\x00two\x00")
	}
	if got := int64(memory.Uint64(32)); got != 2 {
		t.Fatalf("ending cursor = %d, want 2", got)
	}
}

func TestXqdMultivalueReportsRequiredBufferLength(t *testing.T) {
	memory := &Memory{ByteMemory(make([]byte, 64))}
	memory.PutUint64(123, 32)
	memory.PutUint32(0xdeadbeef, 40)

	status := xqd_multivalue(memory, []string{"four"}, 0, 4, 0, 32, 40)
	if status != XqdErrBufferLength {
		t.Fatalf("status = %d, want %d", status, XqdErrBufferLength)
	}
	if got := memory.Uint32(40); got != 5 {
		t.Fatalf("required buffer length = %d, want 5", got)
	}
	if got := int64(memory.Uint64(32)); got != cursorEnd {
		t.Fatalf("ending cursor on buffer error = %d, want %d", got, cursorEnd)
	}
}

func TestXqdMultivalueAllowsOmittedEndingCursor(t *testing.T) {
	memory := &Memory{ByteMemory(make([]byte, 64))}
	copy(memory.Data()[:8], []byte("sentinel"))

	status := xqd_multivalue(memory, []string{"one"}, 16, 4, 0, 0, 40)
	if status != XqdStatusOK {
		t.Fatalf("status = %d, want %d", status, XqdStatusOK)
	}
	if got := string(memory.Data()[:8]); got != "sentinel" {
		t.Fatalf("null ending cursor write changed memory zero to %q", got)
	}
}

func TestXqdMultivalueRejectsInvalidOutputPointers(t *testing.T) {
	tests := []struct {
		name         string
		endingCursor int32
		nwritten     int32
	}{
		{name: "nwritten", endingCursor: 32, nwritten: 62},
		{name: "ending_cursor", endingCursor: 60, nwritten: 40},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			memory := &Memory{ByteMemory(make([]byte, 64))}
			if status := xqd_multivalue(memory, []string{"one"}, 0, 4, 0, tt.endingCursor, tt.nwritten); status != XqdError {
				t.Fatalf("status = %d, want %d", status, XqdError)
			}
		})
	}
}

package profile

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestPerfScriptImporterBasicGolden(t *testing.T) {
	f, err := os.Open("testdata/native_samples/perf_script_basic.txt")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()

	imp := NewPerfScriptImporter()
	events, err := imp.Import(f)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("expected 4 events (one per header), got %d", len(events))
	}

	want := []NativeSampleEvent{
		{PID: 12345, UnixNanos: 1700000000_000001500, Function: "wasm_function_a"},
		{PID: 12345, UnixNanos: 1700000000_001002000, Function: "wasm_function_b"},
		{PID: 12345, UnixNanos: 1700000000_002500000, Function: "wasm_function_a"},
		{PID: 99999, UnixNanos: 1700000000_003000000, Function: "unrelated_function"},
	}
	for i, ev := range events {
		if ev != want[i] {
			t.Errorf("event %d: got %+v, want %+v", i, ev, want[i])
		}
	}
}

func TestPerfScriptImporterEmptyInputReturnsSentinel(t *testing.T) {
	imp := NewPerfScriptImporter()
	_, err := imp.Import(strings.NewReader(""))
	if !errors.Is(err, ErrNoNativeSamples) {
		t.Fatalf("empty input: got %v, want ErrNoNativeSamples", err)
	}
}

func TestPerfScriptImporterWhitespaceOnlyReturnsSentinel(t *testing.T) {
	imp := NewPerfScriptImporter()
	_, err := imp.Import(strings.NewReader("\n\n   \n\t\n"))
	if !errors.Is(err, ErrNoNativeSamples) {
		t.Fatalf("whitespace-only input: got %v, want ErrNoNativeSamples", err)
	}
}

func TestPerfScriptImporterHeaderWithoutFramesStillSentinel(t *testing.T) {
	// A header with no following stack frames is degenerate — perf
	// would only emit this if --call-graph was disabled. We surface it
	// as "no samples" rather than parse-error because the input
	// was structurally valid.
	input := "fastlike 12345 [001] 1700000000.000000000:    1 cycles:\n\n"
	imp := NewPerfScriptImporter()
	_, err := imp.Import(strings.NewReader(input))
	if !errors.Is(err, ErrNoNativeSamples) {
		t.Fatalf("header-only input: got %v, want ErrNoNativeSamples", err)
	}
}

func TestPerfScriptImporterMalformedHeaderIsParseError(t *testing.T) {
	// Random text that does not match the header shape is a parse error,
	// not ErrNoNativeSamples — we want to be loud about garbage input
	// rather than silently pretending the file was empty.
	input := "this is not perf script output at all\n"
	imp := NewPerfScriptImporter()
	_, err := imp.Import(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for malformed header")
	}
	if errors.Is(err, ErrNoNativeSamples) {
		t.Fatalf("malformed input misclassified as no-samples: %v", err)
	}
}

func TestPerfScriptImporterTimestampFractionPadding(t *testing.T) {
	// `perf script` defaults to microseconds (6 digit fraction); our
	// nanosecond conversion needs to pad on the right. Pin both 6- and
	// 9-digit fractions.
	cases := map[string]int64{
		"1700000000.000001":    1700000000_000001000, // 6 digits → pad to 9
		"1700000000.000001000": 1700000000_000001000, // 9 digits → as-is
		"1700000000.5":         1700000000_500000000, // single digit
	}
	for ts, wantNanos := range cases {
		line := "fastlike 12345 [001] " + ts + ":    1 cycles:\n\t7fbe x+0x0 (/tmp/y)\n\n"
		ev, err := NewPerfScriptImporter().Import(strings.NewReader(line))
		if err != nil {
			t.Fatalf("ts %q: %v", ts, err)
		}
		if len(ev) != 1 || ev[0].UnixNanos != wantNanos {
			t.Errorf("ts %q: got %d, want %d", ts, ev[0].UnixNanos, wantNanos)
		}
	}
}

func TestPerfScriptImporterStrayFrameLinesIgnored(t *testing.T) {
	// Stack frames after the first one (and frames without a preceding
	// header) are not errors; we just keep the top frame.
	input := strings.Join([]string{
		"fastlike 12345 [001] 1700000000.000000000:    1 cycles:",
		"\t7fbe top+0x5 (/tmp/x)",
		"\t7fbe deeper+0x2 (/tmp/x)",
		"\t7fbe deepest+0x1 (/tmp/x)",
		"",
		"\t7fbe orphan_frame+0x0 (/tmp/x)",
		"",
	}, "\n")
	imp := NewPerfScriptImporter()
	events, err := imp.Import(strings.NewReader(input))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Function != "top" {
		t.Errorf("expected top frame to win, got %q", events[0].Function)
	}
}

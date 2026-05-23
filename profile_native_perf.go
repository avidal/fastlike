package fastlike

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// PerfScriptImporter parses the text output of `perf script`. Compatible
// with the default per-sample layout `perf script` emits when fed a
// perf.data captured under `perf record -k CLOCK_REALTIME`. The
// CLOCK_REALTIME requirement is what makes timestamps comparable with
// the trace recorder's `time.Now()`-based WallStart; without it,
// MergeNativeSamples will reject every sample as "outside the time
// window" because perf's default CLOCK_MONOTONIC starts at boot. The
// CLI documents this in the helptext for -profile-native-samples
// (lands in a follow-up; today the importer is callable from embedder
// code or tests).
//
// Only the top stack frame is retained per sample for now. Multi-frame
// stack capture is additive and lands when the viewer learns to render
// flamegraphs; right now there's nothing to display deeper frames in.
type PerfScriptImporter struct{}

// NewPerfScriptImporter returns a ready-to-use importer. No
// configuration is needed; the parser is stateless.
func NewPerfScriptImporter() *PerfScriptImporter { return &PerfScriptImporter{} }

// Import reads `perf script` text from r, returns the parsed events, or
// ErrNoNativeSamples when parsing succeeded but no samples were found.
// Other errors indicate a malformed stream or I/O failure.
func (p *PerfScriptImporter) Import(r io.Reader) ([]NativeSampleEvent, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var (
		events       []NativeSampleEvent
		pendingPID   int
		pendingNanos int64
		expecting    bool
		sawAnyHeader bool
		lineNo       int
	)

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			expecting = false
			continue
		}
		if isFrameLine(line) {
			if !expecting {
				// A stack frame without a header — either we're past the
				// frames we wanted (only the top frame matters for v1)
				// or the file is malformed in a way we can recover from.
				continue
			}
			fn := topFrameFunction(trimmed)
			if fn == "" {
				continue
			}
			events = append(events, NativeSampleEvent{
				PID:       pendingPID,
				UnixNanos: pendingNanos,
				Function:  fn,
			})
			expecting = false
			continue
		}

		pid, nanos, ok := parseSampleHeader(line)
		if !ok {
			// Unrecognised header — surface as a parse error so the
			// caller knows the file isn't pure perf-script. Returning
			// nil here would silently drop sections of input.
			return nil, fmt.Errorf("perf script line %d: unrecognised header %q", lineNo, line)
		}
		sawAnyHeader = true
		pendingPID = pid
		pendingNanos = nanos
		expecting = true
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read perf script: %w", err)
	}
	if !sawAnyHeader {
		return nil, ErrNoNativeSamples
	}
	if len(events) == 0 {
		return nil, ErrNoNativeSamples
	}
	return events, nil
}

// perfScriptHeaderRe matches the most common `perf script` per-sample
// header. Fields, in order:
//
//	(1) comm (greedy, may contain spaces in rare cases — we tolerate
//	    that by anchoring on the trailing fields)
//	(2) pid
//	(3) optional [cpu] field
//	(4) timestamp as seconds.fraction
//	(5) sample count
//	(6) event name ending in ':'
//
// The trailing colon separates the header from the stack frames that
// follow on indented lines.
var perfScriptHeaderRe = regexp.MustCompile(
	`^\s*(\S.*?)\s+(\d+)\s+(?:\[\d+\]\s+)?(\d+\.\d+):\s+\d+\s+\S+:\s*$`,
)

// parseSampleHeader returns (pid, unixNanos, ok). When ok is false the
// caller treats the line as a parse error.
func parseSampleHeader(line string) (int, int64, bool) {
	m := perfScriptHeaderRe.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, false
	}
	pid, err := strconv.Atoi(m[2])
	if err != nil {
		return 0, 0, false
	}
	secStr, fracStr, hasFrac := strings.Cut(m[3], ".")
	if !hasFrac {
		fracStr = ""
	}
	sec, err := strconv.ParseInt(secStr, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	// Pad / truncate fractional digits to nanoseconds (9 places).
	if len(fracStr) > 9 {
		fracStr = fracStr[:9]
	}
	for len(fracStr) < 9 {
		fracStr += "0"
	}
	frac, err := strconv.ParseInt(fracStr, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return pid, sec*1_000_000_000 + frac, true
}

// isFrameLine returns true when line is an indented stack-frame entry.
// `perf script` indents stack frames with whitespace (typically a tab
// or 8 spaces); sample headers start at column 0.
func isFrameLine(line string) bool {
	if line == "" {
		return false
	}
	return line[0] == ' ' || line[0] == '\t'
}

// topFrameFunction extracts the function name from a stack-frame line
// of the form:
//
//	7fbeef2a1234 some_function+0x42 (/path/to/object)
//
// Returns "" if the line doesn't match the expected shape. We drop the
// +offset suffix and the trailing parenthesised path because neither
// is useful for the trace overlay; the function name is the
// semantically meaningful field.
func topFrameFunction(trimmed string) string {
	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return ""
	}
	// fields[0] is the hex address, fields[1+] is "symbol+offset
	// (binary)". We want the bit before the '+' in fields[1], or all
	// of fields[1] if there is no '+'.
	sym := fields[1]
	if idx := strings.IndexByte(sym, '+'); idx >= 0 {
		sym = sym[:idx]
	}
	return sym
}

package fastlike

import (
	"bytes"
	"io"
	"os"
)

// addLogger registers a named logger with the given writer.
func (i *Instance) addLogger(name string, w io.Writer) {
	if i.loggers == nil {
		i.loggers = []logger{}
	}

	i.loggers = append(i.loggers, logger{name, w})
}

// getLoggerHandle retrieves the handle ID for a named logger endpoint.
// Returns (handle_id, true) if found, or (-1, false) if not found or the name is reserved.
// Reserved names (stdout, stderr, stdin) cannot be used as log endpoint names.
func (i *Instance) getLoggerHandle(name string) (int, bool) {
	// Reserved names that should not be used as log endpoint names
	reservedNames := []string{"stdout", "stderr", "stdin"}
	for _, reserved := range reservedNames {
		if name == reserved {
			return -1, false
		}
	}

	// Only return handles for pre-configured loggers
	for j, l := range i.loggers {
		if l.name == name {
			return j, true
		}
	}

	// Logger not found
	return -1, false
}

// getLogger retrieves a logger by handle. Returns nil if the handle is invalid.
func (i *Instance) getLogger(handle int) io.Writer {
	if handle < 0 || handle > len(i.loggers)-1 {
		return nil
	}

	return i.loggers[handle]
}

// defaultLogger returns a writer that prefixes log messages with the logger name
// and writes to stdout with each write on a single line.
func defaultLogger(name string) io.Writer {
	return NewPrefixWriter(name, LineWriter{os.Stdout})
}

// logger represents a named log endpoint with its writer.
type logger struct {
	name string
	w    io.Writer
}

// Write implements io.Writer for logger by delegating to its underlying writer.
func (l logger) Write(data []byte) (int, error) {
	return l.w.Write(data)
}

// LineWriter wraps an io.Writer to ensure each Write call ends with exactly one newline.
// Internal newlines are escaped to keep each log entry on a single line.
type LineWriter struct{ io.Writer }

// Write implements io.Writer for LineWriter.
// It strips trailing newlines, escapes internal newlines, and appends a single newline.
func (lw LineWriter) Write(data []byte) (int, error) {
	originalLen := len(data)

	// Strip trailing newlines and escape internal newlines
	data = bytes.TrimRight(data, "\n")
	data = bytes.ReplaceAll(data, []byte("\n"), []byte("\\n"))

	if n, err := lw.Writer.Write(data); err != nil {
		return n, err
	}

	if n, err := lw.Writer.Write([]byte("\n")); err != nil {
		return n, err
	}

	// Return the original length to satisfy io.Writer semantics
	return originalLen, nil
}

// PrefixWriter wraps an io.Writer and prepends a prefix to each write operation.
type PrefixWriter struct {
	io.Writer
	prefix string
}

// Write implements io.Writer for PrefixWriter by prepending the prefix to the data.
func (w *PrefixWriter) Write(data []byte) (n int, err error) {
	l := len(data)
	msg := make([]byte, 0, len(w.prefix)+2+len(data))
	msg = append(msg, []byte(w.prefix+": ")...)
	msg = append(msg, data...)

	if n, err := w.Writer.Write(msg); err != nil {
		// Return only data bytes written, excluding prefix
		prefixLen := len(w.prefix) + 2 // +2 for ": "
		if n <= prefixLen {
			return 0, err
		}
		return n - prefixLen, err
	}

	return l, nil
}

// NewPrefixWriter creates a new PrefixWriter that prepends the given prefix to all writes.
func NewPrefixWriter(prefix string, w io.Writer) *PrefixWriter {
	return &PrefixWriter{Writer: w, prefix: prefix}
}

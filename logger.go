package fastlike

import (
	"bytes"
	"io"
	"os"
)

func (i *Instance) addLogger(name string, w io.Writer) {
	if i.loggers == nil {
		i.loggers = []logger{}
	}

	i.loggers = append(i.loggers, logger{name, w})
}

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

func (i *Instance) getLogger(handle int) io.Writer {
	if handle < 0 || handle > len(i.loggers)-1 {
		return nil
	}

	return i.loggers[handle]
}

func defaultLogger(name string) io.Writer {
	return NewPrefixWriter(name, LineWriter{os.Stdout})
}

type logger struct {
	name string
	w    io.Writer
}

func (l logger) Write(data []byte) (int, error) {
	return l.w.Write(data)
}

// LineWriter takes a writer and returns a new writer that ensures each Write call ends with
// a newline
type LineWriter struct{ io.Writer }

// Write implements io.Writer for LineWriter
func (lw LineWriter) Write(data []byte) (int, error) {
	l := len(data)
	// Ensure that all newlines in data are escaped, after stripping any trailing newlines
	data = bytes.TrimRight(data, "\n")
	data = bytes.ReplaceAll(data, []byte("\n"), []byte("\\n"))
	if n, err := lw.Writer.Write(data); err != nil {
		return n, err
	}

	if n, err := lw.Writer.Write([]byte("\n")); err != nil {
		return n, err
	} else {
		// only return the length of the "original" bytes if everything goes fine
		return l, err
	}
}

type PrefixWriter struct {
	io.Writer
	prefix string
}

func (w *PrefixWriter) Write(data []byte) (n int, err error) {
	l := len(data)
	msg := make([]byte, 0, len(w.prefix)+2+len(data))
	msg = append(msg, []byte(w.prefix+": ")...)
	msg = append(msg, data...)

	if n, err := w.Writer.Write(msg); err != nil {
		return n, err
	}

	return l, nil
}

func NewPrefixWriter(prefix string, w io.Writer) *PrefixWriter {
	return &PrefixWriter{Writer: w, prefix: prefix}
}

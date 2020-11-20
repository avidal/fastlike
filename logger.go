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

func (i *Instance) getLoggerHandle(name string) int {
	for j, l := range i.loggers {
		if l.name == name {
			return j
		}
	}

	// If there's no preconfigured logger by this name, create one using the default logger
	i.addLogger(name, i.defaultLogger(name))
	return len(i.loggers) - 1
}

func (i *Instance) getLogger(handle int) io.Writer {
	if handle > len(i.loggers)-1 {
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
	msg := make([]byte, len(w.prefix)+2+len(data))
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

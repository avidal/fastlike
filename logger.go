package fastlike

import (
	"bytes"
	"io"
)

func (i *Instance) addLogEndpoint(name string, w io.Writer) {
	if i.loggers == nil {
		i.loggers = []*logEndpoint{}
	}

	i.loggers = append(i.loggers, &logEndpoint{name, w})
}

type logEndpoint struct {
	name string
	w    io.Writer
}

func (le *logEndpoint) Write(data []byte) (int, error) {
	return le.w.Write(data)
}

type logEndpoints []*logEndpoint

func (les logEndpoints) Handle(name string) int {
	for i, le := range les {
		if le.name == name {
			return i
		}
	}
	return -1
}

func (les logEndpoints) Get(handle int) io.Writer {
	if handle > len(les)-1 {
		return nil
	}

	return les[handle]
}

// LineWriter takes a writer and returns a new writer that ensures each Write call ends with
// a newline
type LineWriter struct{ io.Writer }

// Write implements io.Writer for LineWriter
func (lw LineWriter) Write(data []byte) (n int, err error) {
	// Ensure that all newlines in data are escaped
	data = bytes.ReplaceAll(data, []byte("\n"), []byte("\\\n"))
	data = bytes.ReplaceAll(data, []byte("\r"), []byte("\\\r"))
	n, err = lw.Write(data)
	if err != nil {
		return n, err
	}

	if nw, err := lw.Write([]byte("\n")); err != nil {
		return n + nw, err
	} else {
		return n + nw, nil
	}
}

type PrefixWriter struct {
	io.Writer
	prefix string
}

func (w *PrefixWriter) Write(data []byte) (n int, err error) {
	if nw, err := w.Writer.Write([]byte(w.prefix)); err != nil {
		return nw, err
	} else {
		n += nw
	}

	if nw, err := w.Writer.Write(data); err != nil {
		return nw, err
	} else {
		n += nw
	}

	return n, nil
}

func NewPrefixWriter(prefix string, w io.Writer) *PrefixWriter {
	return &PrefixWriter{Writer: w, prefix: prefix}
}

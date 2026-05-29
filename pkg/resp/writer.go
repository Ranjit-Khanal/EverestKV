package resp

import (
	"fmt"
	"io"
	"strconv"
)

// Writer encodes RESP2 values to an io.Writer.
type Writer struct {
	w io.Writer
}

// NewWriter returns a RESP2 writer.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

func (w *Writer) Write(v Value) error {
	switch v.Type {
	case TypeSimpleString:
		return w.writeSimple('+', v.Str)
	case TypeError:
		return w.writeSimple('-', v.Str)
	case TypeInteger:
		return w.writeSimple(':', strconv.FormatInt(v.Int, 10))
	case TypeBulkString:
		return w.writeBulk(v.Bulk)
	case TypeArray:
		return w.writeArray(v.Array)
	default:
		return ErrInvalidPrefix
	}
}

func (w *Writer) writeSimple(prefix byte, s string) error {
	_, err := fmt.Fprintf(w.w, "%c%s\r\n", prefix, s)
	return err
}

func (w *Writer) writeBulk(b []byte) error {
	if b == nil {
		_, err := io.WriteString(w.w, "$-1\r\n")
		return err
	}
	if _, err := fmt.Fprintf(w.w, "$%d\r\n", len(b)); err != nil {
		return err
	}
	if _, err := w.w.Write(b); err != nil {
		return err
	}
	_, err := io.WriteString(w.w, "\r\n")
	return err
}

func (w *Writer) writeArray(items []Value) error {
	if items == nil {
		_, err := io.WriteString(w.w, "*-1\r\n")
		return err
	}
	if _, err := fmt.Fprintf(w.w, "*%d\r\n", len(items)); err != nil {
		return err
	}
	for _, item := range items {
		if err := w.Write(item); err != nil {
			return err
		}
	}
	return nil
}

// WriteSimpleString writes +<s>\r\n.
func (w *Writer) WriteSimpleString(s string) error {
	return w.Write(Value{Type: TypeSimpleString, Str: s})
}

// WriteError writes -<s>\r\n.
func (w *Writer) WriteError(s string) error {
	return w.Write(Value{Type: TypeError, Str: s})
}

// WriteBulkString writes $<len>\r\n<payload>\r\n.
func (w *Writer) WriteBulkString(s string) error {
	return w.Write(Value{Type: TypeBulkString, Bulk: []byte(s)})
}

// WriteNullBulk writes $-1\r\n.
func (w *Writer) WriteNullBulk() error {
	return w.writeBulk(nil)
}

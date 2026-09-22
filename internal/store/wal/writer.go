package wal

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SegmentFileName returns the on-disk file name for a WAL segment, e.g.
// "wal-000001.log".
func SegmentFileName(segment uint64) string {
	return fmt.Sprintf("wal-%06d.log", segment)
}

// SegmentPath joins dir with the file name for segment.
func SegmentPath(dir string, segment uint64) string {
	return filepath.Join(dir, SegmentFileName(segment))
}

// ParseSegmentFileName parses a WAL segment file name back into its segment
// number. It returns ok=false for any name not produced by SegmentFileName.
func ParseSegmentFileName(name string) (segment uint64, ok bool) {
	const prefix, suffix = "wal-", ".log"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return 0, false
	}
	n, err := strconv.ParseUint(name[len(prefix):len(name)-len(suffix)], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Writer appends records to a single WAL segment file.
type Writer struct {
	segment uint64
	f       *os.File
	size    int64
}

// CreateSegment creates a new, empty WAL segment file in dir and returns a
// Writer for it. It fails if the segment file already exists.
func CreateSegment(dir string, segment uint64) (*Writer, error) {
	path := SegmentPath(dir, segment)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("wal: create segment %d: %w", segment, err)
	}
	return &Writer{segment: segment, f: f}, nil
}

// OpenSegmentForAppend opens an existing WAL segment file so writes continue
// to be appended after whatever it already contains. Used when recovery
// keeps writing into the last segment found on disk.
func OpenSegmentForAppend(dir string, segment uint64) (*Writer, error) {
	path := SegmentPath(dir, segment)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("wal: open segment %d: %w", segment, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("wal: stat segment %d: %w", segment, err)
	}
	return &Writer{segment: segment, f: f, size: info.Size()}, nil
}

// Segment returns the segment number this writer appends to.
func (w *Writer) Segment() uint64 {
	return w.segment
}

// Size returns the number of bytes appended to the segment so far (by this
// writer, plus whatever existed when it was opened).
func (w *Writer) Size() int64 {
	return w.size
}

// Append encodes rec and writes it to the segment. It does not fsync; call
// Sync to make the write durable.
func (w *Writer) Append(rec Record) error {
	buf := encode(rec)
	n, err := w.f.Write(buf)
	w.size += int64(n)
	if err != nil {
		return fmt.Errorf("wal: append to segment %d: %w", w.segment, err)
	}
	return nil
}

// Sync flushes the segment file's contents to stable storage.
func (w *Writer) Sync() error {
	if err := w.f.Sync(); err != nil {
		return fmt.Errorf("wal: sync segment %d: %w", w.segment, err)
	}
	return nil
}

// Close closes the underlying file without syncing.
func (w *Writer) Close() error {
	if err := w.f.Close(); err != nil {
		return fmt.Errorf("wal: close segment %d: %w", w.segment, err)
	}
	return nil
}

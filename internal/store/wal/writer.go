package wal

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SegmentFileName returns a segment's file name, e.g. "wal-000001.log".
func SegmentFileName(segment uint64) string {
	return fmt.Sprintf("wal-%06d.log", segment)
}

// SegmentPath returns the path of segment in dir.
func SegmentPath(dir string, segment uint64) string {
	return filepath.Join(dir, SegmentFileName(segment))
}

// ParseSegmentFileName returns the segment number in name, if it is a WAL file.
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

// CreateSegment creates a new segment file. It fails if the file exists.
func CreateSegment(dir string, segment uint64) (*Writer, error) {
	path := SegmentPath(dir, segment)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("wal: create segment %d: %w", segment, err)
	}
	return &Writer{segment: segment, f: f}, nil
}

// OpenSegmentForAppend opens an existing segment to keep appending to it.
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

// Segment returns the segment number.
func (w *Writer) Segment() uint64 {
	return w.segment
}

// Size returns the segment's size in bytes.
func (w *Writer) Size() int64 {
	return w.size
}

// Append writes rec. It does not fsync; call Sync for durability.
func (w *Writer) Append(rec Record) error {
	buf := encode(rec)
	n, err := w.f.Write(buf)
	w.size += int64(n)
	if err != nil {
		return fmt.Errorf("wal: append to segment %d: %w", w.segment, err)
	}
	return nil
}

// Sync fsyncs the segment.
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

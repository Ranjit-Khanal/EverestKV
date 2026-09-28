// Package sstable is the on-disk sorted table that memtables flush into.
//
// File layout:
//
//	data block 0
//	data block 1
//	...
//	sparse index
//	footer (fixed size)
//
// Data record:
//
//	[keyLen uint32][key][type uint8][valLen uint32][value]
//
// Sparse index, one entry per ~blockSize bytes of data:
//
//	[keyLen uint32][key][offset uint64]  (repeated)
//
// Footer, the last footerSize bytes:
//
//	[indexOffset uint64][magic uint64]
package sstable

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
)

// EntryType is a put or a delete, same values as the WAL.
type EntryType uint8

// Entry types.
const (
	EntryPut    EntryType = 1
	EntryDelete EntryType = 2
)

// Entry is one key's final state.
type Entry struct {
	Key       []byte
	Value     []byte
	Tombstone bool
}

// blockSize is the approximate data block size between index entries.
const blockSize = 4096

// magic marks a valid footer.
const magic uint64 = 0x45766572657374 // "Everest" in ASCII

const footerSize = 8 + 8 // indexOffset + magic

// FileName returns the file name for id, e.g. "000001.sst".
func FileName(id uint64) string {
	return fmt.Sprintf("%06d.sst", id)
}

// indexEntry is a block's first key and its offset.
type indexEntry struct {
	key    []byte
	offset uint64
}

// Writer builds one SSTable. Keys must be written in increasing order.
type Writer struct {
	finalPath string
	tmpPath   string
	f         *os.File

	offset            uint64
	bytesSinceIndexed uint64
	index             []indexEntry
	lastKey           []byte
	numEntries        int
	closed            bool
}

// NewWriter creates a temp file for table id. Finish gives it its real name.
func NewWriter(dir string, id uint64) (*Writer, error) {
	finalPath := filepath.Join(dir, FileName(id))
	tmpPath := finalPath + ".tmp"

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("sstable: create %s: %w", tmpPath, err)
	}
	return &Writer{finalPath: finalPath, tmpPath: tmpPath, f: f}, nil
}

// Write appends e. Keys must be increasing.
func (w *Writer) Write(e Entry) error {
	if w.lastKey != nil && bytes.Compare(e.Key, w.lastKey) <= 0 {
		return fmt.Errorf("sstable: out-of-order key %q after %q", e.Key, w.lastKey)
	}

	if len(w.index) == 0 || w.bytesSinceIndexed >= blockSize {
		w.index = append(w.index, indexEntry{key: cloneBytes(e.Key), offset: w.offset})
		w.bytesSinceIndexed = 0
	}

	rec := encodeEntry(e)
	n, err := w.f.Write(rec)
	w.offset += uint64(n)
	w.bytesSinceIndexed += uint64(n)
	if err != nil {
		return fmt.Errorf("sstable: write entry: %w", err)
	}

	w.lastKey = cloneBytes(e.Key)
	w.numEntries++
	return nil
}

// Info describes a finished SSTable.
type Info struct {
	Path       string
	NumEntries int
	Size       int64
}

// Finish writes the index and footer, fsyncs, and renames the file into place.
func (w *Writer) Finish() (Info, error) {
	if w.closed {
		return Info{}, fmt.Errorf("sstable: Finish called twice")
	}

	indexOffset := w.offset
	for _, ie := range w.index {
		buf := make([]byte, 4+len(ie.key)+8)
		binary.LittleEndian.PutUint32(buf[0:4], uint32(len(ie.key)))
		copy(buf[4:4+len(ie.key)], ie.key)
		binary.LittleEndian.PutUint64(buf[4+len(ie.key):], ie.offset)
		n, err := w.f.Write(buf)
		w.offset += uint64(n)
		if err != nil {
			w.abort()
			return Info{}, fmt.Errorf("sstable: write index: %w", err)
		}
	}

	footer := make([]byte, footerSize)
	binary.LittleEndian.PutUint64(footer[0:8], indexOffset)
	binary.LittleEndian.PutUint64(footer[8:16], magic)
	if _, err := w.f.Write(footer); err != nil {
		w.abort()
		return Info{}, fmt.Errorf("sstable: write footer: %w", err)
	}
	w.offset += footerSize

	if err := w.f.Sync(); err != nil {
		w.abort()
		return Info{}, fmt.Errorf("sstable: sync %s: %w", w.tmpPath, err)
	}
	size := int64(w.offset)
	if err := w.f.Close(); err != nil {
		return Info{}, fmt.Errorf("sstable: close %s: %w", w.tmpPath, err)
	}
	w.closed = true

	if err := os.Rename(w.tmpPath, w.finalPath); err != nil {
		return Info{}, fmt.Errorf("sstable: rename %s to %s: %w", w.tmpPath, w.finalPath, err)
	}

	if err := syncDir(filepath.Dir(w.finalPath)); err != nil {
		return Info{}, fmt.Errorf("sstable: sync dir: %w", err)
	}

	return Info{Path: w.finalPath, NumEntries: w.numEntries, Size: size}, nil
}

// Abort removes the temp file. No-op after Finish.
func (w *Writer) Abort() error {
	if w.closed {
		return nil
	}
	return w.abort()
}

func (w *Writer) abort() error {
	w.closed = true
	closeErr := w.f.Close()
	removeErr := os.Remove(w.tmpPath)
	if closeErr != nil {
		return closeErr
	}
	if removeErr != nil && !os.IsNotExist(removeErr) {
		return removeErr
	}
	return nil
}

func encodeEntry(e Entry) []byte {
	typ := EntryPut
	value := e.Value
	if e.Tombstone {
		typ = EntryDelete
		value = nil
	}

	buf := make([]byte, 4+len(e.Key)+1+4+len(value))
	off := 0
	binary.LittleEndian.PutUint32(buf[off:], uint32(len(e.Key)))
	off += 4
	off += copy(buf[off:], e.Key)
	buf[off] = byte(typ)
	off++
	binary.LittleEndian.PutUint32(buf[off:], uint32(len(value)))
	off += 4
	copy(buf[off:], value)

	return buf
}

func cloneBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// syncDir fsyncs dir so renames in it survive a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

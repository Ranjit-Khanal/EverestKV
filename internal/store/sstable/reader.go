package sstable

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ErrCorrupt is returned (wrapped) when an SSTable's footer, index or data
// records are inconsistent with the file's size or with each other.
var ErrCorrupt = errors.New("sstable: corrupt table")

// Reader serves point lookups from a single finished SSTable. It loads the
// footer and sparse index into memory on open; each Get then reads exactly
// one data block from disk.
//
// A Reader is safe for concurrent use: lookups use ReadAt, which does not
// share a file offset between callers.
type Reader struct {
	path        string
	f           *os.File
	index       []indexEntry
	indexOffset uint64 // end of the data section
}

// Open opens SSTable id in dir, verifies its footer and loads its sparse
// index.
func Open(dir string, id uint64) (*Reader, error) {
	path := filepath.Join(dir, FileName(id))
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("sstable: open %s: %w", path, err)
	}
	r := &Reader{path: path, f: f}
	if err := r.load(); err != nil {
		f.Close()
		return nil, err
	}
	return r, nil
}

func (r *Reader) load() error {
	st, err := r.f.Stat()
	if err != nil {
		return fmt.Errorf("sstable: stat %s: %w", r.path, err)
	}
	size := st.Size()
	if size < footerSize {
		return fmt.Errorf("%w: %s: %d bytes is too short for a footer", ErrCorrupt, r.path, size)
	}

	footer := make([]byte, footerSize)
	if _, err := r.f.ReadAt(footer, size-footerSize); err != nil {
		return fmt.Errorf("sstable: read footer of %s: %w", r.path, err)
	}
	if got := binary.LittleEndian.Uint64(footer[8:16]); got != magic {
		return fmt.Errorf("%w: %s: bad magic %x", ErrCorrupt, r.path, got)
	}
	indexOffset := binary.LittleEndian.Uint64(footer[0:8])
	indexEnd := uint64(size - footerSize)
	if indexOffset > indexEnd {
		return fmt.Errorf("%w: %s: index offset %d past end of index %d", ErrCorrupt, r.path, indexOffset, indexEnd)
	}

	buf := make([]byte, indexEnd-indexOffset)
	if _, err := r.f.ReadAt(buf, int64(indexOffset)); err != nil {
		return fmt.Errorf("sstable: read index of %s: %w", r.path, err)
	}
	index, err := decodeIndex(buf, indexOffset)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrCorrupt, r.path, err)
	}

	r.index = index
	r.indexOffset = indexOffset
	return nil
}

// decodeIndex parses the sparse index section. Offsets must start at 0,
// be strictly increasing and point inside the data section, and keys must
// be strictly increasing, since Get relies on all of that.
func decodeIndex(buf []byte, dataEnd uint64) ([]indexEntry, error) {
	var index []indexEntry
	for off := 0; off < len(buf); {
		if len(buf)-off < 4 {
			return nil, fmt.Errorf("truncated index key length at %d", off)
		}
		keyLen := int(binary.LittleEndian.Uint32(buf[off:]))
		off += 4
		if len(buf)-off < keyLen+8 {
			return nil, fmt.Errorf("truncated index entry at %d", off)
		}
		key := cloneBytes(buf[off : off+keyLen])
		off += keyLen
		blockOff := binary.LittleEndian.Uint64(buf[off:])
		off += 8

		if blockOff >= dataEnd {
			return nil, fmt.Errorf("index block offset %d outside data section [0, %d)", blockOff, dataEnd)
		}
		if n := len(index); n == 0 {
			if blockOff != 0 {
				return nil, fmt.Errorf("first index block starts at %d, want 0", blockOff)
			}
		} else if blockOff <= index[n-1].offset || bytes.Compare(key, index[n-1].key) <= 0 {
			return nil, fmt.Errorf("index entry %d out of order", n)
		}
		index = append(index, indexEntry{key: key, offset: blockOff})
	}
	if len(index) == 0 && dataEnd != 0 {
		return nil, fmt.Errorf("empty index for %d bytes of data", dataEnd)
	}
	return index, nil
}

// Get looks key up in the table. found reports whether the table holds an
// entry for key at all; when it does, tombstone reports whether that entry
// is a delete, in which case value is nil. A tombstone must still shadow
// older tables, so callers should stop searching when found is true.
func (r *Reader) Get(key []byte) (value []byte, tombstone, found bool, err error) {
	// The candidate block is the last one whose first key is <= key.
	i := sort.Search(len(r.index), func(i int) bool {
		return bytes.Compare(r.index[i].key, key) > 0
	}) - 1
	if i < 0 {
		return nil, false, false, nil
	}

	start := r.index[i].offset
	end := r.indexOffset
	if i+1 < len(r.index) {
		end = r.index[i+1].offset
	}
	block := make([]byte, end-start)
	if _, err := r.f.ReadAt(block, int64(start)); err != nil {
		return nil, false, false, fmt.Errorf("sstable: read block at %d of %s: %w", start, r.path, err)
	}

	for off := 0; off < len(block); {
		e, n, err := decodeEntry(block[off:])
		if err != nil {
			return nil, false, false, fmt.Errorf("%w: %s: block at %d, record at +%d: %v", ErrCorrupt, r.path, start, off, err)
		}
		off += n

		switch c := bytes.Compare(e.Key, key); {
		case c == 0:
			if e.Tombstone {
				return nil, true, true, nil
			}
			return cloneBytes(e.Value), false, true, nil
		case c > 0:
			// Keys are sorted, so key isn't in this block.
			return nil, false, false, nil
		}
	}
	return nil, false, false, nil
}

// decodeEntry parses one data record from the start of buf, returning the
// entry (whose slices alias buf) and the number of bytes it occupied.
func decodeEntry(buf []byte) (Entry, int, error) {
	const fixed = 4 + 1 + 4 // keyLen + type + valLen
	if len(buf) < fixed {
		return Entry{}, 0, fmt.Errorf("truncated record header")
	}
	off := 0
	keyLen := int(binary.LittleEndian.Uint32(buf[off:]))
	off += 4
	if len(buf)-off < keyLen+1+4 {
		return Entry{}, 0, fmt.Errorf("key length %d overruns block", keyLen)
	}
	key := buf[off : off+keyLen]
	off += keyLen
	typ := EntryType(buf[off])
	off++
	valLen := int(binary.LittleEndian.Uint32(buf[off:]))
	off += 4
	if len(buf)-off < valLen {
		return Entry{}, 0, fmt.Errorf("value length %d overruns block", valLen)
	}
	value := buf[off : off+valLen]
	off += valLen

	switch typ {
	case EntryPut:
		return Entry{Key: key, Value: value}, off, nil
	case EntryDelete:
		return Entry{Key: key, Tombstone: true}, off, nil
	default:
		return Entry{}, 0, fmt.Errorf("unknown entry type %d", typ)
	}
}

// Close closes the underlying file.
func (r *Reader) Close() error {
	return r.f.Close()
}

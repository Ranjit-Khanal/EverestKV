package sstable

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// decodedEntry mirrors Entry but read back off disk, for test assertions.
type decodedEntry struct {
	key       []byte
	value     []byte
	tombstone bool
}

// parseFile fully decodes a finished SSTable file for test verification.
// It is a minimal stand-in for the not-yet-implemented read path.
func parseFile(t *testing.T, path string) (entries []decodedEntry, index []indexEntry) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) < footerSize {
		t.Fatalf("file too short to contain a footer: %d bytes", len(data))
	}

	footer := data[len(data)-footerSize:]
	indexOffset := binary.LittleEndian.Uint64(footer[0:8])
	gotMagic := binary.LittleEndian.Uint64(footer[8:16])
	if gotMagic != magic {
		t.Fatalf("bad magic: got %x, want %x", gotMagic, magic)
	}

	// Decode data section [0, indexOffset).
	off := uint64(0)
	for off < indexOffset {
		keyLen := binary.LittleEndian.Uint32(data[off:])
		off += 4
		key := data[off : off+uint64(keyLen)]
		off += uint64(keyLen)
		typ := EntryType(data[off])
		off++
		valLen := binary.LittleEndian.Uint32(data[off:])
		off += 4
		value := data[off : off+uint64(valLen)]
		off += uint64(valLen)

		entries = append(entries, decodedEntry{
			key:       append([]byte(nil), key...),
			value:     append([]byte(nil), value...),
			tombstone: typ == EntryDelete,
		})
	}
	if off != indexOffset {
		t.Fatalf("data section decode ended at %d, index starts at %d", off, indexOffset)
	}

	// Decode index section [indexOffset, len(data)-footerSize).
	indexEnd := uint64(len(data) - footerSize)
	for off < indexEnd {
		keyLen := binary.LittleEndian.Uint32(data[off:])
		off += 4
		key := data[off : off+uint64(keyLen)]
		off += uint64(keyLen)
		blockOffset := binary.LittleEndian.Uint64(data[off:])
		off += 8
		index = append(index, indexEntry{key: append([]byte(nil), key...), offset: blockOffset})
	}
	if off != indexEnd {
		t.Fatalf("index section decode ended at %d, expected %d", off, indexEnd)
	}

	return entries, index
}

func TestWriterSortedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	want := []Entry{
		{Key: []byte("alpha"), Value: []byte("1")},
		{Key: []byte("bravo"), Value: []byte("2")},
		{Key: []byte("charlie"), Tombstone: true},
		{Key: []byte("delta"), Value: []byte("4")},
	}
	for _, e := range want {
		if err := w.Write(e); err != nil {
			t.Fatalf("Write(%q): %v", e.Key, err)
		}
	}

	info, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if info.NumEntries != len(want) {
		t.Fatalf("info.NumEntries = %d, want %d", info.NumEntries, len(want))
	}
	wantPath := filepath.Join(dir, "000001.sst")
	if info.Path != wantPath {
		t.Fatalf("info.Path = %q, want %q", info.Path, wantPath)
	}
	if _, err := os.Stat(filepath.Join(dir, "000001.sst.tmp")); !os.IsNotExist(err) {
		t.Fatalf("temp file still present after Finish")
	}

	got, index := parseFile(t, info.Path)
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i].key, want[i].Key) ||
			!bytes.Equal(got[i].value, want[i].Value) ||
			got[i].tombstone != want[i].Tombstone {
			t.Errorf("entry %d = %+v, want key=%q value=%q tomb=%v", i, got[i], want[i].Key, want[i].Value, want[i].Tombstone)
		}
	}

	if len(index) == 0 {
		t.Fatalf("expected at least one sparse index entry")
	}
	if !bytes.Equal(index[0].key, want[0].Key) {
		t.Fatalf("first index entry key = %q, want %q (first key in file)", index[0].key, want[0].Key)
	}
}

func TestWriterRejectsOutOfOrderKeys(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Abort()

	if err := w.Write(Entry{Key: []byte("b"), Value: []byte("1")}); err != nil {
		t.Fatalf("Write(b): %v", err)
	}
	if err := w.Write(Entry{Key: []byte("a"), Value: []byte("2")}); err == nil {
		t.Fatalf("expected error writing out-of-order key, got nil")
	}
	if err := w.Write(Entry{Key: []byte("b"), Value: []byte("2")}); err == nil {
		t.Fatalf("expected error writing duplicate key, got nil")
	}
}

func TestWriterSparseIndexCoversMultipleBlocks(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	// Big values to force multiple ~4KB blocks.
	bigVal := bytes.Repeat([]byte("x"), 1024)
	const n = 20 // ~20KB of data, several blocks at 4KB threshold
	for i := 0; i < n; i++ {
		key := []byte{byte('a' + i)}
		if err := w.Write(Entry{Key: key, Value: bigVal}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	info, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	entries, index := parseFile(t, info.Path)
	if len(entries) != n {
		t.Fatalf("got %d entries, want %d", len(entries), n)
	}
	if len(index) < 2 {
		t.Fatalf("expected multiple sparse index blocks for ~20KB of data, got %d", len(index))
	}
	// Index offsets must be strictly increasing. parseFile already proved
	// the data section decodes cleanly from offset 0 up to indexOffset, so
	// offsets landing within that range are valid entry boundaries.
	for i, ie := range index {
		if i > 0 && ie.offset <= index[i-1].offset {
			t.Fatalf("index offsets not strictly increasing at %d", i)
		}
	}
}

func TestWriterAbortRemovesTempFile(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Write(Entry{Key: []byte("a"), Value: []byte("1")}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "000001.sst.tmp")); !os.IsNotExist(err) {
		t.Fatalf("temp file still present after Abort")
	}
	if _, err := os.Stat(filepath.Join(dir, "000001.sst")); !os.IsNotExist(err) {
		t.Fatalf("final file should never have been created")
	}
}

func TestNewWriterFailsIfTempExists(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Abort()

	if _, err := NewWriter(dir, 1); err == nil {
		t.Fatalf("expected error creating writer for id already in progress")
	}
}

package sstable

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// writeTable writes entries (already in key order) as SSTable 1 in dir and
// returns an open Reader for it.
func writeTable(t *testing.T, dir string, entries []Entry) *Reader {
	t.Helper()
	w, err := NewWriter(dir, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for _, e := range entries {
		if err := w.Write(e); err != nil {
			t.Fatalf("Write(%q): %v", e.Key, err)
		}
	}
	if _, err := w.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	r, err := Open(dir, 1)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func mustGet(t *testing.T, r *Reader, key string) (value []byte, tombstone, found bool) {
	t.Helper()
	value, tombstone, found, err := r.Get([]byte(key))
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	return value, tombstone, found
}

func TestReaderGetSmallTable(t *testing.T) {
	r := writeTable(t, t.TempDir(), []Entry{
		{Key: []byte("alpha"), Value: []byte("1")},
		{Key: []byte("bravo"), Value: []byte("")},
		{Key: []byte("charlie"), Tombstone: true},
		{Key: []byte("delta"), Value: []byte("4")},
	})

	tests := []struct {
		key       string
		value     string
		tombstone bool
		found     bool
	}{
		{key: "alpha", value: "1", found: true},
		{key: "bravo", value: "", found: true},
		{key: "charlie", tombstone: true, found: true},
		{key: "delta", value: "4", found: true},
		{key: "", found: false},         // before the first key
		{key: "aaa", found: false},      // before the first key
		{key: "b", found: false},        // between keys
		{key: "alphabet", found: false}, // prefix extension of a present key
		{key: "zulu", found: false},     // after the last key
	}
	for _, tt := range tests {
		v, tomb, found := mustGet(t, r, tt.key)
		if found != tt.found || tomb != tt.tombstone || string(v) != tt.value {
			t.Errorf("Get(%q) = (%q, tomb=%v, found=%v), want (%q, tomb=%v, found=%v)",
				tt.key, v, tomb, found, tt.value, tt.tombstone, tt.found)
		}
		if tomb && v != nil {
			t.Errorf("Get(%q): tombstone returned non-nil value %q", tt.key, v)
		}
	}
}

func TestReaderGetMultiBlock(t *testing.T) {
	var entries []Entry
	const n = 2000
	for i := 0; i < n; i++ {
		e := Entry{Key: []byte(fmt.Sprintf("key-%05d", i))}
		if i%7 == 0 {
			e.Tombstone = true
		} else {
			e.Value = bytes.Repeat([]byte{byte('a' + i%26)}, 1+i%50)
		}
		entries = append(entries, e)
	}
	r := writeTable(t, t.TempDir(), entries)
	if len(r.index) < 2 {
		t.Fatalf("expected a multi-block table, got %d index entries", len(r.index))
	}

	for _, e := range entries {
		v, tomb, found := mustGet(t, r, string(e.Key))
		if !found || tomb != e.Tombstone || !bytes.Equal(v, e.Value) {
			t.Fatalf("Get(%q) = (%q, tomb=%v, found=%v), want (%q, tomb=%v, found=true)",
				e.Key, v, tomb, found, e.Value, e.Tombstone)
		}
	}

	// Every block's first key, and keys falling in the gaps around block
	// boundaries, must be handled correctly.
	for _, ie := range r.index {
		if _, _, found := mustGet(t, r, string(ie.key)); !found {
			t.Fatalf("block first key %q not found", ie.key)
		}
		if _, _, found := mustGet(t, r, string(ie.key)+"~"); found {
			t.Fatalf("absent key %q+\"~\" reported found", ie.key)
		}
	}
}

func TestReaderGetLargeValueSpanningBlockSize(t *testing.T) {
	big := bytes.Repeat([]byte("v"), 3*blockSize)
	r := writeTable(t, t.TempDir(), []Entry{
		{Key: []byte("a"), Value: []byte("small")},
		{Key: []byte("b"), Value: big},
		{Key: []byte("c"), Value: []byte("after")},
	})

	if v, _, found := mustGet(t, r, "b"); !found || !bytes.Equal(v, big) {
		t.Fatalf("Get(b): found=%v len=%d, want found=true len=%d", found, len(v), len(big))
	}
	if v, _, found := mustGet(t, r, "c"); !found || string(v) != "after" {
		t.Fatalf("Get(c) = (%q, found=%v), want (\"after\", true)", v, found)
	}
}

func TestReaderEmptyTable(t *testing.T) {
	r := writeTable(t, t.TempDir(), nil)
	if _, _, found := mustGet(t, r, "anything"); found {
		t.Fatalf("empty table reported a key as found")
	}
}

func TestReaderConcurrentGets(t *testing.T) {
	var entries []Entry
	for i := 0; i < 500; i++ {
		entries = append(entries, Entry{Key: []byte(fmt.Sprintf("k%04d", i)), Value: []byte(fmt.Sprintf("v%04d", i))})
	}
	r := writeTable(t, t.TempDir(), entries)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, e := range entries {
				v, _, found, err := r.Get(e.Key)
				if err != nil || !found || !bytes.Equal(v, e.Value) {
					t.Errorf("Get(%q) = (%q, found=%v, err=%v)", e.Key, v, found, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestOpenMissingFile(t *testing.T) {
	if _, err := Open(t.TempDir(), 42); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open(missing) error = %v, want os.ErrNotExist", err)
	}
}

func TestOpenRejectsCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	r := writeTable(t, dir, []Entry{
		{Key: []byte("a"), Value: []byte("1")},
		{Key: []byte("b"), Value: []byte("2")},
	})
	r.Close()
	path := filepath.Join(dir, FileName(1))
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"too short", func(b []byte) []byte { return b[:footerSize-1] }},
		{"truncated", func(b []byte) []byte { return b[:len(b)-1] }},
		{"bad magic", func(b []byte) []byte { b[len(b)-1] ^= 0xff; return b }},
		{"index offset past end", func(b []byte) []byte { b[len(b)-footerSize] = 0xff; return b }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad := tt.mutate(append([]byte(nil), good...))
			if err := os.WriteFile(path, bad, 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			r, err := Open(dir, 1)
			if err == nil {
				r.Close()
				t.Fatalf("Open succeeded on corrupt file")
			}
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("Open error = %v, want ErrCorrupt", err)
			}
		})
	}
}

func TestReaderGetReportsCorruptRecord(t *testing.T) {
	dir := t.TempDir()
	r := writeTable(t, dir, []Entry{
		{Key: []byte("a"), Value: []byte("1")},
		{Key: []byte("b"), Value: []byte("2")},
	})
	r.Close()
	path := filepath.Join(dir, FileName(1))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// Record 0 is [keyLen=1]["a"][type]...: corrupt its type byte.
	data[4+1] = 99
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r, err = Open(dir, 1)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if _, _, _, err := r.Get([]byte("b")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Get over corrupt record error = %v, want ErrCorrupt", err)
	}
}

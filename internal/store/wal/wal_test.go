package wal

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func mustCreate(t *testing.T, dir string, segment uint64) *Writer {
	t.Helper()
	w, err := CreateSegment(dir, segment)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	return w
}

func readAll(t *testing.T, dir string, segment uint64) []Record {
	t.Helper()
	r, err := OpenSegmentReader(dir, segment)
	if err != nil {
		t.Fatalf("OpenSegmentReader: %v", err)
	}
	defer r.Close()

	var recs []Record
	for {
		rec, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: unexpected error: %v", err)
		}
		recs = append(recs, rec)
	}
	return recs
}

func TestRoundTripPutAndDelete(t *testing.T) {
	dir := t.TempDir()
	w := mustCreate(t, dir, 1)

	want := []Record{
		{Seq: 1, Type: RecordPut, Key: []byte("a"), Value: []byte("1")},
		{Seq: 2, Type: RecordPut, Key: []byte("bbbbb"), Value: []byte("value with spaces and stuff")},
		{Seq: 3, Type: RecordDelete, Key: []byte("a"), Value: nil},
		{Seq: 4, Type: RecordPut, Key: []byte(""), Value: []byte("empty key ok")},
		{Seq: 5, Type: RecordPut, Key: []byte("bin\x00key"), Value: []byte{0, 1, 2, 255}},
	}
	for _, rec := range want {
		if err := w.Append(rec); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := readAll(t, dir, 1)
	if len(got) != len(want) {
		t.Fatalf("got %d records, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Seq != want[i].Seq || got[i].Type != want[i].Type ||
			!bytes.Equal(got[i].Key, want[i].Key) || !bytes.Equal(got[i].Value, want[i].Value) {
			t.Errorf("record %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestReaderStopsAtTruncatedTail(t *testing.T) {
	dir := t.TempDir()
	w := mustCreate(t, dir, 1)

	full := []Record{
		{Seq: 1, Type: RecordPut, Key: []byte("k1"), Value: []byte("v1")},
		{Seq: 2, Type: RecordPut, Key: []byte("k2"), Value: []byte("v2")},
	}
	for _, rec := range full {
		if err := w.Append(rec); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	// Simulate a torn write: append a third record's bytes but cut them
	// short, as if the process crashed mid-write.
	tornBytes := encode(Record{Seq: 3, Type: RecordPut, Key: []byte("k3"), Value: []byte("v3-should-not-appear")})
	tornBytes = tornBytes[:len(tornBytes)-5]
	if _, err := w.f.Write(tornBytes); err != nil {
		t.Fatalf("write torn bytes: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := readAll(t, dir, 1)
	if len(got) != len(full) {
		t.Fatalf("got %d records, want %d (torn tail must be dropped silently)", len(got), len(full))
	}
	for i := range full {
		if got[i].Seq != full[i].Seq || !bytes.Equal(got[i].Key, full[i].Key) || !bytes.Equal(got[i].Value, full[i].Value) {
			t.Errorf("record %d: got %+v, want %+v", i, got[i], full[i])
		}
	}
}

func TestReaderStopsAtCorruptedChecksum(t *testing.T) {
	dir := t.TempDir()
	w := mustCreate(t, dir, 1)

	first := Record{Seq: 1, Type: RecordPut, Key: []byte("k1"), Value: []byte("v1")}
	second := Record{Seq: 2, Type: RecordPut, Key: []byte("k2"), Value: []byte("v2")}
	if err := w.Append(first); err != nil {
		t.Fatalf("Append: %v", err)
	}
	firstEnd := w.Size()
	if err := w.Append(second); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Flip a byte inside the second record's payload to break its checksum.
	path := SegmentPath(dir, 1)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	corruptAt := firstEnd + crcSize + lengthSize // first byte of second record's body
	data[corruptAt] ^= 0xFF
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := readAll(t, dir, 1)
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1 (corrupted record must stop the reader)", len(got))
	}
	if !bytes.Equal(got[0].Key, first.Key) || !bytes.Equal(got[0].Value, first.Value) {
		t.Errorf("record 0: got %+v, want %+v", got[0], first)
	}
}

func TestReaderEmptySegment(t *testing.T) {
	dir := t.TempDir()
	w := mustCreate(t, dir, 1)
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := readAll(t, dir, 1)
	if len(got) != 0 {
		t.Fatalf("got %d records, want 0", len(got))
	}
}

func TestOpenSegmentForAppend(t *testing.T) {
	dir := t.TempDir()
	w := mustCreate(t, dir, 1)
	if err := w.Append(Record{Seq: 1, Type: RecordPut, Key: []byte("k1"), Value: []byte("v1")}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	w2, err := OpenSegmentForAppend(dir, 1)
	if err != nil {
		t.Fatalf("OpenSegmentForAppend: %v", err)
	}
	if w2.Size() == 0 {
		t.Fatalf("expected non-zero size after reopening a non-empty segment")
	}
	if err := w2.Append(Record{Seq: 2, Type: RecordPut, Key: []byte("k2"), Value: []byte("v2")}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w2.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := w2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := readAll(t, dir, 1)
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
}

func TestParseSegmentFileName(t *testing.T) {
	for seg := uint64(0); seg <= 3; seg++ {
		name := SegmentFileName(seg)
		got, ok := ParseSegmentFileName(name)
		if !ok || got != seg {
			t.Errorf("ParseSegmentFileName(%q) = %d, %v; want %d, true", name, got, ok, seg)
		}
	}

	for _, bad := range []string{"", "wal-000001.tmp", "000001.log", "wal-abc.log", "wal-000001.log.tmp"} {
		if _, ok := ParseSegmentFileName(bad); ok {
			t.Errorf("ParseSegmentFileName(%q) = ok, want not ok", bad)
		}
	}
}

func TestCreateSegmentFailsIfExists(t *testing.T) {
	dir := t.TempDir()
	w := mustCreate(t, dir, 1)
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := CreateSegment(dir, 1); err == nil {
		t.Fatalf("expected error creating a segment that already exists")
	}
}

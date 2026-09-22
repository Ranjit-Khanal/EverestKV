package manifest

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMissingReturnsZeroValue(t *testing.T) {
	dir := t.TempDir()
	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.SSTables) != 0 || m.WALSafeDeleteBelow != 0 {
		t.Fatalf("Load on fresh dir = %+v, want zero value", m)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Manifest{SSTables: []uint64{1, 2, 5}, WALSafeDeleteBelow: 3}

	if err := Save(dir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestSaveOverwritesPreviousAtomically(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, Manifest{SSTables: []uint64{1}, WALSafeDeleteBelow: 1}); err != nil {
		t.Fatalf("Save 1: %v", err)
	}
	want := Manifest{SSTables: []uint64{1, 2, 3}, WALSafeDeleteBelow: 2}
	if err := Save(dir, want); err != nil {
		t.Fatalf("Save 2: %v", err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}

	if _, err := os.Stat(filepath.Join(dir, fileName+".tmp")); !os.IsNotExist(err) {
		t.Fatalf("temp manifest file left behind after Save")
	}
}

func TestLoadIgnoresStrayTempFile(t *testing.T) {
	dir := t.TempDir()
	want := Manifest{SSTables: []uint64{7}, WALSafeDeleteBelow: 4}
	if err := Save(dir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Simulate a crash mid-Save on a later update: a temp file is present
	// but was never renamed over the real manifest.
	if err := os.WriteFile(Path(dir)+".tmp", []byte("not valid json{{{"), 0o644); err != nil {
		t.Fatalf("write stray temp file: %v", err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %+v, want %+v (stray temp file must be ignored)", got, want)
	}
}

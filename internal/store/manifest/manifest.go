// Package manifest tracks which SSTables are live and which WAL segments
// have been fully flushed into them, so recovery knows what to trust on
// disk and what to replay.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// fileName is the manifest's fixed on-disk name. It is always replaced
// atomically in place; there is no versioned history.
const fileName = "MANIFEST"

// Manifest is the durable record of database state needed for recovery.
type Manifest struct {
	// SSTables lists the ids of all live SSTables. Any *.sst file on disk
	// whose id is not in this list is leftover from a crash (e.g. mid-flush
	// or mid-manifest-update) and must be ignored, not trusted.
	SSTables []uint64 `json:"sstables"`

	// WALSafeDeleteBelow is the smallest WAL segment number that still
	// needs to be replayed. Every segment with a lower number has had its
	// data fully captured by an SSTable already listed here and may be
	// deleted.
	WALSafeDeleteBelow uint64 `json:"wal_safe_delete_below"`
}

// Path returns the manifest file's path within dir.
func Path(dir string) string {
	return filepath.Join(dir, fileName)
}

// Load reads the manifest from dir. A missing manifest is not an error: it
// means dir holds a fresh database, and a zero-value Manifest is returned.
func Load(dir string) (Manifest, error) {
	data, err := os.ReadFile(Path(dir))
	if os.IsNotExist(err) {
		return Manifest{}, nil
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest: read: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("manifest: decode: %w", err)
	}
	return m, nil
}

// Save atomically replaces the manifest in dir with m: write to a temp
// file, fsync it, rename over the previous manifest, then fsync the
// directory so the rename is itself durable.
func Save(dir string, m Manifest) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("manifest: encode: %w", err)
	}

	finalPath := Path(dir)
	tmpPath := finalPath + ".tmp"

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("manifest: create temp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("manifest: write temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("manifest: sync temp: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("manifest: close temp: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("manifest: rename: %w", err)
	}

	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("manifest: open dir: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("manifest: sync dir: %w", err)
	}

	return nil
}

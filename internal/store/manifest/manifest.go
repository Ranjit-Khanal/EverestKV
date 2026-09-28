// Package manifest records the live SSTables and which WAL segments are flushed.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// fileName is the manifest file. It is replaced atomically.
const fileName = "MANIFEST"

// Manifest is the state recovery needs.
type Manifest struct {
	// SSTables lists live table ids. Any other .sst file is crash leftover.
	SSTables []uint64 `json:"sstables"`

	// WALSafeDeleteBelow is the first segment to replay. Lower ones can be deleted.
	WALSafeDeleteBelow uint64 `json:"wal_safe_delete_below"`
}

// Path returns the manifest path in dir.
func Path(dir string) string {
	return filepath.Join(dir, fileName)
}

// Load reads the manifest. A missing file means a fresh database.
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

// Save atomically replaces the manifest with m.
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

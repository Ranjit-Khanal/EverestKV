package store

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/Ranjit-Khanal/everestkv/internal/store/memtable"
	"github.com/Ranjit-Khanal/everestkv/internal/store/wal"
)

// findWALSegments returns the segment numbers of every WAL segment file
// present in dir, in ascending order.
func findWALSegments(dir string) ([]uint64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var segments []uint64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if seg, ok := wal.ParseSegmentFileName(e.Name()); ok {
			segments = append(segments, seg)
		}
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i] < segments[j] })
	return segments, nil
}

// replaySegment applies every record in WAL segment seg to mt, and updates
// *maxSeq to the highest sequence number seen.
func replaySegment(dir string, seg uint64, mt *memtable.Memtable, maxSeq *uint64) error {
	r, err := wal.OpenSegmentReader(dir, seg)
	if err != nil {
		return err
	}
	defer r.Close()

	for {
		rec, err := r.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read record: %w", err)
		}

		switch rec.Type {
		case wal.RecordDelete:
			mt.Delete(rec.Seq, rec.Key)
		default:
			mt.Put(rec.Seq, rec.Key, rec.Value)
		}
		if rec.Seq > *maxSeq {
			*maxSeq = rec.Seq
		}
	}
}

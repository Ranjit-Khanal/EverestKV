package store

import (
	"fmt"
	"sync"

	"github.com/Ranjit-Khanal/everestkv/internal/store/wal"
)

// commitLog writes to a WAL segment using the chosen SyncMode.
type commitLog struct {
	w segmentWriter

	// mu serializes append+sync in SyncEveryWrite mode.
	mu sync.Mutex
	gc *groupCommit
}

func newCommitLog(w segmentWriter, mode SyncMode) *commitLog {
	cl := &commitLog{w: w}
	if mode == GroupCommit {
		cl.gc = newGroupCommit(w)
	}
	return cl
}

func createCommitLog(dir string, segment uint64, mode SyncMode) (*commitLog, error) {
	w, err := wal.CreateSegment(dir, segment)
	if err != nil {
		return nil, err
	}
	return newCommitLog(w, mode), nil
}

// append writes rec and waits until it is durable.
func (cl *commitLog) append(rec wal.Record) error {
	if cl.gc != nil {
		return cl.gc.commit(rec)
	}

	cl.mu.Lock()
	defer cl.mu.Unlock()
	if err := cl.w.Append(rec); err != nil {
		return err
	}
	return cl.w.Sync()
}

func (cl *commitLog) segment() uint64 {
	return cl.w.Segment()
}

func (cl *commitLog) sync() error {
	return cl.w.Sync()
}

func (cl *commitLog) close() error {
	if err := cl.w.Close(); err != nil {
		return fmt.Errorf("store: close wal segment %d: %w", cl.w.Segment(), err)
	}
	return nil
}

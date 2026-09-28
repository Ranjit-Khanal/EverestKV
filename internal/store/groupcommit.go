package store

import (
	"sync"

	"github.com/Ranjit-Khanal/everestkv/internal/store/wal"
)

// segmentWriter is the part of *wal.Writer group commit uses. Tests fake it.
type segmentWriter interface {
	Append(rec wal.Record) error
	Sync() error
	Segment() uint64
	Close() error
}

// groupCommit shares one fsync across concurrent writers. The first writer with
// no fsync running leads the round; commit returns once an fsync covers its append.
type groupCommit struct {
	w segmentWriter

	mu       sync.Mutex
	cond     *sync.Cond
	appended uint64 // records appended so far
	synced   uint64 // records confirmed fsynced
	flushing bool

	// failUpTo/failErr record the last failed sync and what it covered.
	failUpTo uint64
	failErr  error
}

func newGroupCommit(w segmentWriter) *groupCommit {
	g := &groupCommit{w: w}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// commit appends rec and waits for an fsync that covers it.
func (g *groupCommit) commit(rec wal.Record) error {
	g.mu.Lock()

	// Append under g.mu so any later Sync is sure to include it.
	if err := g.w.Append(rec); err != nil {
		g.mu.Unlock()
		return err
	}
	g.appended++
	ticket := g.appended

	for {
		if g.synced >= ticket {
			g.mu.Unlock()
			return nil
		}
		if g.failUpTo >= ticket {
			err := g.failErr
			g.mu.Unlock()
			return err
		}
		if !g.flushing {
			g.flushing = true
			break // become leader for this round
		}
		g.cond.Wait()
	}

	target := g.appended // this round covers everything appended so far
	g.mu.Unlock()

	err := g.w.Sync()

	g.mu.Lock()
	if err != nil {
		g.failUpTo = target
		g.failErr = err
	} else {
		g.synced = target
	}
	g.flushing = false
	g.cond.Broadcast()
	g.mu.Unlock()

	if err != nil {
		return err
	}
	return nil
}

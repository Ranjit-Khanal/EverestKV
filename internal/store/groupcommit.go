package store

import (
	"sync"

	"github.com/Ranjit-Khanal/everestkv/internal/store/wal"
)

// segmentWriter is the subset of *wal.Writer that group commit needs. It
// exists so tests can substitute a fake and observe/control fsync timing
// without touching the filesystem.
type segmentWriter interface {
	Append(rec wal.Record) error
	Sync() error
	Segment() uint64
	Close() error
}

// groupCommit batches concurrent Append calls into shared fsyncs: the first
// caller to find no fsync in flight becomes the leader for a round, syncing
// everything appended up to that point; anyone who appended after that
// snapshot becomes part of the next round instead, with one of them
// electing itself leader in turn. A caller's commit only ever returns once
// an fsync call that is guaranteed (by happens-before through appendMu) to
// include its own append has completed.
type groupCommit struct {
	w segmentWriter

	mu       sync.Mutex
	cond     *sync.Cond
	appended uint64 // records appended so far
	synced   uint64 // records confirmed fsynced
	flushing bool

	// failUpTo/failErr record that a sync attempt covering records up to
	// failUpTo failed with failErr. They reset (failUpTo advances again)
	// each time a new failure occurs; a successful sync only ever advances
	// synced, never touches these.
	failUpTo uint64
	failErr  error
}

func newGroupCommit(w segmentWriter) *groupCommit {
	g := &groupCommit{w: w}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// commit appends rec and blocks until an fsync covering it has completed
// (or definitively failed).
func (g *groupCommit) commit(rec wal.Record) error {
	g.mu.Lock()

	// The append itself happens under g.mu, so every append that observes
	// a later Sync call has not yet happened is guaranteed (by mutex
	// ordering) to complete before that Sync call is made — which is
	// exactly what's required for the fsync to actually cover it.
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

	target := g.appended // everything appended so far is covered by this round
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

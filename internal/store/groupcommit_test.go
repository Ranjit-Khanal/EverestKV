package store

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Ranjit-Khanal/everestkv/internal/store/wal"
)

// fakeSegmentWriter is an in-memory segmentWriter used to observe and
// control fsync timing without touching the filesystem.
type fakeSegmentWriter struct {
	mu        sync.Mutex
	appended  []wal.Record
	syncCount int
	syncErr   error

	// syncGate, if non-nil, is closed by the test to let a blocked Sync
	// call proceed — used to force multiple appends to pile up while one
	// Sync is in flight.
	syncGate chan struct{}
	syncing  chan struct{} // closed by Sync to signal "I've started"
}

func (f *fakeSegmentWriter) Append(rec wal.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appended = append(f.appended, rec)
	return nil
}

func (f *fakeSegmentWriter) Sync() error {
	f.mu.Lock()
	f.syncCount++
	gate := f.syncGate
	syncing := f.syncing
	f.syncing = nil // only signal "sync started" once, for the first call
	err := f.syncErr
	f.mu.Unlock()

	if syncing != nil {
		close(syncing)
	}
	if gate != nil {
		<-gate
	}
	return err
}

func (f *fakeSegmentWriter) Segment() uint64 { return 1 }
func (f *fakeSegmentWriter) Close() error    { return nil }

func (f *fakeSegmentWriter) count() (appended, syncs int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.appended), f.syncCount
}

func TestGroupCommitSingleWriter(t *testing.T) {
	f := &fakeSegmentWriter{}
	gc := newGroupCommit(f)

	if err := gc.commit(wal.Record{Seq: 1, Type: wal.RecordPut, Key: []byte("a")}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	appended, syncs := f.count()
	if appended != 1 || syncs != 1 {
		t.Fatalf("appended=%d syncs=%d, want 1, 1", appended, syncs)
	}
}

func TestGroupCommitBatchesConcurrentWriters(t *testing.T) {
	f := &fakeSegmentWriter{
		syncGate: make(chan struct{}),
		syncing:  make(chan struct{}),
	}
	gc := newGroupCommit(f)

	const n = 20
	var wg sync.WaitGroup
	var succeeded atomic.Int64

	// Kick off the leader first and wait until its Sync call has actually
	// started, so the rest reliably pile up as latecomers while it's in
	// flight rather than racing to be leader themselves.
	syncing := f.syncing // capture before any goroutine can mutate f.syncing
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := gc.commit(wal.Record{Seq: 1, Type: wal.RecordPut, Key: []byte("leader")}); err != nil {
			t.Errorf("leader commit: %v", err)
			return
		}
		succeeded.Add(1)
	}()
	<-syncing

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := wal.Record{Seq: uint64(i + 2), Type: wal.RecordPut, Key: []byte("k")}
			if err := gc.commit(rec); err != nil {
				t.Errorf("commit %d: %v", i, err)
				return
			}
			succeeded.Add(1)
		}(i)
	}

	// Wait until every latecomer has actually appended (and so, given
	// flushing is still true the whole time, joined the wait rather than
	// racing off to become its own leader) before letting the leader's
	// Sync proceed. Appends are cheap mutex-protected slice writes, so
	// this converges immediately; without it, slow goroutine scheduling
	// could let the gate open before everyone has queued up, understating
	// how much batching actually happens.
	for {
		appended, _ := f.count()
		if appended == n+1 {
			break
		}
	}

	// Let the leader's Sync (and any it triggers next) proceed.
	close(f.syncGate)
	wg.Wait()

	if got := succeeded.Load(); got != n+1 {
		t.Fatalf("succeeded = %d, want %d", got, n+1)
	}
	appended, syncs := f.count()
	if appended != n+1 {
		t.Fatalf("appended = %d, want %d", appended, n+1)
	}
	// The whole point of group commit: far fewer fsyncs than writers.
	if syncs >= n+1 {
		t.Fatalf("syncs = %d, want fewer than %d (writers should have batched)", syncs, n+1)
	}
	t.Logf("%d writers batched into %d syncs", n+1, syncs)
}

func TestGroupCommitPropagatesSyncError(t *testing.T) {
	wantErr := errors.New("disk full")
	f := &fakeSegmentWriter{syncErr: wantErr}
	gc := newGroupCommit(f)

	err := gc.commit(wal.Record{Seq: 1, Type: wal.RecordPut, Key: []byte("a")})
	if !errors.Is(err, wantErr) {
		t.Fatalf("commit error = %v, want %v", err, wantErr)
	}

	// A later commit gets its own fresh sync attempt, independent of the
	// earlier failure.
	f.mu.Lock()
	f.syncErr = nil
	f.mu.Unlock()
	if err := gc.commit(wal.Record{Seq: 2, Type: wal.RecordPut, Key: []byte("b")}); err != nil {
		t.Fatalf("commit after clearing error: %v", err)
	}
}

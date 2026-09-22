package store

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Ranjit-Khanal/everestkv/internal/store/manifest"
	"github.com/Ranjit-Khanal/everestkv/internal/store/wal"
)

func mustOpen(t *testing.T, dir string, opts Options) *DB {
	t.Helper()
	db, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return db
}

func TestPutGetDelete(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir, Options{})
	defer db.Close()

	if err := db.Put([]byte("k1"), []byte("v1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	v, ok, err := db.Get([]byte("k1"))
	if err != nil || !ok || !bytes.Equal(v, []byte("v1")) {
		t.Fatalf("Get(k1) = %q, %v, %v; want v1, true, nil", v, ok, err)
	}

	if err := db.Delete([]byte("k1")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, ok, err = db.Get([]byte("k1"))
	if err != nil || ok {
		t.Fatalf("Get(k1) after delete = ok=%v err=%v; want false, nil", ok, err)
	}

	_, ok, err = db.Get([]byte("never-written"))
	if err != nil || ok {
		t.Fatalf("Get(never-written) = ok=%v err=%v; want false, nil", ok, err)
	}
}

func TestWriteOrderIsDurableBeforeAck(t *testing.T) {
	// A write must be durable in the log before it's acknowledged. We
	// can't directly observe fsync ordering from the public API, but we
	// can confirm the end-to-end contract: once Put returns, killing the
	// process (simulated by closing files without any graceful shutdown)
	// and reopening must still show the write.
	dir := t.TempDir()
	db := mustOpen(t, dir, Options{})

	if err := db.Put([]byte("durable"), []byte("yes")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Simulate a crash: close the underlying WAL file handle directly
	// without going through the normal Close path (which would be a
	// clean shutdown, not a crash).
	if err := db.log.w.Close(); err != nil {
		t.Fatalf("close underlying wal file: %v", err)
	}

	db2 := mustOpen(t, dir, Options{})
	defer db2.Close()
	v, ok, err := db2.Get([]byte("durable"))
	if err != nil || !ok || !bytes.Equal(v, []byte("yes")) {
		t.Fatalf("Get(durable) after crash+reopen = %q, %v, %v; want yes, true, nil", v, ok, err)
	}
}

// peekImmutableCount reports the current length of db.immutables. It's a
// white-box helper (same package) used only to observe internal queue
// behavior in tests; there is no public equivalent.
func peekImmutableCount(db *DB) int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return len(db.immutables)
}

func TestRotationContinuesWhileFlushInProgress(t *testing.T) {
	dir := t.TempDir()
	// A generous MaxImmutableMemtables means backpressure should never
	// kick in here, isolating what this test checks: that writing past
	// the threshold doesn't wait for the resulting flush to finish. Many
	// concurrent writers on GroupCommit are used so they can collectively
	// rotate memtables far faster than the single background goroutine can
	// flush them, making a backlog reliably observable rather than
	// timing-dependent (and keeping this fast: SyncEveryWrite's per-call
	// fsync would otherwise dominate the runtime here).
	db := mustOpen(t, dir, Options{MemtableSizeThreshold: 1024, MaxImmutableMemtables: 100, SyncMode: GroupCommit})
	defer db.Close()

	stopMonitor := make(chan struct{})
	maxObserved := 0
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		for {
			select {
			case <-stopMonitor:
				return
			default:
				if n := peekImmutableCount(db); n > maxObserved {
					maxObserved = n
				}
				time.Sleep(100 * time.Microsecond)
			}
		}
	}()

	const goroutines = 16
	const perGoroutine = 100
	val := bytes.Repeat([]byte("x"), 50)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				key := []byte(fmt.Sprintf("g%d-key-%04d", g, i))
				if err := db.Put(key, val); err != nil {
					t.Errorf("Put: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(stopMonitor)
	<-monitorDone

	if maxObserved < 2 {
		t.Fatalf("max observed pending immutable memtables = %d, want >= 2 (writes should outrun the flush goroutine at least once)", maxObserved)
	}

	// One final write, guaranteed to land after everything above: it's
	// almost certainly still in the active (unflushed) memtable, so it's a
	// safe check given Get doesn't yet consult flushed SSTables.
	if err := db.Put([]byte("final"), val); err != nil {
		t.Fatalf("Put(final): %v", err)
	}
	v, ok, err := db.Get([]byte("final"))
	if err != nil || !ok || !bytes.Equal(v, val) {
		t.Fatalf("Get(final) = %q, %v, %v; want present", v, ok, err)
	}
}

func TestBackpressureBlocksThenUnblocks(t *testing.T) {
	dir := t.TempDir()
	// Tiny threshold and a max of 1 pending immutable memtable, so a
	// second rotation has to wait for the first flush.
	const maxImmutables = 1
	db := mustOpen(t, dir, Options{MemtableSizeThreshold: 256, MaxImmutableMemtables: maxImmutables})
	defer db.Close()

	stopMonitor := make(chan struct{})
	violated := false
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		for {
			select {
			case <-stopMonitor:
				return
			default:
				if peekImmutableCount(db) > maxImmutables {
					violated = true
				}
				time.Sleep(100 * time.Microsecond)
			}
		}
	}()

	val := bytes.Repeat([]byte("x"), 50)
	const n = 300
	done := make(chan error, 1)
	go func() {
		for i := 0; i < n; i++ {
			key := []byte(fmt.Sprintf("key-%04d", i))
			if err := db.Put(key, val); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	if err := <-done; err != nil {
		t.Fatalf("Put under backpressure: %v", err)
	}
	close(stopMonitor)
	<-monitorDone

	if violated {
		t.Fatalf("pending immutable memtables exceeded MaxImmutableMemtables=%d at some point; backpressure did not hold", maxImmutables)
	}

	// The last write must have gone through (proves the loop actually
	// finished rather than being silently cut short) and, being the most
	// recent, is almost certainly still in the active memtable.
	lastKey := []byte(fmt.Sprintf("key-%04d", n-1))
	if _, ok, err := db.Get(lastKey); err != nil || !ok {
		t.Fatalf("Get(%s) missing after backpressured writes: ok=%v err=%v", lastKey, ok, err)
	}
}

func TestCrashRecoveryReplaysUnflushedWrites(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir, Options{})

	const n = 100
	for i := 0; i < n; i++ {
		key := []byte(fmt.Sprintf("k%03d", i))
		val := []byte(fmt.Sprintf("v%03d", i))
		if err := db.Put(key, val); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	if err := db.Delete([]byte("k050")); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Simulate a crash: close the WAL file handle directly, skipping the
	// normal flush-pending-work/close-cleanly path entirely. The active
	// memtable's contents only ever existed in memory and the WAL.
	if err := db.log.w.Close(); err != nil {
		t.Fatalf("close underlying wal file: %v", err)
	}

	db2 := mustOpen(t, dir, Options{})
	defer db2.Close()

	for i := 0; i < n; i++ {
		key := []byte(fmt.Sprintf("k%03d", i))
		if i == 50 {
			_, ok, err := db2.Get(key)
			if err != nil || ok {
				t.Fatalf("Get(%s) after recovery = ok=%v err=%v; want deleted", key, ok, err)
			}
			continue
		}
		want := []byte(fmt.Sprintf("v%03d", i))
		v, ok, err := db2.Get(key)
		if err != nil || !ok || !bytes.Equal(v, want) {
			t.Fatalf("Get(%s) after recovery = %q, %v, %v; want %q, true, nil", key, v, ok, err, want)
		}
	}
}

func TestCrashMidFlushIgnoresPartialSSTable(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir, Options{})

	if err := db.Put([]byte("survivor"), []byte("still-here")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := db.log.w.Close(); err != nil { // fsync what we have, then "crash"
		t.Fatalf("close wal: %v", err)
	}

	// Simulate a crash partway through a flush: an SSTable file exists on
	// disk (as if Finish's rename had just completed, or even mid-write)
	// but the manifest was never updated to list it, because that step
	// never happened before the crash.
	orphanPath := filepath.Join(dir, "000001.sst")
	if err := os.WriteFile(orphanPath, []byte("not a real sstable, just bytes on disk"), 0o644); err != nil {
		t.Fatalf("write orphan sstable: %v", err)
	}
	orphanTmpPath := filepath.Join(dir, "000002.sst.tmp")
	if err := os.WriteFile(orphanTmpPath, []byte("partial write, never finished"), 0o644); err != nil {
		t.Fatalf("write orphan temp sstable: %v", err)
	}

	db2 := mustOpen(t, dir, Options{})
	defer db2.Close()

	// Reopening must not error out or try to interpret the orphan files,
	// and the data must come back from the WAL, not the (bogus) SSTable.
	v, ok, err := db2.Get([]byte("survivor"))
	if err != nil || !ok || !bytes.Equal(v, []byte("still-here")) {
		t.Fatalf("Get(survivor) = %q, %v, %v; want still-here, true, nil", v, ok, err)
	}

	// The orphan files must still be untouched/ignored, not adopted into
	// the manifest.
	m, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("manifest.Load: %v", err)
	}
	for _, id := range m.SSTables {
		if id == 1 || id == 2 {
			t.Fatalf("manifest lists orphan sstable id %d as live: %+v", id, m)
		}
	}
	if _, err := os.Stat(orphanPath); err != nil {
		t.Fatalf("orphan sstable file should still exist untouched: %v", err)
	}
}

func TestRealFlushProducesManifestEntryAndDeletesWAL(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir, Options{MemtableSizeThreshold: 200, MaxImmutableMemtables: 2})

	val := bytes.Repeat([]byte("v"), 50)
	// Enough writes to force at least one full rotation+flush cycle.
	for i := 0; i < 20; i++ {
		key := []byte(fmt.Sprintf("k%02d", i))
		if err := db.Put(key, val); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	m, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("manifest.Load: %v", err)
	}
	if len(m.SSTables) == 0 {
		t.Fatalf("expected at least one flushed sstable, manifest = %+v", m)
	}
	if _, err := os.Stat(filepath.Join(dir, "000001.sst")); err != nil {
		t.Fatalf("expected 000001.sst on disk: %v", err)
	}

	// Every WAL segment below WALSafeDeleteBelow must be gone.
	for seg := uint64(1); seg < m.WALSafeDeleteBelow; seg++ {
		if _, err := os.Stat(wal.SegmentPath(dir, seg)); !os.IsNotExist(err) {
			t.Fatalf("wal segment %d should have been deleted after flush, stat err = %v", seg, err)
		}
	}
}

func TestConcurrentPutsGroupCommit(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir, Options{SyncMode: GroupCommit})
	defer db.Close()

	const goroutines = 16
	const perGoroutine = 50
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				key := []byte(fmt.Sprintf("g%d-k%d", g, i))
				val := []byte(fmt.Sprintf("v%d", i))
				if err := db.Put(key, val); err != nil {
					t.Errorf("Put: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	for g := 0; g < goroutines; g++ {
		for i := 0; i < perGoroutine; i++ {
			key := []byte(fmt.Sprintf("g%d-k%d", g, i))
			want := []byte(fmt.Sprintf("v%d", i))
			v, ok, err := db.Get(key)
			if err != nil || !ok || !bytes.Equal(v, want) {
				t.Fatalf("Get(%s) = %q, %v, %v; want %q, true, nil", key, v, ok, err, want)
			}
		}
	}
}

func TestCloseIsIdempotentAndRejectsWritesAfter(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir, Options{})

	if err := db.Put([]byte("a"), []byte("1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := db.Put([]byte("b"), []byte("2")); err != ErrClosed {
		t.Fatalf("Put after Close = %v, want ErrClosed", err)
	}
}

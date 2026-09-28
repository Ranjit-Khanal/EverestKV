package store

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/Ranjit-Khanal/everestkv/internal/store/manifest"
	"github.com/Ranjit-Khanal/everestkv/internal/store/memtable"
	"github.com/Ranjit-Khanal/everestkv/internal/store/sstable"
	"github.com/Ranjit-Khanal/everestkv/internal/store/wal"
)

// immutableMemtable is a frozen memtable waiting to be flushed, plus the
// range of WAL segments whose data it captures. The range is usually a
// single segment (one rotation = one segment), except for the first
// memtable after a recovery that replayed several not-yet-flushed
// segments, which spans all of them plus the fresh segment writes land in
// until it too rotates.
type immutableMemtable struct {
	mt              *memtable.Memtable
	firstWALSegment uint64
	lastWALSegment  uint64
}

// DB is an LSM-tree key-value store: writes go to a write-ahead log and an
// in-memory memtable; the memtable is periodically flushed to an immutable,
// sorted SSTable on disk. Reads check the memtables first, then the
// SSTables from newest to oldest.
type DB struct {
	dir  string
	opts Options

	seqCounter atomic.Uint64

	// mu guards active/log (read via RLock by writers, swapped via Lock by
	// rotate), immutables/closed/flushErr (read/written via Lock by
	// writers, rotate, and the flush goroutine), and tables (read via
	// RLock by Get, appended via Lock by the flush goroutine). Using one RWMutex for both
	// lets writers proceed concurrently (RLock) while still giving rotate
	// and Close an exclusive, consistent view when they need to swap state
	// or drain in-flight writers.
	mu             sync.RWMutex
	active         *memtable.Memtable
	log            *commitLog
	activeWALFirst uint64 // lower bound of WAL segments db.active's data depends on
	immutables     []*immutableMemtable
	tables         []*sstable.Reader // live SSTables, oldest first
	closed         bool
	flushErr       error // sticky error from the background flush goroutine

	flushCond *sync.Cond // condition over mu; signaled on new immutable work, freed backpressure slots, flush errors, and Close

	nextWALSegment uint64
	nextSSTableID  uint64
	liveSSTables   []uint64 // touched only by the single flush goroutine after Open

	flushWG sync.WaitGroup
}

// Open opens (or creates) a database in dir: it loads the manifest, replays
// whatever WAL segments still need it into a fresh memtable, and starts
// accepting writes into a new WAL segment.
func Open(dir string, opts Options) (*DB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("store: mkdir %s: %w", dir, err)
	}
	opts = opts.withDefaults()

	man, err := manifest.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("store: load manifest: %w", err)
	}
	liveSSTables := append([]uint64(nil), man.SSTables...)
	// SSTable ids are assigned in flush order, so ascending id is oldest
	// to newest, which is the order Get relies on.
	sort.Slice(liveSSTables, func(i, j int) bool { return liveSSTables[i] < liveSSTables[j] })

	tables, err := openTables(dir, liveSSTables)
	if err != nil {
		return nil, err
	}
	// Until Open succeeds, any error must release the readers opened above.
	ok := false
	defer func() {
		if !ok {
			closeTables(tables)
		}
	}()

	segments, err := findWALSegments(dir)
	if err != nil {
		return nil, fmt.Errorf("store: scan wal segments: %w", err)
	}

	active := memtable.New()
	var maxSeq uint64
	var replayedFirst uint64
	replayedAny := false
	for _, seg := range segments {
		if seg < man.WALSafeDeleteBelow {
			continue
		}
		if !replayedAny {
			replayedFirst = seg
			replayedAny = true
		}
		if err := replaySegment(dir, seg, active, &maxSeq); err != nil {
			return nil, fmt.Errorf("store: replay wal segment %d: %w", seg, err)
		}
	}

	nextSeg := uint64(1)
	if len(segments) > 0 {
		nextSeg = segments[len(segments)-1] + 1
	}
	log, err := createCommitLog(dir, nextSeg, opts.SyncMode)
	if err != nil {
		return nil, fmt.Errorf("store: create wal segment %d: %w", nextSeg, err)
	}

	activeWALFirst := nextSeg
	if replayedAny {
		activeWALFirst = replayedFirst
	}

	nextSSTableID := uint64(1)
	if len(liveSSTables) > 0 {
		nextSSTableID = liveSSTables[len(liveSSTables)-1] + 1
	}

	db := &DB{
		dir:            dir,
		opts:           opts,
		active:         active,
		log:            log,
		activeWALFirst: activeWALFirst,
		tables:         tables,
		nextWALSegment: nextSeg + 1,
		nextSSTableID:  nextSSTableID,
		liveSSTables:   liveSSTables,
	}
	db.seqCounter.Store(maxSeq)
	db.flushCond = sync.NewCond(&db.mu)

	db.flushWG.Add(1)
	go db.flushLoop()

	ok = true
	return db, nil
}

// openTables opens a reader for each SSTable id, in the given order. On
// error it closes any readers it already opened.
func openTables(dir string, ids []uint64) ([]*sstable.Reader, error) {
	tables := make([]*sstable.Reader, 0, len(ids))
	for _, id := range ids {
		r, err := sstable.Open(dir, id)
		if err != nil {
			closeTables(tables)
			return nil, fmt.Errorf("store: open sstable %d: %w", id, err)
		}
		tables = append(tables, r)
	}
	return tables, nil
}

func closeTables(tables []*sstable.Reader) error {
	var errs []error
	for _, r := range tables {
		if err := r.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Put stores value under key, overwriting any existing value.
func (db *DB) Put(key, value []byte) error {
	return db.write(wal.RecordPut, key, value)
}

// Delete records a tombstone for key.
func (db *DB) Delete(key []byte) error {
	return db.write(wal.RecordDelete, key, nil)
}

// Get returns the most recent value written for key. It checks, newest
// data first, the active memtable, the pending immutable memtables, and
// then the live SSTables; the first entry found for key wins, so a
// tombstone (delete) shadows every older value.
//
// Get holds a read lock throughout, including while reading SSTables from
// disk. That doesn't block writers (which also take a read lock), and it
// guarantees a flush cannot move a key from memory to disk mid-lookup, or
// Close release a table's file, while Get is using it.
func (db *DB) Get(key []byte) ([]byte, bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return nil, false, ErrClosed
	}

	if v, tomb, found := db.active.Get(key); found {
		return v, !tomb, nil
	}
	for i := len(db.immutables) - 1; i >= 0; i-- {
		if v, tomb, found := db.immutables[i].mt.Get(key); found {
			return v, !tomb, nil
		}
	}
	for i := len(db.tables) - 1; i >= 0; i-- {
		v, tomb, found, err := db.tables[i].Get(key)
		if err != nil {
			return nil, false, fmt.Errorf("store: get: %w", err)
		}
		if found {
			return v, !tomb, nil
		}
	}
	return nil, false, nil
}

func (db *DB) write(typ wal.RecordType, key, value []byte) error {
	db.mu.RLock()
	if db.closed {
		db.mu.RUnlock()
		return ErrClosed
	}
	if err := db.flushErr; err != nil {
		db.mu.RUnlock()
		return err
	}

	active := db.active
	log := db.log
	seq := db.seqCounter.Add(1)
	rec := wal.Record{Seq: seq, Type: typ, Key: key, Value: value}

	// Steps 1-2: append to the WAL and make it durable per the configured
	// sync mode. This, and the memtable insert below, happen while holding
	// the read lock: many writers can be in this section concurrently
	// (which is what lets GroupCommit batch their fsyncs), but rotate()
	// (which needs the write lock) cannot swap db.active/db.log out from
	// under an in-flight writer until every current holder finishes.
	if err := log.append(rec); err != nil {
		db.mu.RUnlock()
		return fmt.Errorf("store: append to wal: %w", err)
	}

	// Step 3: insert into the active memtable. This only happens after the
	// append above has returned, so a write is never acknowledged before
	// it is durable in the log.
	if typ == wal.RecordDelete {
		active.Delete(seq, key)
	} else {
		active.Put(seq, key, value)
	}
	size := active.Size()
	db.mu.RUnlock()

	// Step 4: return success. If this write pushed the memtable over its
	// size threshold, rotate it now; rotation itself is cheap (new file +
	// pointer swap), so writes don't wait for the actual flush unless the
	// pending-flush queue is already full (backpressure).
	if size >= db.opts.MemtableSizeThreshold {
		if err := db.rotate(); err != nil {
			return err
		}
	}
	return nil
}

// rotate freezes the active memtable and starts a fresh one with a new WAL
// segment, if the active memtable is still over threshold (a concurrent
// caller may have already rotated it). It blocks, holding out all new
// writes, if the immutable queue is already full.
func (db *DB) rotate() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.active.Size() < db.opts.MemtableSizeThreshold {
		return nil
	}

	for len(db.immutables) >= db.opts.MaxImmutableMemtables {
		if db.flushErr != nil {
			return db.flushErr
		}
		if db.closed {
			return ErrClosed
		}
		db.flushCond.Wait()
	}
	if db.flushErr != nil {
		return db.flushErr
	}

	seg := db.nextWALSegment
	db.nextWALSegment++
	newLog, err := createCommitLog(db.dir, seg, db.opts.SyncMode)
	if err != nil {
		return fmt.Errorf("store: create wal segment %d: %w", seg, err)
	}

	frozen := db.active
	frozenLog := db.log
	db.active = memtable.New()
	db.log = newLog

	db.immutables = append(db.immutables, &immutableMemtable{
		mt:              frozen,
		firstWALSegment: db.activeWALFirst,
		lastWALSegment:  frozenLog.segment(),
	})
	db.activeWALFirst = seg

	db.flushCond.Broadcast()

	if err := frozenLog.close(); err != nil {
		return err
	}
	return nil
}

// flushLoop is the single background goroutine that flushes immutable
// memtables to SSTables, one at a time, oldest first.
func (db *DB) flushLoop() {
	defer db.flushWG.Done()

	for {
		db.mu.Lock()
		for len(db.immutables) == 0 && !db.closed {
			db.flushCond.Wait()
		}
		if len(db.immutables) == 0 {
			db.mu.Unlock()
			return
		}
		imm := db.immutables[0]
		db.mu.Unlock()

		table, err := db.flushOne(imm)
		if err != nil {
			db.mu.Lock()
			db.flushErr = err
			db.flushCond.Broadcast()
			db.mu.Unlock()
			return
		}

		// Publish the SSTable and retire the memtable in one step, so Get
		// always finds the flushed keys in exactly one of the two.
		db.mu.Lock()
		db.tables = append(db.tables, table)
		db.immutables = db.immutables[1:]
		db.flushCond.Broadcast()
		db.mu.Unlock()
	}
}

// flushOne writes imm's memtable out as a new SSTable, records it in the
// manifest, deletes the WAL segments it made obsolete, and returns a reader
// for the new table. Only this goroutine ever touches db.nextSSTableID and
// db.liveSSTables, so no lock is needed for them.
//
// On error, imm stays queued (and readable) and the returned error becomes
// the DB's sticky flushErr.
func (db *DB) flushOne(imm *immutableMemtable) (*sstable.Reader, error) {
	id := db.nextSSTableID

	w, err := sstable.NewWriter(db.dir, id)
	if err != nil {
		return nil, fmt.Errorf("store: create sstable %d: %w", id, err)
	}

	it := imm.mt.NewIterator()
	for it.Next() {
		err := w.Write(sstable.Entry{Key: it.Key(), Value: it.Value(), Tombstone: it.Tombstone()})
		if err != nil {
			w.Abort()
			return nil, fmt.Errorf("store: write sstable %d: %w", id, err)
		}
	}
	if _, err := w.Finish(); err != nil {
		return nil, fmt.Errorf("store: finish sstable %d: %w", id, err)
	}

	db.nextSSTableID++
	db.liveSSTables = append(db.liveSSTables, id)

	err = manifest.Save(db.dir, manifest.Manifest{
		SSTables:           db.liveSSTables,
		WALSafeDeleteBelow: imm.lastWALSegment + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("store: save manifest: %w", err)
	}

	table, err := sstable.Open(db.dir, id)
	if err != nil {
		return nil, fmt.Errorf("store: open flushed sstable %d: %w", id, err)
	}

	// The SSTable and manifest are now durably in place; the WAL segments
	// this memtable came from are redundant. Failing to delete one doesn't
	// lose or corrupt anything (recovery already skips segments below
	// WALSafeDeleteBelow), but it's still surfaced as an error since it
	// usually indicates a real filesystem problem.
	for seg := imm.firstWALSegment; seg <= imm.lastWALSegment; seg++ {
		if err := os.Remove(wal.SegmentPath(db.dir, seg)); err != nil && !os.IsNotExist(err) {
			table.Close()
			return nil, fmt.Errorf("store: delete wal segment %d: %w", seg, err)
		}
	}

	return table, nil
}

// Close stops accepting new writes, waits for any already-queued flushes to
// finish, fsyncs and closes the active WAL segment, and closes the SSTables.
func (db *DB) Close() error {
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return nil
	}
	db.closed = true
	db.flushCond.Broadcast()
	db.mu.Unlock()

	db.flushWG.Wait()

	db.mu.Lock()
	defer db.mu.Unlock()

	var errs []error
	if err := db.log.sync(); err != nil {
		errs = append(errs, err)
	}
	if err := db.log.close(); err != nil {
		errs = append(errs, err)
	}
	if err := closeTables(db.tables); err != nil {
		errs = append(errs, fmt.Errorf("store: close sstables: %w", err))
	}
	db.tables = nil
	if db.flushErr != nil {
		errs = append(errs, db.flushErr)
	}
	return errors.Join(errs...)
}

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

// immutableMemtable is a frozen memtable waiting to flush, and the WAL segments it covers.
type immutableMemtable struct {
	mt              *memtable.Memtable
	firstWALSegment uint64
	lastWALSegment  uint64
}

// DB is an LSM-tree store: WAL + memtable, flushed to SSTables.
// Reads check memtables first, then SSTables newest to oldest.
type DB struct {
	dir  string
	opts Options

	seqCounter atomic.Uint64

	// mu guards the fields up to flushErr. Writers and Get take RLock so they run
	// together; rotate, flush and Close take Lock to swap state.
	mu             sync.RWMutex
	active         *memtable.Memtable
	log            *commitLog
	activeWALFirst uint64 // first WAL segment db.active depends on
	immutables     []*immutableMemtable
	tables         []*sstable.Reader // live SSTables, oldest first
	closed         bool
	flushErr       error // sticky flush error

	flushCond *sync.Cond // signaled when flush work or queue space changes

	nextWALSegment uint64
	nextSSTableID  uint64
	liveSSTables   []uint64 // only the flush goroutine touches this

	flushWG sync.WaitGroup
}

// Open opens or creates a database in dir and replays the WAL.
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
	// Ids grow in flush order, so this is oldest to newest.
	sort.Slice(liveSSTables, func(i, j int) bool { return liveSSTables[i] < liveSSTables[j] })

	tables, err := openTables(dir, liveSSTables)
	if err != nil {
		return nil, err
	}
	// Close the readers if Open fails.
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

	active := memtable.NewMemtable()
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

// openTables opens a reader per id, closing them all on error.
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

// Put stores value under key.
func (db *DB) Put(key, value []byte) error {
	return db.write(wal.RecordPut, key, value)
}

// Delete writes a tombstone for key.
func (db *DB) Delete(key []byte) error {
	return db.write(wal.RecordDelete, key, nil)
}

// Get returns the newest value for key; a tombstone hides older values.
// It holds RLock throughout so a flush or Close can't happen mid-lookup.
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

	// 1-2: append to the WAL and sync. RLock lets writers batch fsyncs
	// while stopping rotate from swapping the log underneath them.
	if err := log.append(rec); err != nil {
		db.mu.RUnlock()
		return fmt.Errorf("store: append to wal: %w", err)
	}

	// 3: insert into the memtable, only after the WAL write is durable.
	if typ == wal.RecordDelete {
		active.Delete(seq, key)
	} else {
		active.Put(seq, key, value)
	}
	size := active.Size()
	db.mu.RUnlock()

	// 4: rotate if the memtable is full. Writes only wait if the flush queue is full.
	if size >= db.opts.MemtableSizeThreshold {
		if err := db.rotate(); err != nil {
			return err
		}
	}
	return nil
}

// rotate freezes the full memtable and starts a new one with a new WAL segment.
// It blocks all writes while the flush queue is full.
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
	db.active = memtable.NewMemtable()
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

// flushLoop flushes frozen memtables to SSTables, oldest first.
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

		// Swap in one step so Get sees the keys in exactly one place.
		db.mu.Lock()
		db.tables = append(db.tables, table)
		db.immutables = db.immutables[1:]
		db.flushCond.Broadcast()
		db.mu.Unlock()
	}
}

// flushOne writes imm to an SSTable, updates the manifest and drops old WAL segments.
// On error imm stays queued and the error sticks.
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

	// The data is on disk now, so the old WAL segments can go. A failed
	// delete loses nothing but still points to a disk problem.
	for seg := imm.firstWALSegment; seg <= imm.lastWALSegment; seg++ {
		if err := os.Remove(wal.SegmentPath(db.dir, seg)); err != nil && !os.IsNotExist(err) {
			table.Close()
			return nil, fmt.Errorf("store: delete wal segment %d: %w", seg, err)
		}
	}

	return table, nil
}

// Close stops writes, finishes queued flushes, and closes the WAL and SSTables.
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

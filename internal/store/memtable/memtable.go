// Package memtable implements the in-memory sorted structure that buffers
// writes between the WAL and an on-disk SSTable flush.
package memtable

import "sync"

// Memtable is a sorted, in-memory table of the latest value (or tombstone)
// per key. It is safe for concurrent use.
type Memtable struct {
	mu   sync.RWMutex
	skl  *skiplist
	size int64
}

// NewMemtable returns an empty Memtable.
func NewMemtable() *Memtable {
	return &Memtable{skl: newSkiplist()}
}

// Put records key=value as of seq, overwriting any earlier state for key.
func (m *Memtable) Put(seq uint64, key, value []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upsert(entry{key: clone(key), value: clone(value), tombstone: false, seq: seq})
}

// Delete records a tombstone for key as of seq, overwriting any earlier
// state for key.
func (m *Memtable) Delete(seq uint64, key []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upsert(entry{key: clone(key), tombstone: true, seq: seq})
}

func (m *Memtable) upsert(e entry) {
	old, existed := m.skl.upsert(e)
	m.size += int64(len(e.key) + len(e.value))
	if existed {
		m.size -= int64(len(old.key) + len(old.value))
	}
}

// Get returns the latest recorded state for key: the value and
// tombstone=false for a live entry, tombstone=true for a deleted entry, or
// found=false if key has never been written in this memtable.
func (m *Memtable) Get(key []byte) (value []byte, tombstone bool, found bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.skl.get(key)
	if !ok {
		return nil, false, false
	}
	return e.value, e.tombstone, true
}

// Size returns the approximate size in bytes of all keys and values
// currently held (excluding tombstones' absent values and skip-list
// bookkeeping overhead).
func (m *Memtable) Size() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.size
}

// Iterator walks a Memtable's entries in ascending key order as of the
// moment NewIterator was called. It does not observe later writes.
type Iterator struct {
	cur *node
}

// NewIterator returns an Iterator positioned before the first entry.
// Call Next to advance to the first entry.
//
// The Iterator walks the underlying skip list nodes directly, without
// holding the Memtable's lock for its whole lifetime, so it must only be
// used on a Memtable that is no longer accepting writes (e.g. one already
// frozen for flushing). Iterating a Memtable concurrently with Put/Delete
// on it is a data race.
func (m *Memtable) NewIterator() *Iterator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return &Iterator{cur: &node{forward: []*node{m.skl.front()}}}
}

// Next advances the iterator and reports whether an entry is available.
func (it *Iterator) Next() bool {
	if it.cur == nil || it.cur.forward[0] == nil {
		return false
	}
	it.cur = it.cur.forward[0]
	return true
}

// Key returns the current entry's key.
func (it *Iterator) Key() []byte { return it.cur.entry.key }

// Value returns the current entry's value (empty for a tombstone).
func (it *Iterator) Value() []byte { return it.cur.entry.value }

// Tombstone reports whether the current entry is a deletion marker.
func (it *Iterator) Tombstone() bool { return it.cur.entry.tombstone }

// Seq returns the sequence number the current entry was written at.
func (it *Iterator) Seq() uint64 { return it.cur.entry.seq }

func clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

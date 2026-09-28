// Package memtable buffers sorted writes in memory until they are flushed to an SSTable.
package memtable

import "sync"

// Memtable holds the latest value or tombstone per key. Safe for concurrent use.
type Memtable struct {
	mu   sync.RWMutex
	skl  *skiplist
	size int64
}

// NewMemtable returns an empty Memtable.
func NewMemtable() *Memtable {
	return &Memtable{skl: newSkiplist()}
}

// Put sets key to value at seq.
func (m *Memtable) Put(seq uint64, key, value []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upsert(entry{key: clone(key), value: clone(value), tombstone: false, seq: seq})
}

// Delete writes a tombstone for key at seq.
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

// Get returns the latest state of key. found is false if key was never written here.
func (m *Memtable) Get(key []byte) (value []byte, tombstone bool, found bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.skl.get(key)
	if !ok {
		return nil, false, false
	}
	return e.value, e.tombstone, true
}

// Size returns the approximate bytes of keys and values held.
func (m *Memtable) Size() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.size
}

// Iterator walks entries in key order.
type Iterator struct {
	cur *node
}

// NewIterator returns an Iterator before the first entry; call Next to start.
// Only use it on a frozen memtable: it reads without holding the lock.
func (m *Memtable) NewIterator() *Iterator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return &Iterator{cur: &node{forward: []*node{m.skl.front()}}}
}

// Next moves to the next entry and reports whether there is one.
func (it *Iterator) Next() bool {
	if it.cur == nil || it.cur.forward[0] == nil {
		return false
	}
	it.cur = it.cur.forward[0]
	return true
}

// Key returns the current entry's key.
func (it *Iterator) Key() []byte { return it.cur.entry.key }

// Value returns the current entry's value.
func (it *Iterator) Value() []byte { return it.cur.entry.value }

// Tombstone reports whether the current entry is a delete.
func (it *Iterator) Tombstone() bool { return it.cur.entry.tombstone }

// Seq returns the current entry's sequence number.
func (it *Iterator) Seq() uint64 { return it.cur.entry.seq }

func clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

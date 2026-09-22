package memtable

import (
	"bytes"
	"math/rand/v2"
)

const (
	maxLevel    = 16
	probability = 0.25
)

// entry is a single key's latest state in the skip list.
type entry struct {
	key       []byte
	value     []byte
	tombstone bool
	seq       uint64
}

// node is a skip list node. forward[i] is the next node at level i.
type node struct {
	entry   entry
	forward []*node
}

// skiplist is a sorted, singly-linked skip list keyed by byte-slice key
// order (bytes.Compare). It is not safe for concurrent use on its own; the
// Memtable wrapper provides synchronization.
type skiplist struct {
	head  *node // sentinel with no entry, forward[i] points into the list
	level int   // highest level currently in use, >= 1
}

func newSkiplist() *skiplist {
	return &skiplist{
		head:  &node{forward: make([]*node, maxLevel)},
		level: 1,
	}
}

func randomLevel() int {
	level := 1
	for level < maxLevel && rand.Float64() < probability {
		level++
	}
	return level
}

// search fills update with, at each level, the last node whose key is
// strictly less than key, and returns the node at level 0 that would come
// right after them (the candidate for an exact match).
func (s *skiplist) search(key []byte, update []*node) *node {
	cur := s.head
	for i := s.level - 1; i >= 0; i-- {
		for cur.forward[i] != nil && bytes.Compare(cur.forward[i].entry.key, key) < 0 {
			cur = cur.forward[i]
		}
		update[i] = cur
	}
	return cur.forward[0]
}

// upsert inserts a new entry or overwrites the existing one for e.key. It
// returns the previous entry and true if one existed.
func (s *skiplist) upsert(e entry) (entry, bool) {
	updatePtrs := make([]*node, maxLevel)
	next := s.search(e.key, updatePtrs)
	if next != nil && bytes.Equal(next.entry.key, e.key) {
		old := next.entry
		next.entry = e
		return old, true
	}

	lvl := randomLevel()
	if lvl > s.level {
		for i := s.level; i < lvl; i++ {
			updatePtrs[i] = s.head
		}
		s.level = lvl
	}

	n := &node{entry: e, forward: make([]*node, lvl)}
	for i := 0; i < lvl; i++ {
		n.forward[i] = updatePtrs[i].forward[i]
		updatePtrs[i].forward[i] = n
	}
	return entry{}, false
}

// get returns the entry for key, if present.
func (s *skiplist) get(key []byte) (entry, bool) {
	cur := s.head
	for i := s.level - 1; i >= 0; i-- {
		for cur.forward[i] != nil && bytes.Compare(cur.forward[i].entry.key, key) < 0 {
			cur = cur.forward[i]
		}
	}
	cur = cur.forward[0]
	if cur != nil && bytes.Equal(cur.entry.key, key) {
		return cur.entry, true
	}
	return entry{}, false
}

// front returns the first node in sorted order, or nil if empty.
func (s *skiplist) front() *node {
	return s.head.forward[0]
}

package memtable

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

func TestPutGet(t *testing.T) {
	m := NewMemtable()
	m.Put(1, []byte("k1"), []byte("v1"))
	m.Put(2, []byte("k2"), []byte("v2"))

	v, tomb, found := m.Get([]byte("k1"))
	if !found || tomb || !bytes.Equal(v, []byte("v1")) {
		t.Fatalf("Get(k1) = %q, %v, %v; want v1, false, true", v, tomb, found)
	}

	_, _, found = m.Get([]byte("missing"))
	if found {
		t.Fatalf("Get(missing) found = true, want false")
	}
}

func TestOverwrite(t *testing.T) {
	m := NewMemtable()
	m.Put(1, []byte("k"), []byte("first"))
	m.Put(2, []byte("k"), []byte("second"))

	v, tomb, found := m.Get([]byte("k"))
	if !found || tomb || !bytes.Equal(v, []byte("second")) {
		t.Fatalf("Get(k) = %q, %v, %v; want second, false, true", v, tomb, found)
	}
}

func TestTombstone(t *testing.T) {
	m := NewMemtable()
	m.Put(1, []byte("k"), []byte("v"))
	m.Delete(2, []byte("k"))

	v, tomb, found := m.Get([]byte("k"))
	if !found || !tomb || len(v) != 0 {
		t.Fatalf("Get(k) after delete = %q, %v, %v; want empty, true, true", v, tomb, found)
	}

	// A delete of a never-seen key still records a tombstone.
	m.Delete(3, []byte("never-existed"))
	_, tomb, found = m.Get([]byte("never-existed"))
	if !found || !tomb {
		t.Fatalf("Get(never-existed) = tomb=%v found=%v; want true, true", tomb, found)
	}
}

func TestSortedIteration(t *testing.T) {
	m := NewMemtable()
	keys := []string{"delta", "alpha", "charlie", "echo", "bravo"}
	for i, k := range keys {
		m.Put(uint64(i+1), []byte(k), []byte("v-"+k))
	}

	it := m.NewIterator()
	var got []string
	for it.Next() {
		got = append(got, string(it.Key()))
	}

	want := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	if len(got) != len(want) {
		t.Fatalf("got %d keys, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("iteration order = %v, want %v", got, want)
		}
	}
}

func TestIterationSeesOverwritesAndTombstones(t *testing.T) {
	m := NewMemtable()
	m.Put(1, []byte("a"), []byte("old"))
	m.Put(2, []byte("a"), []byte("new"))
	m.Put(3, []byte("b"), []byte("b-val"))
	m.Delete(4, []byte("b"))
	m.Put(5, []byte("c"), []byte("c-val"))

	it := m.NewIterator()
	type kv struct {
		key   string
		value string
		tomb  bool
	}
	var got []kv
	for it.Next() {
		got = append(got, kv{string(it.Key()), string(it.Value()), it.Tombstone()})
	}

	want := []kv{
		{"a", "new", false},
		{"b", "", true},
		{"c", "c-val", false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSize(t *testing.T) {
	m := NewMemtable()
	if m.Size() != 0 {
		t.Fatalf("empty Size() = %d, want 0", m.Size())
	}

	m.Put(1, []byte("key"), []byte("value")) // 3 + 5 = 8
	if got, want := m.Size(), int64(8); got != want {
		t.Fatalf("Size() after one put = %d, want %d", got, want)
	}

	m.Put(2, []byte("key"), []byte("v")) // overwrite: 3 + 1 = 4
	if got, want := m.Size(), int64(4); got != want {
		t.Fatalf("Size() after overwrite = %d, want %d", got, want)
	}

	m.Delete(3, []byte("key")) // 3 + 0 = 3
	if got, want := m.Size(), int64(3); got != want {
		t.Fatalf("Size() after delete = %d, want %d", got, want)
	}
}

func TestConcurrentAccess(t *testing.T) {
	m := NewMemtable()
	const goroutines = 8
	const perGoroutine = 200

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				key := []byte(fmt.Sprintf("g%d-k%d", g, i))
				seq := uint64(g*perGoroutine + i)
				if i%10 == 0 {
					m.Put(seq, key, []byte("v"))
					m.Delete(seq+1, key)
				} else {
					m.Put(seq, key, []byte(fmt.Sprintf("v%d", i)))
				}
				_, _, _ = m.Get(key)
				_ = m.Size()
			}
		}(g)
	}
	wg.Wait()

	// Spot check a few keys landed with expected final state.
	v, tomb, found := m.Get([]byte("g0-k1"))
	if !found || tomb || !bytes.Equal(v, []byte("v1")) {
		t.Fatalf("Get(g0-k1) = %q, %v, %v; want v1, false, true", v, tomb, found)
	}
	_, tomb, found = m.Get([]byte("g0-k0"))
	if !found || !tomb {
		t.Fatalf("Get(g0-k0) = tomb=%v found=%v; want true, true", tomb, found)
	}
}

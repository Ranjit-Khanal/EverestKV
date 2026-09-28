package store

import (
	"testing"
	"time"
)

// newTestStore returns a Store whose clock only moves when advance is called.
func newTestStore() (s *Store, advance func(time.Duration)) {
	now := time.Unix(1_000_000, 0)
	s = NewStore()
	s.now = func() time.Time { return now }
	return s, func(d time.Duration) { now = now.Add(d) }
}

func TestStoreTTLExpiry(t *testing.T) {
	s, advance := newTestStore()
	s.Set("temp", "v", 10*time.Second)
	s.Set("perm", "v", 0)

	if got, ok := s.TTL("temp"); !ok || got != 10*time.Second {
		t.Fatalf("TTL(temp) = %v, %v; want 10s, true", got, ok)
	}
	if got, ok := s.TTL("perm"); !ok || got != 0 {
		t.Fatalf("TTL(perm) = %v, %v; want 0, true", got, ok)
	}

	advance(9 * time.Second)
	if v, ok := s.Get("temp"); !ok || v != "v" {
		t.Fatalf("Get(temp) before expiry = %q, %v", v, ok)
	}

	advance(time.Second)
	if _, ok := s.Get("temp"); ok {
		t.Fatalf("Get(temp) after expiry found the key")
	}
	if _, ok := s.TTL("temp"); ok {
		t.Fatalf("TTL(temp) after expiry found the key")
	}
	if keys := s.Keys(); len(keys) != 1 || keys[0] != "perm" {
		t.Fatalf("Keys = %q, want [perm]", keys)
	}
	if s.Delete("temp") {
		t.Fatalf("Delete(temp) after expiry reported it existed")
	}
}

func TestStoreSetReplacesTTL(t *testing.T) {
	s, advance := newTestStore()
	s.Set("k", "v1", time.Second)
	s.Set("k", "v2", 0) // clears the TTL
	advance(time.Hour)
	if v, ok := s.Get("k"); !ok || v != "v2" {
		t.Fatalf("Get(k) = %q, %v; want v2, true", v, ok)
	}

	s.Set("k", "v3", time.Second) // adds one back
	advance(time.Second)
	if _, ok := s.Get("k"); ok {
		t.Fatalf("Get(k) found the key after its new TTL")
	}
}

func TestStoreSweep(t *testing.T) {
	s, advance := newTestStore()
	s.Set("a", "v", time.Second)
	s.Set("b", "v", time.Minute)
	s.Set("c", "v", 0)

	advance(time.Second)
	s.sweep()
	s.mu.RLock()
	n, expiring, running := len(s.data), s.expiring, s.sweeper != nil
	s.mu.RUnlock()
	if n != 2 || expiring != 1 || !running {
		t.Fatalf("after first sweep: %d keys, %d expiring, sweeper=%v; want 2, 1, true", n, expiring, running)
	}

	advance(time.Minute)
	s.sweep()
	s.mu.RLock()
	n, expiring, running = len(s.data), s.expiring, s.sweeper != nil
	s.mu.RUnlock()
	if n != 1 || expiring != 0 || running {
		t.Fatalf("after second sweep: %d keys, %d expiring, sweeper=%v; want 1, 0, false", n, expiring, running)
	}
}

package store

import "sync"

// Store is a thread-safe in-memory string key-value map.
type Store struct {
	mu   sync.RWMutex
	data map[string]string
}

// New returns an empty Store.
func New() *Store {
	return &Store{data: make(map[string]string)}
}

// Get returns the value and true if the key exists.
func (s *Store) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

// Set stores value under key.
func (s *Store) Set(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
}

// Keys returns a snapshot of every key currently stored, in no
// particular order.
func (s *Store) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}

// Delete removes key and reports whether it existed.
func (s *Store) Delete(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.data[key]
	delete(s.data, key)
	return ok
}

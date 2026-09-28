package store

import (
	"sync"
	"time"
)

// sweepInterval controls how often expired keys are removed.
const sweepInterval = time.Second

type entry struct {
	value     string
	expiresAt time.Time // zero means the key never expires
}

func (e entry) expired(now time.Time) bool {
	return !e.expiresAt.IsZero() && !now.Before(e.expiresAt)
}

// Store is a thread-safe in-memory key-value map with optional TTLs.
type Store struct {
	mu       sync.RWMutex
	data     map[string]entry
	expiring int         // number of entries in data with a TTL
	sweeper  *time.Timer // pending sweep, nil when expiring == 0
	now      func() time.Time
}

// Construtor pattern in go
func NewStore() *Store {
	return &Store{data: make(map[string]entry), now: time.Now}
}

// Get returns the value if the key exists and has not expired.
func (s *Store) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.data[key]
	if !ok || e.expired(s.now()) {
		return "", false
	}
	return e.value, true
}

// Set stores value under key. A ttl of 0 means the key never expires.
func (s *Store) Set(key, value string, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e := entry{value: value}
	if ttl > 0 {
		e.expiresAt = s.now().Add(ttl)
	}
	s.put(key, e)
}

// TTL returns the time left before key expires, or 0 if it never expires.
func (s *Store) TTL(key string) (remaining time.Duration, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.now()
	e, ok := s.data[key]
	if !ok || e.expired(now) {
		return 0, false
	}
	if e.expiresAt.IsZero() {
		return 0, true
	}
	return e.expiresAt.Sub(now), true
}

// Keys returns every live key, in no particular order.
func (s *Store) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.now()
	keys := make([]string, 0, len(s.data))
	for k, e := range s.data {
		if !e.expired(now) {
			keys = append(keys, k)
		}
	}
	return keys
}

// Delete removes key and reports whether it existed.
func (s *Store) Delete(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[key]
	if !ok {
		return false
	}
	s.remove(key, e)
	return !e.expired(s.now())
}

// put stores e under key and starts the sweeper if needed. Caller holds s.mu.
func (s *Store) put(key string, e entry) {
	if old, ok := s.data[key]; ok && !old.expiresAt.IsZero() {
		s.expiring--
	}
	s.data[key] = e
	if !e.expiresAt.IsZero() {
		s.expiring++
		if s.sweeper == nil {
			s.sweeper = time.AfterFunc(sweepInterval, s.sweep)
		}
	}
}

// remove deletes key and updates the expiring count. Caller holds s.mu.
func (s *Store) remove(key string, e entry) {
	delete(s.data, key)
	if !e.expiresAt.IsZero() {
		s.expiring--
	}
}

// sweep removes expired keys and stops once no key has a TTL.
func (s *Store) sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, e := range s.data {
		if e.expired(now) {
			s.remove(k, e)
		}
	}
	if s.expiring > 0 {
		s.sweeper.Reset(sweepInterval)
	} else {
		s.sweeper = nil
	}
}

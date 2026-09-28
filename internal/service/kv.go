// Package service holds the key-value business rules between the HTTP
// handlers and the store. It knows nothing about HTTP.
package service

import (
	"errors"
	"sort"
	"time"
)

var (
	ErrNotFound   = errors.New("key not found")
	ErrEmptyKey   = errors.New("empty key")
	ErrInvalidTTL = errors.New("ttl must not be negative")
)

// Store is the storage KV depends on. *store.Store implements it.
type Store interface {
	Get(key string) (string, bool)
	Set(key, value string, ttl time.Duration)
	Delete(key string) bool
	TTL(key string) (time.Duration, bool)
	Keys() []string
}

// KV is the key-value service.
type KV struct {
	store Store
}

// NewKV returns a KV service backed by st.
func NewKV(st Store) *KV {
	return &KV{store: st}
}

// Get returns the value for key, or ErrNotFound.
func (s *KV) Get(key string) (string, error) {
	if key == "" {
		return "", ErrEmptyKey
	}
	v, ok := s.store.Get(key)
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Set stores value under key. A ttl of 0 means the key never expires.
func (s *KV) Set(key, value string, ttl time.Duration) error {
	if key == "" {
		return ErrEmptyKey
	}
	if ttl < 0 {
		return ErrInvalidTTL
	}
	s.store.Set(key, value, ttl)
	return nil
}

// Delete removes key, or returns ErrNotFound.
func (s *KV) Delete(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	if !s.store.Delete(key) {
		return ErrNotFound
	}
	return nil
}

// TTL returns the time left before key expires, or 0 if it never expires.
func (s *KV) TTL(key string) (time.Duration, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	remaining, ok := s.store.TTL(key)
	if !ok {
		return 0, ErrNotFound
	}
	return remaining, nil
}

// Keys returns every live key, sorted.
func (s *KV) Keys() []string {
	keys := s.store.Keys()
	sort.Strings(keys)
	return keys
}

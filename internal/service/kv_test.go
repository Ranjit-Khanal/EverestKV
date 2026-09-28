package service

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Ranjit-Khanal/everestkv/internal/store"
)

func TestKV(t *testing.T) {
	s := NewKV(store.NewStore())

	if err := s.Set("b", "1", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set("a", "2", time.Minute); err != nil {
		t.Fatalf("Set with TTL: %v", err)
	}
	if v, err := s.Get("b"); err != nil || v != "1" {
		t.Fatalf("Get(b) = %q, %v", v, err)
	}
	if got := s.Keys(); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("Keys = %q, want [a b]", got)
	}
	if d, err := s.TTL("a"); err != nil || d <= 0 || d > time.Minute {
		t.Fatalf("TTL(a) = %v, %v", d, err)
	}
	if d, err := s.TTL("b"); err != nil || d != 0 {
		t.Fatalf("TTL(b) = %v, %v; want 0", d, err)
	}
	if err := s.Delete("b"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	for name, err := range map[string]error{
		"Get missing":    second(s.Get("b")),
		"Delete missing": s.Delete("b"),
		"TTL missing":    second(s.TTL("b")),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s = %v, want ErrNotFound", name, err)
		}
	}
	for name, err := range map[string]error{
		"Get":    second(s.Get("")),
		"Set":    s.Set("", "v", 0),
		"Delete": s.Delete(""),
		"TTL":    second(s.TTL("")),
	} {
		if !errors.Is(err, ErrEmptyKey) {
			t.Errorf("%s empty key = %v, want ErrEmptyKey", name, err)
		}
	}
	if err := s.Set("k", "v", -time.Second); !errors.Is(err, ErrInvalidTTL) {
		t.Errorf("Set negative ttl = %v, want ErrInvalidTTL", err)
	}
}

func second[T any](_ T, err error) error { return err }

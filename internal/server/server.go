// Package server runs the EverestKV HTTP server. The API itself lives in
// internal/http/handlers.
package server

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/Ranjit-Khanal/everestkv/internal/http/handlers"
	"github.com/Ranjit-Khanal/everestkv/internal/service"
	"github.com/Ranjit-Khanal/everestkv/internal/store"
)

// ErrServerClosed is returned by Serve and ListenAndServe after Shutdown.
var ErrServerClosed = http.ErrServerClosed

// Config holds server listen options.
type Config struct {
	Addr string
}

// DefaultConfig listens on port 8379.
func DefaultConfig() Config {
	return Config{Addr: ":8379"}
}

// Server is an HTTP server exposing the key-value store.
type Server struct {
	cfg   Config
	store *store.Store
	http  *http.Server
}

// New returns a Server with the given config and an empty in-memory store.
func New(cfg Config) *Server {
	s := &Server{cfg: cfg, store: store.NewStore()}
	s.http = &http.Server{
		Handler:           handlers.New(service.NewKV(s.store)).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// ListenAndServe listens on cfg.Addr and calls Serve.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// Serve accepts connections on ln until an error occurs or Shutdown is
// called, in which case it returns ErrServerClosed. Serve always closes
// ln.
func (s *Server) Serve(ln net.Listener) error {
	log.Printf("everestkv listening on %s", ln.Addr())
	return s.http.Serve(ln)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.http.Shutdown(ctx)
	if err != nil {
		s.http.Close()
	}
	return err
}

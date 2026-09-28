// Package server runs the EverestKV HTTP server around a given handler.
package server

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"
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

// Server is an HTTP server that serves a handler.
type Server struct {
	cfg  Config
	http *http.Server
}

// NewServer returns a Server that serves h.
func NewServer(cfg Config, h http.Handler) *Server {
	return &Server{
		cfg: cfg,
		http: &http.Server{
			Handler:           h,
			ReadHeaderTimeout: 10 * time.Second,
		},
	}
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

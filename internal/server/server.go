package server

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"
)

var ErrServerClosed = http.ErrServerClosed

type Config struct {
	Addr string
}

func DefaultConfig() Config {
	return Config{Addr: ":8379"}
}

type Server struct {
	cfg  Config
	http *http.Server
}

func NewServer(cfg Config, h http.Handler) *Server {
	return &Server{
		cfg: cfg,
		http: &http.Server{
			Handler:           h,
			ReadHeaderTimeout: 10 * time.Second,
		},
	}
}

func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

func (s *Server) Serve(ln net.Listener) error {
	log.Printf("everestkv listening on %s", ln.Addr())
	return s.http.Serve(ln)
}

func (s *Server) Shutdown(ctx context.Context) error {
	err := s.http.Shutdown(ctx)
	if err != nil {
		s.http.Close()
	}
	return err
}

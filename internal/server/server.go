package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"

	"github.com/Ranjit-Khanal/everestkv/internal/command"
	"github.com/Ranjit-Khanal/everestkv/internal/store"
	"github.com/Ranjit-Khanal/everestkv/pkg/resp"
)

// Config holds server listen options.
type Config struct {
	Addr string
}

// DefaultConfig listens on the default Redis port.
func DefaultConfig() Config {
	return Config{Addr: ":6379"}
}

// Server is a TCP server that speaks RESP2.
type Server struct {
	cfg   Config
	store *store.Store
}

// New returns a Server with the given config and an empty in-memory store.
func New(cfg Config) *Server {
	return &Server{
		cfg:   cfg,
		store: store.New(),
	}
}

// ListenAndServe accepts connections until an error occurs.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	defer ln.Close()

	log.Printf("everestkv listening on %s", s.cfg.Addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.serveConn(conn)
	}
}

func (s *Server) serveConn(conn net.Conn) {
	defer conn.Close()
	log.Printf("client connected: %s", conn.RemoteAddr())

	parser := resp.NewParser(conn)
	writer := resp.NewWriter(conn)

	for {
		v, err := parser.Parse()
		if err != nil {
			if err != io.EOF {
				log.Printf("parse error from %s: %v", conn.RemoteAddr(), err)
			}
			return
		}

		if err := s.dispatch(writer, v); err != nil {
			if !errors.Is(err, command.ErrQuit) {
				log.Printf("dispatch error from %s: %v", conn.RemoteAddr(), err)
			}
			return
		}
	}
}

func (s *Server) dispatch(w *resp.Writer, v resp.Value) error {
	cmd, args, err := v.Command()
	if err != nil {
		return w.WriteError(fmt.Sprintf("ERR %v", err))
	}
	return command.Dispatch(s.store, cmd, args, w)
}

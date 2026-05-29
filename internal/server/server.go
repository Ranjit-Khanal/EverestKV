package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"

	"github.com/Ranjit-Khanal/everestkv/pkg/resp"
)

var errClose = errors.New("connection closed")

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
	cfg Config
}

// New returns a Server with the given config.
func New(cfg Config) *Server {
	return &Server{cfg: cfg}
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
			if !errors.Is(err, errClose) {
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

	switch strings.ToUpper(cmd) {
	case "PING":
		return w.WriteSimpleString("PONG")
	case "ECHO":
		if len(args) != 1 {
			return w.WriteError("ERR wrong number of arguments for 'echo' command")
		}
		return w.WriteBulkString(args[0])
	case "QUIT":
		if err := w.WriteSimpleString("OK"); err != nil {
			return err
		}
		return errClose
	default:
		return w.WriteError(fmt.Sprintf("ERR unknown command '%s'", strings.ToLower(cmd)))
	}
}

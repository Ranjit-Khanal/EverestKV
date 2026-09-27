// Package server owns the network side of EverestKV: it accepts TCP
// connections, runs one goroutine per connection, decodes RESP2 requests
// with pkg/resp, and hands each one to internal/command for execution. It
// contains no command logic of its own.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/Ranjit-Khanal/everestkv/internal/command"
	"github.com/Ranjit-Khanal/everestkv/internal/store"
	"github.com/Ranjit-Khanal/everestkv/pkg/resp"
)

// ErrServerClosed is returned by Serve and ListenAndServe after Shutdown.
var ErrServerClosed = errors.New("server: closed")

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

	// mu guards ln, conns and closing. conns holds every connection still
	// being served; wg counts their goroutines. New connections are only
	// added (and wg.Add called) while closing is false, so once Shutdown
	// sets it, wg can only go down.
	mu      sync.Mutex
	ln      net.Listener
	conns   map[net.Conn]struct{}
	closing bool
	wg      sync.WaitGroup
}

// New returns a Server with the given config and an empty in-memory store.
func New(cfg Config) *Server {
	return &Server{
		cfg:   cfg,
		store: store.New(),
		conns: make(map[net.Conn]struct{}),
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
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		ln.Close()
		return ErrServerClosed
	}
	s.ln = ln
	s.mu.Unlock()
	defer ln.Close()

	log.Printf("everestkv listening on %s", ln.Addr())

	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.isClosing() {
				return ErrServerClosed
			}
			return err
		}
		if !s.trackConn(conn) {
			conn.Close()
			return ErrServerClosed
		}
		go s.serveConn(conn)
	}
}

// Shutdown gracefully stops the server. It stops accepting connections,
// then lets each connection finish the command it is executing (and any
// complete commands it has already buffered) before closing it; clients
// waiting idle for their next command are disconnected right away. It
// returns nil once every connection is closed. If ctx ends first, it
// force-closes the remaining connections and returns ctx.Err().
//
// When Shutdown returns nil, no command is running, so it is safe to close
// the store.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closing = true
	if s.ln != nil {
		s.ln.Close()
	}
	// Unblock connections waiting in Parse. A connection mid-dispatch
	// finishes its command, then fails its next read and exits.
	for c := range s.conns {
		c.SetReadDeadline(time.Now())
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		for c := range s.conns {
			c.Close()
		}
		s.mu.Unlock()
		<-done
		return ctx.Err()
	}
}

func (s *Server) isClosing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closing
}

// trackConn registers conn for Shutdown. It returns false if the server is
// already shutting down, in which case conn must not be served.
func (s *Server) trackConn(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.conns[conn] = struct{}{}
	s.wg.Add(1)
	return true
}

func (s *Server) untrackConn(conn net.Conn) {
	s.mu.Lock()
	delete(s.conns, conn)
	s.mu.Unlock()
	s.wg.Done()
}

func (s *Server) serveConn(conn net.Conn) {
	defer s.untrackConn(conn)
	defer conn.Close()
	log.Printf("client connected: %s", conn.RemoteAddr())

	parser := resp.NewParser(conn)
	writer := resp.NewWriter(conn)

	for {
		v, err := parser.Parse()
		if err != nil {
			shutdownRead := s.isClosing() && errors.Is(err, os.ErrDeadlineExceeded)
			if err != io.EOF && !shutdownRead {
				log.Printf("parse error from %s: %v", conn.RemoteAddr(), err)
			}
			return
		}

		if err := s.dispatch(writer, v); err != nil {
			if !errors.Is(err, command.ErrQuit) && !s.isClosing() {
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

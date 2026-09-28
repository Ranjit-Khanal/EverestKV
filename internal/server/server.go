// Package server owns the network side of EverestKV: it serves a small
// HTTP/JSON API over the store. It contains no storage logic of its own.
//
// Routes:
//
//	GET    /v1/ping        200 "PONG"
//	GET    /v1/keys        200 {"keys": [...]}, sorted
//	GET    /v1/kv/{key}    200 raw value, or 404
//	PUT    /v1/kv/{key}    204; the request body is the value
//	DELETE /v1/kv/{key}    204, or 404 if the key did not exist
//
// Keys are the rest of the path after /v1/kv/, percent-decoded, so they
// may contain any byte (including "/") as long as the client escapes it.
// Values are raw bytes, not JSON. Errors are {"error": "..."}.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Ranjit-Khanal/everestkv/internal/store"
)

// ErrServerClosed is returned by Serve and ListenAndServe after Shutdown.
var ErrServerClosed = http.ErrServerClosed

// MaxValueBytes caps the size of a PUT body.
const MaxValueBytes = 32 << 20

const kvPrefix = "/v1/kv/"

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
	s := &Server{cfg: cfg, store: store.New()}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/ping", s.handlePing)
	mux.HandleFunc("GET /v1/keys", s.handleKeys)

	s.http = &http.Server{
		Handler:           s.routeKV(mux),
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

// Shutdown gracefully stops the server. It stops accepting connections,
// closes idle keep-alive connections right away, and waits for in-flight
// requests to finish. It returns nil once every connection is closed. If
// ctx ends first, it force-closes the remaining connections and returns
// ctx.Err(); their handlers may still be finishing when it returns.
//
// When Shutdown returns nil, no request is running, so it is safe to
// close the store.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.http.Shutdown(ctx)
	if err != nil {
		s.http.Close()
	}
	return err
}

// routeKV sends /v1/kv/ requests to handleKV and everything else to next.
// It bypasses ServeMux for keys because ServeMux cleans the decoded path
// and redirects, which would silently rewrite keys like "a//b" or "x/../y".
func (s *Server) routeKV(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escaped, ok := strings.CutPrefix(r.URL.EscapedPath(), kvPrefix)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		key, err := url.PathUnescape(escaped)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid key escaping")
			return
		}
		if key == "" {
			writeError(w, http.StatusBadRequest, "empty key")
			return
		}
		s.handleKV(w, r, key)
	})
}

func (s *Server) handleKV(w http.ResponseWriter, r *http.Request, key string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		val, ok := s.store.Get(key)
		if !ok {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		io.WriteString(w, val)

	case http.MethodPut:
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxValueBytes))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				writeError(w, http.StatusRequestEntityTooLarge, "value too large")
				return
			}
			writeError(w, http.StatusBadRequest, "reading body: "+err.Error())
			return
		}
		s.store.Set(key, string(body))
		w.WriteHeader(http.StatusNoContent)

	case http.MethodDelete:
		if !s.store.Delete(key) {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		w.Header().Set("Allow", "GET, HEAD, PUT, DELETE")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handlePing(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, "PONG")
}

func (s *Server) handleKeys(w http.ResponseWriter, _ *http.Request) {
	keys := s.store.Keys()
	sort.Strings(keys)
	writeJSON(w, http.StatusOK, map[string][]string{"keys": keys})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

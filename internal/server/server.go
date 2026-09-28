// Package server owns the network side of EverestKV: it serves a small
// HTTP/JSON API over the store. It contains no storage logic of its own.
//
// Routes:
//
//	GET    /v1/ping        200 "PONG"
//	GET    /v1/keys        200 {"keys": [...]}, sorted
//	GET    /v1/kv/{key}    200 raw value, or 404
//	PUT    /v1/kv/{key}    204; the body is the value, ?ttl=N sets a TTL in seconds
//	DELETE /v1/kv/{key}    204, or 404 if the key did not exist
//	GET    /v1/ttl/{key}   200 {"ttl": N}, -1 if no expiry, or 404
//
// Keys are the rest of the path after the prefix, percent-decoded, so they
// may contain any byte (including "/") as long as the client escapes it.
// Values are raw bytes, not JSON. Errors are {"error": "..."}.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Ranjit-Khanal/everestkv/internal/store"
)

// ErrServerClosed is returned by Serve and ListenAndServe after Shutdown.
var ErrServerClosed = http.ErrServerClosed

// MaxValueBytes caps the size of a PUT body.
const MaxValueBytes = 32 << 20

const (
	kvPrefix  = "/v1/kv/"
	ttlPrefix = "/v1/ttl/"
)

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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/ping", s.handlePing)
	mux.HandleFunc("GET /v1/keys", s.handleKeys)

	s.http = &http.Server{
		Handler:           s.routeKeys(mux),
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

// routeKeys routes /v1/kv/ and /v1/ttl/ itself, since ServeMux would rewrite keys like "a//b".
func (s *Server) routeKeys(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.EscapedPath()
		handle := s.handleKV
		escaped, ok := strings.CutPrefix(path, kvPrefix)
		if !ok {
			handle = s.handleTTL
			escaped, ok = strings.CutPrefix(path, ttlPrefix)
		}
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
		handle(w, r, key)
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
		var ttl time.Duration
		if q := r.URL.Query().Get("ttl"); q != "" {
			secs, err := strconv.ParseInt(q, 10, 64)
			if err != nil || secs <= 0 || secs > math.MaxInt64/int64(time.Second) {
				writeError(w, http.StatusBadRequest, "ttl must be a positive number of seconds")
				return
			}
			ttl = time.Duration(secs) * time.Second
		}
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
		s.store.Set(key, string(body), ttl)
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

func (s *Server) handleTTL(w http.ResponseWriter, r *http.Request, key string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	remaining, ok := s.store.TTL(key)
	if !ok {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	secs := int64(-1)
	if remaining > 0 {
		// Round up so a live key never reports 0.
		secs = int64((remaining + time.Second - 1) / time.Second)
	}
	writeJSON(w, http.StatusOK, map[string]int64{"ttl": secs})
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

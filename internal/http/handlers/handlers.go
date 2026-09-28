// Package handlers serves the HTTP/JSON API over the store.
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
package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Ranjit-Khanal/everestkv/internal/service"
)

// MaxValueBytes caps the size of a PUT body.
const MaxValueBytes = 32 << 20

const (
	kvPrefix  = "/v1/kv/"
	ttlPrefix = "/v1/ttl/"
)

// KVService is the service Handlers depends on. *service.KV implements it.
type KVService interface {
	Get(key string) (string, error)
	Set(key, value string, ttl time.Duration) error
	Delete(key string) error
	TTL(key string) (time.Duration, error)
	Keys() []string
}

// Handlers serves API requests through a KVService.
type Handlers struct {
	kv KVService
}

// NewHandlers returns Handlers backed by kv.
func NewHandlers(kv KVService) *Handlers {
	return &Handlers{kv: kv}
}

// Routes returns an http.Handler for every API route.
func (h *Handlers) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/ping", h.Ping)
	mux.HandleFunc("GET /v1/keys", h.Keys)
	return h.routeKeys(mux)
}

// routeKeys routes /v1/kv/ and /v1/ttl/ itself, since ServeMux would rewrite keys like "a//b".
func (h *Handlers) routeKeys(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.EscapedPath()
		handle := h.KV
		escaped, ok := strings.CutPrefix(path, kvPrefix)
		if !ok {
			handle = h.TTL
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
		handle(w, r, key)
	})
}

// KV handles GET, HEAD, PUT and DELETE on a single key.
func (h *Handlers) KV(w http.ResponseWriter, r *http.Request, key string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		val, err := h.kv.Get(key)
		if err != nil {
			writeServiceError(w, err)
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
		if err := h.kv.Set(key, string(body), ttl); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case http.MethodDelete:
		if err := h.kv.Delete(key); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		w.Header().Set("Allow", "GET, HEAD, PUT, DELETE")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// TTL reports the seconds left before a key expires.
func (h *Handlers) TTL(w http.ResponseWriter, r *http.Request, key string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	remaining, err := h.kv.TTL(key)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	secs := int64(-1)
	if remaining > 0 {
		// Round up so a live key never reports 0.
		secs = int64((remaining + time.Second - 1) / time.Second)
	}
	writeJSON(w, http.StatusOK, map[string]int64{"ttl": secs})
}

// Ping answers PONG.
func (h *Handlers) Ping(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, "PONG")
}

// Keys lists every key, sorted.
func (h *Handlers) Keys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]string{"keys": h.kv.Keys()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeServiceError maps a service error to its HTTP status.
func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, service.ErrEmptyKey), errors.Is(err, service.ErrInvalidTTL):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

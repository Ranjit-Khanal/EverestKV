// Command everestkv-web serves a browser dashboard for EverestKV. It holds no
// storage logic of its own: every operation the UI offers is issued
// against the server's HTTP API, through the same internal/client
// package the CLI uses.
//
// Usage:
//
//	everestkv-web [-addr :8080] [-server localhost:8379]
package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/Ranjit-Khanal/everestkv/internal/client"
)

//go:embed static
var staticFS embed.FS

func main() {
	httpAddr := flag.String("addr", ":8080", "HTTP listen address for the dashboard")
	serverAddr := flag.String("server", "localhost:8379", "EverestKV server address")
	flag.Parse()

	c := client.NewClient(*serverAddr)
	if _, err := c.Ping(); err != nil {
		log.Fatalf("connecting to EverestKV at %s: %v", *serverAddr, err)
	}

	api := &api{client: c, serverAddr: *serverAddr}

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(static)))
	mux.HandleFunc("/api/status", api.handleStatus)
	mux.HandleFunc("/api/get", api.handleGet)
	mux.HandleFunc("/api/set", api.handleSet)
	mux.HandleFunc("/api/keys", api.handleKeys)
	mux.HandleFunc("/api/command", api.handleCommand)

	log.Printf("everestkv dashboard on http://localhost%s (backing store: %s)", *httpAddr, *serverAddr)
	if err := http.ListenAndServe(*httpAddr, mux); err != nil {
		log.Fatal(err)
	}
}

// api holds the shared client used to talk to the EverestKV server.
type api struct {
	client     *client.Client
	serverAddr string
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (a *api) handleStatus(w http.ResponseWriter, r *http.Request) {
	reply, err := a.client.Ping()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"connected": false,
			"server":    a.serverAddr,
			"error":     err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"connected": true,
		"server":    a.serverAddr,
		"reply":     reply,
	})
}

func (a *api) handleGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "missing 'key' query parameter")
		return
	}
	value, found, err := a.client.Get(key)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"key":   key,
		"value": value,
		"found": found,
	})
}

// entry is one key/value pair as shown in the "Stored Keys" panel.
type entry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// handleKeys lists every key currently stored, with its value, for
// the dashboard's keys panel. It lists the keys followed by a GET per
// key — no different from doing the same from the CLI, just batched
// server-side.
func (a *api) handleKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	ks, err := a.client.Keys()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	sort.Strings(ks)

	entries := make([]entry, 0, len(ks))
	for _, k := range ks {
		value, found, err := a.client.Get(k)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if !found {
			// Deleted between the KEYS snapshot and this GET; skip it.
			continue
		}
		entries = append(entries, entry{Key: k, Value: value})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

type setRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (a *api) handleSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req setRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Key == "" {
		writeError(w, http.StatusBadRequest, "'key' is required")
		return
	}
	if err := a.client.Set(req.Key, req.Value, 0); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type commandRequest struct {
	Line string `json:"line"`
}

// handleCommand runs an arbitrary command line, the same way the CLI
// prompt does, so the dashboard console has full parity with the CLI.
func (a *api) handleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req commandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if fields := strings.Fields(req.Line); len(fields) > 0 {
		switch strings.ToUpper(fields[0]) {
		case "EXIT", "QUIT":
			// These only leave the CLI prompt; there is nothing to leave here.
			writeError(w, http.StatusBadRequest, "EXIT/QUIT is disabled in the dashboard console")
			return
		}
	}
	reply, err := a.client.Execute(req.Line)
	if err != nil {
		if client.IsTransportError(err) {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"reply": commandErrorText(err), "isError": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reply": reply, "isError": false})
}

// commandErrorText shows server errors by their message alone, like the
// CLI's "ERR ..." lines, rather than with the HTTP status appended.
func commandErrorText(err error) string {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		return "ERR " + apiErr.Message
	}
	return err.Error()
}

package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Ranjit-Khanal/everestkv/internal/client"
)

// startServer serves on a random local port and returns the server, its
// address, and a channel that receives Serve's return value.
func startServer(t *testing.T) (*Server, string, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := New(Config{})
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	})
	return srv, ln.Addr().String(), errc
}

func waitServe(t *testing.T, errc <-chan error) {
	t.Helper()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrServerClosed) {
			t.Fatalf("Serve returned %v, want ErrServerClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Serve did not return after Shutdown")
	}
}

func TestKVRoundTrip(t *testing.T) {
	_, addr, _ := startServer(t)
	c := client.New(addr)

	if got, err := c.Ping(); err != nil || got != "PONG" {
		t.Fatalf("Ping = %q, %v", got, err)
	}

	// Keys that ServeMux would clean or split, and a binary value.
	pairs := map[string]string{
		"plain":      "v",
		"with space": "hello world",
		"a/b":        "slash",
		"a//b":       "double slash",
		"x/../y":     "dotdot",
		"q?x=1#frag": "query chars",
		"नमस्ते":     "unicode",
		"bin":        "\x00\xff\r\n",
	}
	for k, v := range pairs {
		if err := c.Set(k, v, 0); err != nil {
			t.Fatalf("Set(%q): %v", k, err)
		}
	}
	for k, want := range pairs {
		got, found, err := c.Get(k)
		if err != nil || !found || got != want {
			t.Fatalf("Get(%q) = %q, %v, %v; want %q", k, got, found, err, want)
		}
	}

	keys, err := c.Keys()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != len(pairs) || !slices.IsSorted(keys) {
		t.Fatalf("Keys = %q, want %d sorted keys", keys, len(pairs))
	}

	if existed, err := c.Delete("a/b"); err != nil || !existed {
		t.Fatalf("Delete existing = %v, %v", existed, err)
	}
	if existed, err := c.Delete("a/b"); err != nil || existed {
		t.Fatalf("Delete missing = %v, %v", existed, err)
	}
	if _, found, err := c.Get("a/b"); err != nil || found {
		t.Fatalf("Get after Delete: found=%v err=%v", found, err)
	}
}

func TestTTL(t *testing.T) {
	_, addr, _ := startServer(t)
	c := client.New(addr)

	if err := c.Set("temp", "v", 2*time.Second); err != nil {
		t.Fatalf("Set with TTL: %v", err)
	}
	if err := c.Set("perm", "v", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	for _, tt := range []struct{ line, want string }{
		{"TTL temp", "2"},
		{"TTL perm", "-1"},
		{"TTL missing", "-2"},
		{"SET a/b v EX 100", "OK"},
		{"TTL a/b", "100"},
	} {
		if got, err := c.Execute(tt.line); err != nil || got != tt.want {
			t.Fatalf("Execute(%q) = %q, %v; want %q", tt.line, got, err, tt.want)
		}
	}
	for _, line := range []string{"SET k v EX 0", "SET k v EX x", "SET k v PX 1", "SET k v EX"} {
		var cmdErr *client.CommandError
		if _, err := c.Execute(line); !errors.As(err, &cmdErr) {
			t.Fatalf("Execute(%q) err = %v, want *CommandError", line, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, found, err := c.Get("temp")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("temp did not expire")
		}
		time.Sleep(50 * time.Millisecond)
	}
	keys, err := c.Keys()
	if err != nil || !slices.Equal(keys, []string{"a/b", "perm"}) {
		t.Fatalf("Keys = %q, %v; want [a/b perm]", keys, err)
	}
}

func TestBadRequests(t *testing.T) {
	_, addr, _ := startServer(t)
	base := "http://" + addr

	tests := []struct {
		method, path string
		body         io.Reader
		want         int
	}{
		{http.MethodGet, "/v1/kv/", nil, http.StatusBadRequest},
		{http.MethodGet, "/v1/kv/%zz", nil, http.StatusBadRequest},
		{http.MethodPost, "/v1/kv/k", nil, http.StatusMethodNotAllowed},
		{http.MethodPut, "/v1/kv/big", strings.NewReader(strings.Repeat("x", MaxValueBytes+1)), http.StatusRequestEntityTooLarge},
		{http.MethodPut, "/v1/kv/k?ttl=0", nil, http.StatusBadRequest},
		{http.MethodPut, "/v1/kv/k?ttl=-5", nil, http.StatusBadRequest},
		{http.MethodPut, "/v1/kv/k?ttl=1.5", nil, http.StatusBadRequest},
		{http.MethodPut, "/v1/kv/k?ttl=99999999999999", nil, http.StatusBadRequest},
		{http.MethodDelete, "/v1/ttl/k", nil, http.StatusMethodNotAllowed},
		{http.MethodGet, "/v1/ttl/", nil, http.StatusBadRequest},
		{http.MethodPost, "/v1/keys", nil, http.StatusMethodNotAllowed},
		{http.MethodGet, "/nope", nil, http.StatusNotFound},
	}
	for _, tt := range tests {
		req, err := http.NewRequest(tt.method, base, tt.body)
		if err != nil {
			t.Fatal(err)
		}
		req.URL.Opaque = "//" + addr + tt.path // send the path verbatim, even when malformed
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tt.method, tt.path, err)
		}
		res.Body.Close()
		if res.StatusCode != tt.want {
			t.Errorf("%s %s = %d, want %d", tt.method, tt.path, res.StatusCode, tt.want)
		}
	}
}

func TestExecute(t *testing.T) {
	_, addr, _ := startServer(t)
	c := client.New(addr)

	steps := []struct{ line, want string }{
		{"PING", "PONG"},
		{"keys *", "(empty array)"},
		{"SET city Kathmandu", "OK"},
		{"get city", "Kathmandu"},
		{"GET missing", "(nil)"},
		{"KEYS *", "city"},
		{"DEL city", "1"},
		{"DEL city", "0"},
	}
	for _, s := range steps {
		got, err := c.Execute(s.line)
		if err != nil || got != s.want {
			t.Fatalf("Execute(%q) = %q, %v; want %q", s.line, got, err, s.want)
		}
	}

	for _, line := range []string{"", "FLY away", "GET", "SET k", "KEYS a*"} {
		var cmdErr *client.CommandError
		if _, err := c.Execute(line); !errors.As(err, &cmdErr) {
			t.Errorf("Execute(%q) err = %v, want *CommandError", line, err)
		}
	}
}

func TestShutdownClosesIdleClients(t *testing.T) {
	srv, addr, errc := startServer(t)
	c := client.New(addr)
	if err := c.Set("k", "v", 0); err != nil { // leaves an idle keep-alive connection
		t.Fatalf("Set: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Shutdown with only idle clients took %s", d)
	}
	waitServe(t, errc)

	if _, err := c.Ping(); err == nil {
		t.Fatalf("server still answering after Shutdown")
	}
}

func TestShutdownWaitsForInFlightRequest(t *testing.T) {
	srv, addr, errc := startServer(t)

	// A PUT whose body is still being sent keeps its request in flight.
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPut, "http://"+addr+"/v1/kv/slow", pr)
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			res.Body.Close()
			if res.StatusCode != http.StatusNoContent {
				err = errors.New(res.Status)
			}
		}
		done <- err
	}()
	pw.Write([]byte("part1-"))
	time.Sleep(100 * time.Millisecond) // let the request reach the handler

	shut := make(chan error, 1)
	go func() { shut <- srv.Shutdown(context.Background()) }()
	select {
	case err := <-shut:
		t.Fatalf("Shutdown returned %v while a request was in flight", err)
	case <-time.After(100 * time.Millisecond):
	}

	pw.Write([]byte("part2"))
	pw.Close()
	if err := <-done; err != nil {
		t.Fatalf("in-flight PUT: %v", err)
	}
	if err := <-shut; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	waitServe(t, errc)
	if v, _ := srv.store.Get("slow"); v != "part1-part2" {
		t.Fatalf("stored %q, want full body", v)
	}
}

func TestShutdownTimeoutForceCloses(t *testing.T) {
	srv, addr, errc := startServer(t)

	// Start a PUT and never finish its body.
	pr, pw := io.Pipe()
	defer pw.Close()
	go func() {
		req, _ := http.NewRequest(http.MethodPut, "http://"+addr+"/v1/kv/stuck", pr)
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
		}
	}()
	pw.Write([]byte("partial"))
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := srv.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want context.DeadlineExceeded", err)
	}
	waitServe(t, errc)
}

func TestServeAfterShutdown(t *testing.T) {
	srv := New(Config{})
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if err := srv.Serve(ln); !errors.Is(err, ErrServerClosed) {
		t.Fatalf("Serve after Shutdown = %v, want ErrServerClosed", err)
	}
	if _, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second); err == nil {
		t.Fatalf("listener not closed by Serve after Shutdown")
	}
}

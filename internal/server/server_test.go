package server

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Ranjit-Khanal/everestkv/internal/client"
	"github.com/Ranjit-Khanal/everestkv/pkg/resp"
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

func dial(t *testing.T, addr string) *client.Client {
	t.Helper()
	c, err := client.Dial(addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
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

func TestShutdownDisconnectsIdleClients(t *testing.T) {
	srv, addr, errc := startServer(t)
	c := dial(t, addr)
	if err := c.Set("k", "v"); err != nil {
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
		t.Fatalf("idle client still usable after Shutdown")
	}
	if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		conn.Close()
		t.Fatalf("server still accepting connections after Shutdown")
	}
}

func TestShutdownRunsBufferedCommands(t *testing.T) {
	srv, addr, errc := startServer(t)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Pipeline two commands, and wait for the first reply so both are
	// known to have reached the server before Shutdown starts.
	w := resp.NewWriter(conn)
	for _, cmd := range [][]string{{"SET", "a", "1"}, {"SET", "b", "2"}} {
		if err := w.Write(cmdValue(cmd...)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	p := resp.NewParser(conn)
	if v, err := p.Parse(); err != nil || v.Str != "OK" {
		t.Fatalf("first reply = %+v, %v", v, err)
	}

	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	waitServe(t, errc)

	if v, err := p.Parse(); err != nil || v.Str != "OK" {
		t.Fatalf("second pipelined reply = %+v, %v; want OK", v, err)
	}
	if _, ok := srv.store.Get("b"); !ok {
		t.Fatalf("pipelined SET b was not applied")
	}
}

func TestShutdownTimeoutForceClosesStuckConnections(t *testing.T) {
	srv, addr, errc := startServer(t)
	c := dial(t, addr)
	big := strings.Repeat("x", 1<<20)
	if err := c.Set("big", big); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Ask for far more reply data than the socket buffers hold and never
	// read it, so the server blocks writing a reply and cannot drain.
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	w := resp.NewWriter(conn)
	for i := 0; i < 64; i++ {
		if err := w.Write(cmdValue("GET", "big")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	time.Sleep(100 * time.Millisecond) // let the server fill the socket

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := srv.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want context.DeadlineExceeded", err)
	}
	waitServe(t, errc)

	srv.mu.Lock()
	n := len(srv.conns)
	srv.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d connections still tracked after forced Shutdown", n)
	}
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

func cmdValue(args ...string) resp.Value {
	items := make([]resp.Value, len(args))
	for i, a := range args {
		items[i] = resp.Value{Type: resp.TypeBulkString, Bulk: []byte(a)}
	}
	return resp.Value{Type: resp.TypeArray, Array: items}
}

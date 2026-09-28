// Command everestkv runs the EverestKV server: an HTTP key-value API
// listening on :8379, so curl or any HTTP client can talk to it directly.
//
// On SIGINT or SIGTERM it shuts down gracefully: it stops accepting
// connections and waits up to shutdownTimeout for in-flight requests to
// finish. A second signal during that wait exits immediately.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Ranjit-Khanal/everestkv/internal/server"
)

const shutdownTimeout = 10 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(server.DefaultConfig())
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	select {
	case err := <-serveErr:
		log.Fatal(err)
	case <-ctx.Done():
	}
	// Restore default signal handling so a second signal kills the process.
	stop()

	log.Printf("shutting down (waiting up to %s for in-flight requests; signal again to force)", shutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown timed out, closed remaining connections: %v", err)
	}
	if err := <-serveErr; !errors.Is(err, server.ErrServerClosed) {
		log.Printf("server: %v", err)
	}
	log.Printf("bye")
}

// Command everestkv runs the server on :8379.
//
// On SIGINT or SIGTERM it waits up to shutdownTimeout for requests to finish.
// A second signal exits right away.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Ranjit-Khanal/everestkv/internal/http/handlers"
	"github.com/Ranjit-Khanal/everestkv/internal/server"
	"github.com/Ranjit-Khanal/everestkv/internal/service"
	"github.com/Ranjit-Khanal/everestkv/internal/store"
)

const shutdownTimeout = 10 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// store → service → handlers → server
	st := store.NewStore()
	kv := service.NewKV(st)
	srv := server.NewServer(server.DefaultConfig(), handlers.NewHandlers(kv).Routes())
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	select {
	case err := <-serveErr:
		log.Fatal(err)
	case <-ctx.Done():
	}
	// Let a second signal kill the process.
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

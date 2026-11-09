// Command hoard runs the Höðrs Hoard web server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/keyboardsmash/hodrs-hoard/internal/config"
	"github.com/keyboardsmash/hodrs-hoard/internal/server"
)

// version is set at build time:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/hoard
var version = "dev"

func main() {
	// Keep main tiny: run returns an error instead of exiting, which makes
	// cleanup (deferred calls) run reliably before os.Exit.
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "hoard:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))

	srv, err := server.New(version, log)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second, // protects against slow-header clients
	}

	// ctx is cancelled when the process gets Ctrl+C (SIGINT) or SIGTERM,
	// which is what `podman stop` sends.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop() // `defer` runs when run() returns, like a finally block

	// Run the server in a goroutine (a lightweight thread) so this function
	// can wait for either a server error or a shutdown signal.
	// errCh is a channel: a typed pipe between goroutines.
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "version", version)
		errCh <- httpServer.ListenAndServe()
	}()

	// select waits until one of the channels has a value.
	select {
	case err := <-errCh:
		return err // e.g. port already in use
	case <-ctx.Done():
		log.Info("shutting down")
	}

	// Give in-flight requests up to 10 seconds to finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}
	// After Shutdown, ListenAndServe returns http.ErrServerClosed; that is expected.
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

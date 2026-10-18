// Command server runs the Tutti Frutti game server.
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

	"github.com/AresiusXP/tutti-frutti/server/internal/config"
	"github.com/AresiusXP/tutti-frutti/server/internal/httpapi"
)

// version is overridden at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

const (
	// readinessGrace gives Kubernetes time to take the pod out of the
	// Service endpoints before we stop accepting connections.
	readinessGrace = 2 * time.Second
	// shutdownTimeout bounds the graceful drain (terminationGracePeriodSeconds is 30).
	shutdownTimeout = 20 * time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	api := httpapi.New(httpapi.Options{Logger: logger, Version: version})
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No Read/WriteTimeout: they would also cut long-lived WebSockets.
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server listening", "addr", srv.Addr, "version", version)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	api.SetReady(false)
	time.Sleep(readinessGrace)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}

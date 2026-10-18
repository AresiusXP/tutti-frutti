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

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/config"
	"github.com/AresiusXP/tutti-frutti/server/internal/httpapi"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
	"github.com/AresiusXP/tutti-frutti/server/internal/ws"
)

// version is overridden at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

// Shutdown budget (docs/DESIGN.md 9a); the total stays below the pod's
// terminationGracePeriodSeconds (30).
const (
	// readinessGrace gives Kubernetes time to take the pod out of the
	// Service endpoints before we stop accepting connections.
	readinessGrace = 2 * time.Second
	// hubShutdownTimeout bounds the room drain: final snapshots, closing every
	// WebSocket with 1012 (http.Server.Shutdown does not track hijacked
	// connections, so the hub is what drains them) and the last attempt to
	// record the statistics of games that just ended.
	hubShutdownTimeout = 15 * time.Second
	// wsShutdownTimeout bounds closing the sockets the hub does not track and
	// waiting for their goroutines.
	wsShutdownTimeout = 3 * time.Second
	// httpShutdownTimeout bounds the remaining in-flight HTTP requests.
	httpShutdownTimeout = 5 * time.Second
	// startupTimeout bounds database connection, migrations and restore.
	startupTimeout = 2 * time.Minute
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// One registry shared by the hub, the WebSocket handler and the HTTP API.
	reg := prometheus.NewRegistry()

	// Persistence: Postgres when configured, otherwise memory (games then do
	// not survive a restart).
	startCtx, cancelStart := context.WithTimeout(ctx, startupTimeout)
	defer cancelStart()

	var (
		st          store.Backend
		readyChecks []func(context.Context) error
	)
	if cfg.DatabaseURL != "" {
		pg, err := store.NewPostgres(startCtx, cfg.DatabaseURL, store.WithLogger(logger))
		if err != nil {
			return fmt.Errorf("database: %w", err)
		}
		st = pg
		readyChecks = append(readyChecks, pg.Ping)
	} else {
		logger.Warn("DATABASE_URL is empty: using the in-memory store, games will not survive a restart")
		st = store.NewMemory()
	}
	defer st.Close()

	hub := room.NewHub(room.Config{
		Store:      st,
		Logger:     logger,
		Registerer: reg,
		MaxRooms:   cfg.MaxRooms,
		Recorder:   st,
	})
	// hub.Start ties its janitor to the context it receives, so that context
	// must live as long as the server (the signal context), not just for the
	// startup. The startup budget is enforced by a watchdog that is disarmed as
	// soon as Start returns: if the restore hangs it cancels the context and
	// Start fails; afterwards nothing cancels it but SIGTERM.
	hubCtx, cancelHubCtx := context.WithCancel(ctx)
	defer cancelHubCtx()
	watchdog := time.AfterFunc(startupTimeout, cancelHubCtx)
	restored, err := hub.Start(hubCtx)
	watchdog.Stop()
	if err != nil {
		return fmt.Errorf("restore rooms: %w", err)
	}
	logger.Info("rooms restored", "count", restored)

	// One secret for every signed cookie; an empty SESSION_SECRET becomes one
	// random secret (with a warning) shared by all of them.
	secret := auth.NewSecret([]byte(cfg.SessionSecret), logger)
	guests := auth.NewGuests(secret, cfg.SecureCookies(), logger)
	sessions := auth.NewSessions(secret, cfg.SecureCookies())
	// Google's discovery is lazy: an unreachable Google never delays or fails
	// the start (the first login retries it).
	var google *auth.Google
	if cfg.GoogleEnabled() {
		google = auth.NewGoogle(auth.GoogleConfig{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  cfg.GoogleRedirectURL(),
			Secret:       secret,
			Secure:       cfg.SecureCookies(),
			Logger:       logger,
		})
	}
	origins := cfg.Origins()

	api := httpapi.New(httpapi.Options{
		Logger:            logger,
		Version:           version,
		Registry:          reg,
		Rooms:             hub,
		Guests:            guests,
		Sessions:          sessions,
		Accounts:          st,
		Google:            google,
		AllowedOrigins:    origins,
		TrustForwardedFor: cfg.TrustForwardedFor,
		ReadyChecks:       readyChecks,
	})
	wsHandler := ws.NewHandler(ws.Deps{
		Rooms:             hub,
		Guests:            guests,
		Sessions:          sessions,
		AllowedOrigins:    origins,
		TrustForwardedFor: cfg.TrustForwardedFor,
		Logger:            logger,
		Registerer:        reg,
	})
	api.Mux().Handle("GET /ws", wsHandler)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No Read/WriteTimeout: they would also cut long-lived WebSockets.
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server listening", "addr", srv.Addr, "version", version,
			"database", cfg.DatabaseURL != "", "google", google != nil, "origins", origins)
		errCh <- srv.ListenAndServe()
	}()

	var serveErr error
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = fmt.Errorf("http server: %w", err)
		}
	case <-ctx.Done():
		logger.Info("shutting down")
		// 1. Leave the Service endpoints, 2. drain rooms and WebSockets,
		// 3. finish in-flight HTTP requests.
		api.SetReady(false)
		time.Sleep(readinessGrace)
	}

	drainCtx, cancelDrain := context.WithTimeout(context.Background(), hubShutdownTimeout)
	defer cancelDrain()
	if err := hub.Shutdown(drainCtx); err != nil {
		logger.Error("room shutdown did not finish cleanly", "err", err)
	}

	// Sockets that are not attached to a room (still unjoined, or already
	// closed by the hub) are not tracked by the hub; the handler drains them
	// and waits for every connection goroutine, because http.Server.Shutdown
	// ignores hijacked connections.
	wsCtx, cancelWS := context.WithTimeout(context.Background(), wsShutdownTimeout)
	defer cancelWS()
	if err := wsHandler.Shutdown(wsCtx); err != nil {
		logger.Error("websocket drain did not finish cleanly", "err", err)
	}

	httpCtx, cancelHTTP := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancelHTTP()
	if err := srv.Shutdown(httpCtx); err != nil && serveErr == nil {
		serveErr = fmt.Errorf("graceful shutdown: %w", err)
	}
	if serveErr == nil {
		logger.Info("shutdown complete")
	}
	return serveErr
}

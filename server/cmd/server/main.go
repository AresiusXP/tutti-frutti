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
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/config"
	"github.com/AresiusXP/tutti-frutti/server/internal/httpapi"
	"github.com/AresiusXP/tutti-frutti/server/internal/judge"
	"github.com/AresiusXP/tutti-frutti/server/internal/moderation"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
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
		st          store.FullBackend
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

	// The terms gate (docs/DESIGN.md, "Safety"): one object for the room's join
	// check and the REST endpoints, so a guest that accepted is remembered by
	// both for the life of the process.
	terms := safety.NewTerms(st, nil)

	// The moderation policy (blocks and restrictions) is loaded BEFORE the hub
	// starts or accepts a join: a hub that served a join first could admit a pair
	// the store says must not share a room (docs/DESIGN.md, "Safety"). A store that
	// cannot answer stops the start; an empty policy would silently turn every
	// block and restriction off.
	policy := safety.NewPolicy(nil)
	nBlocks, nRestrictions, err := moderation.LoadPolicy(startCtx, st, policy, time.Now())
	if err != nil {
		return fmt.Errorf("moderation policy: %w", err)
	}
	logger.Info("moderation policy loaded", "blocks", nBlocks, "restrictions", nRestrictions)
	gauges := moderation.NewReportGauges(reg, nil)

	// The AI judge is optional: without the Cloudflare settings the host is
	// never offered it. A nil *judge.Cloudflare must not reach the interface.
	roomCfg := room.Config{
		Terms:             terms,
		Store:             st,
		Logger:            logger,
		Registerer:        reg,
		MaxRooms:          cfg.MaxRooms,
		Recorder:          st,
		BotsEnabled:       cfg.BotsEnabled,
		MaxBots:           cfg.MaxBots,
		BotFirstDelayMin:  cfg.BotFirstDelayMin,
		BotFirstDelayMax:  cfg.BotFirstDelayMax,
		BotSecondDelayMin: cfg.BotSecondDelayMin,
		BotSecondDelayMax: cfg.BotSecondDelayMax,
		PublicDisabled:    !cfg.PublicRoomsEnabled,
		Policy:            policy,
	}
	var judgeSwitch *judge.Switch
	if cfg.JudgeEnabled() {
		j, err := judge.New(judge.Config{
			AccountID:    cfg.CloudflareAccountID,
			APIToken:     cfg.CloudflareAPIToken,
			DailyNeurons: float64(cfg.JudgeDailyNeurons),
			Logger:       logger,
			Registerer:   reg,
		})
		if err != nil {
			return fmt.Errorf("ai judge: %w", err)
		}
		// The operator's kill switch sits in front of the provider (see
		// docs/MODERATION.md); it is a Judge like any other.
		judgeSwitch = judge.NewSwitch(j, reg)
		roomCfg.Judge = judgeSwitch
	}
	hub := room.NewHub(roomCfg)
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
	if !cfg.PublicRoomsEnabled {
		logger.Warn("public games are switched off (PUBLIC_ROOMS_ENABLED=false): new public games cannot be created or joined; players already seated in a public game can reconnect and finish it; private rooms work as usual")
	}

	// The operator's side of the safety layer (docs/MODERATION.md): the daily
	// retention purge and the control socket. Both stop with the process, before
	// the store is closed.
	var background sync.WaitGroup
	bgCtx, stopBackground := context.WithCancel(ctx)
	defer func() {
		stopBackground()
		background.Wait()
	}()
	background.Go(func() {
		(&moderation.Maintenance{Safety: st, Mod: st, Gauges: gauges, Log: logger}).Run(bgCtx)
	})
	if cfg.ModerationSocket != "" {
		deps := moderation.Deps{
			Reports: st, Mod: st, Policy: policy, Rooms: hub, Public: hub, Gauges: gauges, Log: logger, Version: version,
		}
		if judgeSwitch != nil { // a nil *judge.Switch must not reach the interface
			deps.Judge = judgeSwitch
		}
		// A control socket that cannot be created is logged loudly and does not
		// stop the game: the owner's daily `moderate status` fails and says so.
		if l, err := moderation.Listen(cfg.ModerationSocket); err != nil {
			logger.Error("the moderation control socket could not be created: the operator tool is unavailable", "err", err)
		} else {
			logger.Info("moderation control socket listening", "path", cfg.ModerationSocket)
			background.Go(func() {
				if err := moderation.NewServer(deps).Serve(bgCtx, l); err != nil {
					logger.Error("the moderation control server stopped", "err", err)
				}
			})
		}
	}

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
		Terms:             terms,
		Safety:            st,
		Moderation:        st,
		Policy:            policy,
		Reports:           gauges,
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
			"database", cfg.DatabaseURL != "", "google", google != nil, "judge", cfg.JudgeEnabled(), "origins", origins,
			"terms", terms.Version())
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

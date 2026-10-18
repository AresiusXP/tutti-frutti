package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const (
	// migrateTimeout bounds the whole migration step (lock wait included).
	migrateTimeout = 2 * time.Minute
	// A starting instance polls for the migration lock every lockPollSeconds,
	// lockPollAttempts times, before giving up.
	lockPollSeconds  = 1
	lockPollAttempts = 90
)

// newMigrationProvider builds a goose Provider over db with the embedded
// migrations. Concurrent starters are serialised by a Postgres session
// advisory lock held on the connection that runs the migrations.
func newMigrationProvider(db *sql.DB, logger *slog.Logger) (*goose.Provider, error) {
	fsys, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(lockPollSeconds, lockPollAttempts))
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, db, fsys,
		goose.WithSlog(logger),
		goose.WithSessionLocker(locker),
	)
}

// migrate applies the pending embedded migrations. It uses a short-lived
// database/sql handle with its own connections (not the runtime pool), so the
// session lock can never leak into a pooled connection.
func migrate(ctx context.Context, cfg *pgx.ConnConfig, logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, migrateTimeout)
	defer cancel()

	db := stdlib.OpenDB(*cfg)
	defer func() { _ = db.Close() }()

	provider, err := newMigrationProvider(db, logger)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	if len(results) > 0 {
		logger.Info("store: migrations applied", "count", len(results), "last", results[len(results)-1].Source.Path)
	}
	return nil
}

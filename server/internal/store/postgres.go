package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultMaxConns    = 8
	connectTimeout     = 10 * time.Second
	healthCheckPeriod  = 30 * time.Second
	startupPingTimeout = 10 * time.Second

	startupErrPrefix = "store: postgres startup: "
	opErrPrefix      = "store: postgres: "
)

const (
	upsertSQL    = `INSERT INTO room_snapshots (code, state, updated_at) VALUES ($1, $2, $3) ON CONFLICT (code) DO UPDATE SET state = EXCLUDED.state, updated_at = EXCLUDED.updated_at`
	deleteSQL    = `DELETE FROM room_snapshots WHERE code = $1`
	loadAllSQL   = `SELECT code, state, updated_at FROM room_snapshots ORDER BY code COLLATE "C"`
	deleteOldSQL = `DELETE FROM room_snapshots WHERE updated_at < $1`
)

// Postgres is a Store backed by PostgreSQL (table room_snapshots, created by
// the embedded goose migrations). It is safe for concurrent use.
type Postgres struct {
	pool *pgxpool.Pool
}

var _ Store = (*Postgres)(nil)

// PostgresOption customises NewPostgres.
type PostgresOption func(*postgresConfig)

type postgresConfig struct {
	logger   *slog.Logger
	maxConns int32
}

// WithLogger sends migration logs (and nothing else) to l. Without it the
// store is silent.
func WithLogger(l *slog.Logger) PostgresOption {
	return func(c *postgresConfig) { c.logger = l }
}

// WithMaxConns overrides the pool size (default 8); values < 1 are ignored.
func WithMaxConns(n int) PostgresOption {
	return func(c *postgresConfig) {
		if n >= 1 && n <= 1<<20 {
			c.maxConns = int32(n)
		}
	}
}

// NewPostgres connects to databaseURL, verifies the connection, applies the
// embedded migrations and returns the store. Pool settings come from code
// (8 connections, none kept warm, 10 s connect timeout, 30 s health checks);
// pool_* parameters in the URL are overridden. Errors never include the URL or
// the password.
func NewPostgres(ctx context.Context, databaseURL string, opts ...PostgresOption) (*Postgres, error) {
	conf := postgresConfig{maxConns: defaultMaxConns}
	for _, opt := range opts {
		opt(&conf)
	}
	logger := conf.logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New(startupErrPrefix + "database URL is empty")
	}
	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, newCredentials(databaseURL).wrap(startupErrPrefix+"invalid database URL", err, false)
	}
	creds := newCredentials(databaseURL, poolCfg.ConnConfig.Password)
	fail := func(op string, err error) error {
		return creds.wrap(startupErrPrefix+op, err, true)
	}

	poolCfg.MaxConns = conf.maxConns
	poolCfg.MinConns = 0
	poolCfg.MinIdleConns = 0
	poolCfg.HealthCheckPeriod = healthCheckPeriod
	if poolCfg.ConnConfig.ConnectTimeout <= 0 {
		poolCfg.ConnConfig.ConnectTimeout = connectTimeout
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fail("create pool", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, startupPingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fail("ping", err)
	}
	if err := migrate(ctx, poolCfg.ConnConfig, logger); err != nil {
		pool.Close()
		return nil, fail("migrate", err)
	}
	return &Postgres{pool: pool}, nil
}

// Save implements Store (upsert; UpdatedAt defaults to the current time).
func (p *Postgres) Save(ctx context.Context, s Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateSnapshot(s); err != nil {
		return err
	}
	stamp := s.UpdatedAt
	if stamp.IsZero() {
		stamp = time.Now()
	}
	if _, err := p.pool.Exec(ctx, upsertSQL, s.Code, []byte(s.State), normalizeTime(stamp)); err != nil {
		return fmt.Errorf("%ssave %q: %w", opErrPrefix, s.Code, err)
	}
	return nil
}

// Delete implements Store.
func (p *Postgres) Delete(ctx context.Context, code string) error {
	if _, err := p.pool.Exec(ctx, deleteSQL, code); err != nil {
		return fmt.Errorf("%sdelete %q: %w", opErrPrefix, code, err)
	}
	return nil
}

// LoadAll implements Store; snapshots are sorted by Code in byte order.
func (p *Postgres) LoadAll(ctx context.Context) ([]Snapshot, error) {
	rows, err := p.pool.Query(ctx, loadAllSQL)
	if err != nil {
		return nil, fmt.Errorf("%sload all: %w", opErrPrefix, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Snapshot, error) {
		var (
			s     Snapshot
			state []byte
		)
		if err := row.Scan(&s.Code, &state, &s.UpdatedAt); err != nil {
			return Snapshot{}, err
		}
		s.State = json.RawMessage(state)
		s.UpdatedAt = s.UpdatedAt.UTC()
		return s, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%sload all: %w", opErrPrefix, err)
	}
	return out, nil
}

// DeleteOlderThan implements Store; a snapshot updated exactly at t is kept.
func (p *Postgres) DeleteOlderThan(ctx context.Context, t time.Time) (int64, error) {
	tag, err := p.pool.Exec(ctx, deleteOldSQL, normalizeTime(t))
	if err != nil {
		return 0, fmt.Errorf("%sdelete older than %s: %w", opErrPrefix, t.UTC().Format(time.RFC3339), err)
	}
	return tag.RowsAffected(), nil
}

// Ping implements Store.
func (p *Postgres) Ping(ctx context.Context) error {
	if err := p.pool.Ping(ctx); err != nil {
		return fmt.Errorf("%sping: %w", opErrPrefix, err)
	}
	return nil
}

// Close implements Store; it waits for in-flight queries and is idempotent.
func (p *Postgres) Close() {
	p.pool.Close()
}

// redactedError carries a message scrubbed of credentials.
type redactedError struct {
	msg   string
	cause error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.cause }

// credentials identifies what must never appear in an error message.
type credentials struct {
	url       string
	passwords []string
}

func newCredentials(databaseURL string, extraPasswords ...string) credentials {
	c := credentials{url: databaseURL}
	for _, pw := range append([]string{urlPassword(databaseURL)}, extraPasswords...) {
		if pw == "" {
			continue
		}
		c.passwords = append(c.passwords, pw, url.QueryEscape(pw), strings.TrimPrefix(url.UserPassword("", pw).String(), ":"))
	}
	return c
}

// wrap returns "<op>: <err>" with the URL and passwords scrubbed from the
// text. The cause stays reachable through errors.Is/As (context errors,
// *pgconn.PgError) only when keepCause is set; *pgconn.ParseConfigError
// exposes the raw URL, so parse failures drop it.
func (c credentials) wrap(op string, err error, keepCause bool) error {
	msg := op + ": " + err.Error()
	if c.url != "" {
		msg = strings.ReplaceAll(msg, c.url, "<database-url>")
	}
	for _, pw := range c.passwords {
		if pw != "" {
			msg = strings.ReplaceAll(msg, pw, "xxxxx")
		}
	}
	out := &redactedError{msg: msg}
	if keepCause {
		out.cause = err
	}
	return out
}

// urlPassword extracts the userinfo password of a URL-style DSN ("" otherwise).
func urlPassword(databaseURL string) string {
	u, err := url.Parse(databaseURL)
	if err != nil || u.User == nil {
		return ""
	}
	pw, _ := u.User.Password()
	return pw
}

package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Postgres tests need a real database. Set TEST_DATABASE_URL to run them, e.g.
//
//	TEST_DATABASE_URL='postgres://tutti:tutti@127.0.0.1:55432/tutti?sslmode=disable'
//
// WARNING: they TRUNCATE room_snapshots and DROP the room_snapshots and
// goose_db_version tables to exercise the migrations from scratch, so the URL
// must point at a throwaway database (CI uses a service container).
const testDatabaseEnv = "TEST_DATABASE_URL"

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv(testDatabaseEnv)
	if dsn == "" {
		t.Skipf("%s is not set: skipping Postgres tests (they TRUNCATE room_snapshots and drop the schema, use a throwaway database)", testDatabaseEnv)
	}
	return dsn
}

func openTestPostgres(t *testing.T, dsn string, opts ...PostgresOption) *Postgres {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	p, err := NewPostgres(ctx, dsn, opts...)
	if err != nil {
		t.Fatalf("NewPostgres: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// adminExec runs statements on a throwaway direct connection. It does not use
// t.Context() because it is also called from t.Cleanup, after that is cancelled.
func adminExec(t *testing.T, dsn string, statements ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	for _, stmt := range statements {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
}

func adminQueryInt(t *testing.T, dsn, query string, args ...any) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var n int
	if err := conn.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

// resetSchema removes everything the store creates, so the next NewPostgres
// migrates from scratch.
func resetSchema(t *testing.T, dsn string) {
	t.Helper()
	adminExec(t, dsn, `DROP TABLE IF EXISTS room_snapshots`, `DROP TABLE IF EXISTS goose_db_version`)
}

func TestPostgresContract(t *testing.T) {
	dsn := testDatabaseURL(t)
	testStoreContract(t, func(t *testing.T) Store {
		p := openTestPostgres(t, dsn)
		adminExec(t, dsn, `TRUNCATE room_snapshots`)
		return p
	})
}

func TestPostgresMigrations(t *testing.T) {
	dsn := testDatabaseURL(t)

	t.Run("fresh database gets the schema", func(t *testing.T) {
		resetSchema(t, dsn)
		openTestPostgres(t, dsn)

		columns := []struct {
			name, dataType, nullable string
		}{
			{"code", "text", "NO"},
			{"state", "jsonb", "NO"},
			{"updated_at", "timestamp with time zone", "NO"},
		}
		for _, c := range columns {
			n := adminQueryInt(t, dsn, `SELECT count(*) FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = 'room_snapshots'
				AND column_name = $1 AND data_type = $2 AND is_nullable = $3`, c.name, c.dataType, c.nullable)
			if n != 1 {
				t.Errorf("column %s %s NOT NULL=%s not found", c.name, c.dataType, c.nullable)
			}
		}
		if n := adminQueryInt(t, dsn, `SELECT count(*) FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'room_snapshots'`); n != len(columns) {
			t.Errorf("room_snapshots has %d columns, want %d", n, len(columns))
		}
		if n := adminQueryInt(t, dsn, `SELECT count(*) FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'room_snapshots'
			AND column_name = 'updated_at' AND column_default LIKE 'now()%'`); n != 1 {
			t.Error("updated_at has no default now()")
		}
		if n := adminQueryInt(t, dsn, `SELECT count(*) FROM pg_index i
			JOIN pg_class c ON c.oid = i.indrelid
			JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = ANY (i.indkey)
			WHERE c.relname = 'room_snapshots' AND i.indisprimary AND a.attname = 'code'`); n != 1 {
			t.Error("code is not the primary key")
		}
		if n := adminQueryInt(t, dsn, `SELECT count(*) FROM pg_indexes
			WHERE tablename = 'room_snapshots' AND indexname = 'room_snapshots_updated_at_idx'
			AND indexdef LIKE '%(updated_at)%'`); n != 1 {
			t.Error("index room_snapshots_updated_at_idx on (updated_at) not found")
		}
	})

	t.Run("second start is a no-op", func(t *testing.T) {
		resetSchema(t, dsn)

		var firstLog bytes.Buffer
		first := openTestPostgres(t, dsn, WithLogger(slog.New(slog.NewTextHandler(&firstLog, &slog.HandlerOptions{Level: slog.LevelDebug}))))
		if !strings.Contains(firstLog.String(), "migrations applied") {
			t.Errorf("first start did not log the migration:\n%s", firstLog.String())
		}
		mustSave(t, first, snap("KEEP", `{"survives":true}`, baseTime))
		applied := adminQueryInt(t, dsn, `SELECT count(*) FROM goose_db_version WHERE is_applied`)
		if applied != 2 { // goose's version 0 bootstrap row + our migration
			t.Fatalf("goose_db_version has %d applied rows, want 2", applied)
		}

		var secondLog bytes.Buffer
		second := openTestPostgres(t, dsn, WithLogger(slog.New(slog.NewTextHandler(&secondLog, &slog.HandlerOptions{Level: slog.LevelDebug}))))
		if strings.Contains(secondLog.String(), "migrations applied") {
			t.Errorf("second start re-applied migrations:\n%s", secondLog.String())
		}
		if got := adminQueryInt(t, dsn, `SELECT count(*) FROM goose_db_version`); got != applied {
			t.Errorf("goose_db_version rows = %d, want %d (unchanged)", got, applied)
		}
		if got := mustLoadAll(t, second); len(got) != 1 || got[0].Code != "KEEP" {
			t.Errorf("data lost by the second start: %v", got)
		}
	})

	t.Run("concurrent starters are serialised", func(t *testing.T) {
		resetSchema(t, dsn)
		const starters = 5
		var wg sync.WaitGroup
		stores := make([]*Postgres, starters)
		errs := make([]error, starters)
		for i := range starters {
			wg.Go(func() {
				ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
				defer cancel()
				stores[i], errs[i] = NewPostgres(ctx, dsn)
			})
		}
		wg.Wait()
		for i := range starters {
			if errs[i] != nil {
				t.Errorf("starter %d: %v", i, errs[i])
				continue
			}
			t.Cleanup(stores[i].Close)
		}
		if got := adminQueryInt(t, dsn, `SELECT count(*) FROM goose_db_version WHERE version_id = 1 AND is_applied`); got != 1 {
			t.Errorf("migration 1 recorded %d times, want 1", got)
		}
	})

	t.Run("down migration drops the schema and up recreates it", func(t *testing.T) {
		resetSchema(t, dsn)
		openTestPostgres(t, dsn).Close()

		cfg := mustParseConnConfig(t, dsn)
		db := stdlib.OpenDB(*cfg)
		defer func() { _ = db.Close() }()
		provider, err := newMigrationProvider(db, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := provider.DownTo(t.Context(), 0); err != nil {
			t.Fatalf("down: %v", err)
		}
		if n := adminQueryInt(t, dsn, `SELECT count(*) FROM pg_tables WHERE tablename = 'room_snapshots'`); n != 0 {
			t.Error("room_snapshots still exists after down")
		}
		if n := adminQueryInt(t, dsn, `SELECT count(*) FROM pg_indexes WHERE indexname = 'room_snapshots_updated_at_idx'`); n != 0 {
			t.Error("index still exists after down")
		}

		p := openTestPostgres(t, dsn)
		mustSave(t, p, snap("BACK", `{}`, baseTime))
	})

	t.Run("migration failure is reported and cleans up", func(t *testing.T) {
		resetSchema(t, dsn)
		t.Cleanup(func() { resetSchema(t, dsn) })
		// A pre-existing table that goose does not know about makes the
		// CREATE TABLE fail.
		adminExec(t, dsn, `CREATE TABLE room_snapshots (code text)`)

		p, err := NewPostgres(t.Context(), dsn)
		if err == nil {
			p.Close()
			t.Fatal("NewPostgres succeeded against a conflicting schema")
		}
		if !strings.Contains(err.Error(), "migrate") {
			t.Errorf("error does not say it is a migration failure: %v", err)
		}
	})

	t.Run("silent without a logger", func(t *testing.T) {
		resetSchema(t, dsn)
		var std bytes.Buffer
		prevOut, prevFlags := log.Writer(), log.Flags()
		log.SetOutput(&std)
		t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })

		openTestPostgres(t, dsn)
		if std.Len() != 0 {
			t.Errorf("goose wrote to the standard logger: %q", std.String())
		}
	})

	t.Run("goose output goes to the provided logger", func(t *testing.T) {
		resetSchema(t, dsn)
		var out bytes.Buffer
		openTestPostgres(t, dsn, WithLogger(slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))))
		for _, want := range []string{"00001_room_snapshots.sql", "migrations applied"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("log output lacks %q:\n%s", want, out.String())
			}
		}
	})
}

func TestPostgresPoolSettings(t *testing.T) {
	dsn := testDatabaseURL(t)

	tests := []struct {
		name string
		opts []PostgresOption
		want int32
	}{
		{"default", nil, 8},
		{"WithMaxConns", []PostgresOption{WithMaxConns(3)}, 3},
		{"WithMaxConns zero is ignored", []PostgresOption{WithMaxConns(0)}, 8},
		{"WithMaxConns negative is ignored", []PostgresOption{WithMaxConns(-4)}, 8},
		{"WithLogger nil is silent", []PostgresOption{WithLogger(nil)}, 8},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := openTestPostgres(t, dsn, tc.opts...)
			cfg := p.pool.Config()
			if cfg.MaxConns != tc.want {
				t.Errorf("MaxConns = %d, want %d", cfg.MaxConns, tc.want)
			}
			if cfg.MinConns != 0 || cfg.MinIdleConns != 0 {
				t.Errorf("MinConns/MinIdleConns = %d/%d, want 0/0", cfg.MinConns, cfg.MinIdleConns)
			}
			if cfg.HealthCheckPeriod != 30*time.Second {
				t.Errorf("HealthCheckPeriod = %v, want 30s", cfg.HealthCheckPeriod)
			}
			if cfg.ConnConfig.ConnectTimeout != 10*time.Second {
				t.Errorf("ConnectTimeout = %v, want 10s", cfg.ConnConfig.ConnectTimeout)
			}
		})
	}

	t.Run("explicit connect_timeout in the URL is respected", func(t *testing.T) {
		p := openTestPostgres(t, dsn+withQuery(dsn, "connect_timeout=3"))
		if got := p.pool.Config().ConnConfig.ConnectTimeout; got != 3*time.Second {
			t.Errorf("ConnectTimeout = %v, want 3s", got)
		}
	})

	t.Run("a single connection serves concurrent callers", func(t *testing.T) {
		p := openTestPostgres(t, dsn, WithMaxConns(1))
		adminExec(t, dsn, `TRUNCATE room_snapshots`)
		var wg sync.WaitGroup
		for i := range 10 {
			wg.Go(func() {
				if err := p.Save(t.Context(), snap(fmt.Sprintf("P%02d", i), `{}`, baseTime)); err != nil {
					t.Errorf("Save: %v", err)
				}
			})
		}
		wg.Wait()
		if got := mustLoadAll(t, p); len(got) != 10 {
			t.Errorf("stored %d snapshots, want 10", len(got))
		}
	})
}

// withQuery returns the suffix that appends a query parameter to dsn, which
// must be URL-style (the CI and docs URLs are).
func withQuery(dsn, param string) string {
	if strings.Contains(dsn, "?") {
		return "&" + param
	}
	return "?" + param
}

func mustParseConnConfig(t *testing.T, dsn string) *pgx.ConnConfig {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	return cfg
}

func TestPostgresSnapshotSurvivesRestart(t *testing.T) {
	dsn := testDatabaseURL(t)

	first := openTestPostgres(t, dsn)
	adminExec(t, dsn, `TRUNCATE room_snapshots`)
	mustSave(t, first,
		snap("ZZ99", `{"game":{"phase":"playing","players":[{"id":"a"}]},"name":"Ñandú 🍉"}`, baseTime),
		snap("AA11", `{"phase":"lobby"}`, baseTime.Add(time.Minute)),
	)
	first.Close()

	second := openTestPostgres(t, dsn)
	got := mustLoadAll(t, second)
	if len(got) != 2 || got[0].Code != "AA11" || got[1].Code != "ZZ99" {
		t.Fatalf("after restart: %v", codesOf(got))
	}
	jsonEqual(t, []byte(`{"game":{"phase":"playing","players":[{"id":"a"}]},"name":"Ñandú 🍉"}`), got[1].State)
	if !got[1].UpdatedAt.Equal(baseTime) || !got[0].UpdatedAt.Equal(baseTime.Add(time.Minute)) {
		t.Errorf("UpdatedAt not preserved: %v %v", got[0].UpdatedAt, got[1].UpdatedAt)
	}
}

func TestPostgresAfterClose(t *testing.T) {
	dsn := testDatabaseURL(t)
	p := openTestPostgres(t, dsn)
	if err := p.Ping(t.Context()); err != nil {
		t.Fatalf("Ping before Close: %v", err)
	}
	p.Close()
	p.Close()

	if err := p.Ping(t.Context()); err == nil {
		t.Error("Ping after Close succeeded")
	}
	if err := p.Save(t.Context(), snap("AFTR", `{}`, baseTime)); err == nil {
		t.Error("Save after Close succeeded")
	}
	if err := p.Delete(t.Context(), "AFTR"); err == nil {
		t.Error("Delete after Close succeeded")
	}
	if _, err := p.LoadAll(t.Context()); err == nil {
		t.Error("LoadAll after Close succeeded")
	}
	if _, err := p.DeleteOlderThan(t.Context(), baseTime); err == nil {
		t.Error("DeleteOlderThan after Close succeeded")
	}
}

func TestPostgresQueryErrorsAreWrapped(t *testing.T) {
	dsn := testDatabaseURL(t)
	p := openTestPostgres(t, dsn)
	t.Cleanup(func() { resetSchema(t, dsn) })
	// Break the schema under the open store; the next test re-migrates.
	adminExec(t, dsn, `DROP TABLE room_snapshots`)

	checks := []struct {
		name string
		err  func() error
	}{
		{"Save", func() error { return p.Save(t.Context(), snap("ERR1", `{}`, baseTime)) }},
		{"Delete", func() error { return p.Delete(t.Context(), "ERR1") }},
		{"LoadAll", func() error { _, err := p.LoadAll(t.Context()); return err }},
		{"DeleteOlderThan", func() error { _, err := p.DeleteOlderThan(t.Context(), baseTime); return err }},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			err := c.err()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.HasPrefix(err.Error(), "store: postgres: ") {
				t.Errorf("error is not wrapped with the store prefix: %v", err)
			}
		})
	}
	if err := p.Ping(t.Context()); err != nil {
		t.Errorf("Ping must not depend on the table: %v", err)
	}
}

func TestPostgresStateIsStoredAsJSONB(t *testing.T) {
	dsn := testDatabaseURL(t)
	p := openTestPostgres(t, dsn)
	adminExec(t, dsn, `TRUNCATE room_snapshots`)
	mustSave(t, p, snap("JB01", `{"b": 1, "a": [1, 2], "n": {"x": "é"}}`, baseTime))
	if n := adminQueryInt(t, dsn, `SELECT count(*) FROM room_snapshots WHERE code = 'JB01'
		AND state @> '{"a":[1]}' AND jsonb_typeof(state) = 'object'`); n != 1 {
		t.Error("state is not queryable as jsonb")
	}
}

func TestNewPostgresInputErrors(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{"empty", "", "database URL is empty"},
		{"blank", "   ", "database URL is empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := NewPostgres(t.Context(), tc.dsn)
			if err == nil {
				p.Close()
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestNewPostgresUnreachable(t *testing.T) {
	const dsn = "postgres://tutti:pw@127.0.0.1:1/tutti?sslmode=disable&connect_timeout=2"

	t.Run("connection refused", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		p, err := NewPostgres(ctx, dsn)
		if err == nil {
			p.Close()
			t.Fatal("expected an error")
		}
		if !strings.HasPrefix(err.Error(), "store: postgres startup: ping:") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		p, err := NewPostgres(ctx, dsn)
		if err == nil {
			p.Close()
			t.Fatal("expected an error")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want it to wrap context.Canceled", err)
		}
	})
}

func TestNewPostgresErrorsDoNotLeakCredentials(t *testing.T) {
	const password = "s3cr3t-Pa$$w0rd!"
	escaped := url.QueryEscape(password)

	tests := []struct {
		name string
		dsn  string
	}{
		{"URL, connection refused", "postgres://tutti:" + escaped + "@127.0.0.1:1/tutti?sslmode=disable&connect_timeout=2"},
		{"URL, raw special characters", "postgres://tutti:" + password + "@127.0.0.1:1/tutti?sslmode=disable&connect_timeout=2"},
		{"URL, invalid port", "postgres://tutti:" + escaped + "@127.0.0.1:notaport/tutti"},
		{"URL, invalid port raw password", "postgres://tutti:" + password + "@127.0.0.1:notaport/tutti"},
		{"URL, unknown sslmode", "postgres://tutti:" + escaped + "@127.0.0.1:1/tutti?sslmode=bogus"},
		{"URL, password as query parameter", "postgres://tutti@127.0.0.1:1/tutti?sslmode=disable&connect_timeout=2&password=" + escaped},
		{"URL, malformed host", "postgres://tutti:" + escaped + "@[::1/tutti"},
		{"URL, bad percent escape", "postgres://tutti:" + escaped + "%zz@127.0.0.1:1/tutti"},
		{"URL, postgresql scheme", "postgresql://tutti:" + escaped + "@127.0.0.1:1/tutti?sslmode=disable&connect_timeout=2"},
		{"keyword/value, connection refused", "host=127.0.0.1 port=1 user=tutti password=" + password + " dbname=tutti sslmode=disable connect_timeout=2"},
		{"keyword/value, quoted password", "host=127.0.0.1 port=1 user=tutti password='" + password + "' dbname=tutti sslmode=disable connect_timeout=2"},
		{"keyword/value, unknown sslmode", "host=127.0.0.1 port=1 user=tutti password=" + password + " sslmode=bogus"},
		{"keyword/value, unknown keyword", "host=127.0.0.1 user=tutti password=" + password + " ssl-mode"},
		{"user:password@ without a scheme", "tutti:" + password + "@127.0.0.1:1/tutti"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			p, err := NewPostgres(ctx, tc.dsn)
			if err == nil {
				p.Close()
				t.Fatal("expected an error")
			}
			for _, rendering := range []string{
				err.Error(),
				fmt.Sprintf("%v", err),
				fmt.Sprintf("%+v", err),
				fmt.Sprintf("%#v", err),
				fmt.Sprintf("%q", err),
			} {
				for _, secret := range []string{password, escaped, tc.dsn} {
					if strings.Contains(rendering, secret) {
						t.Errorf("error leaks %q: %s", secret, rendering)
					}
				}
			}
		})
	}
}

func TestPostgresWrongPasswordDoesNotLeak(t *testing.T) {
	dsn := testDatabaseURL(t)
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		t.Skipf("%s is not a URL with credentials", testDatabaseEnv)
	}
	const wrong = "wr0ng-s3cr3t-Pa$$"
	u.User = url.UserPassword(u.User.Username(), wrong)

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	p, err := NewPostgres(ctx, u.String())
	if err == nil {
		p.Close()
		t.Skip("the server does not check passwords (trust authentication)")
	}
	for _, secret := range []string{wrong, url.QueryEscape(wrong), u.String()} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaks %q: %v", secret, err)
		}
	}
	if !strings.HasPrefix(err.Error(), "store: postgres startup: ") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewPostgresParseErrorDropsTheCause(t *testing.T) {
	_, err := NewPostgres(t.Context(), "postgres://tutti:hunter2@127.0.0.1:notaport/tutti")
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Unwrap(err) != nil {
		t.Errorf("parse error exposes its cause (it carries the raw URL): %T", errors.Unwrap(err))
	}
	if !strings.Contains(err.Error(), "invalid database URL") {
		t.Errorf("error = %v", err)
	}
}

func TestCredentialsWrap(t *testing.T) {
	cause := errors.New("dial postgres://u:pw%40x@h/db failed for pw@x and pw%40x")
	creds := newCredentials("postgres://u:pw%40x@h/db", "other")

	err := creds.wrap("op", cause, true)
	if strings.Contains(err.Error(), "pw") {
		t.Errorf("message not scrubbed: %s", err)
	}
	if !errors.Is(err, cause) {
		t.Error("cause not kept")
	}
	if errors.Unwrap(creds.wrap("op", cause, false)) != nil {
		t.Error("cause kept although keepCause=false")
	}

	// Empty secrets must not rewrite the whole message.
	if got := newCredentials("").wrap("op", errors.New("plain"), true).Error(); got != "op: plain" {
		t.Errorf("empty credentials rewrote the message: %q", got)
	}
}

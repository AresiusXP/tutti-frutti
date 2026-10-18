package integration

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// dbEnv enables the Postgres variants. The suite works in a private schema
// created for each test (so it can run next to the store package's tests and
// never touches a shared room_snapshots table); when the schema cannot be
// created it falls back to TRUNCATE on the shared table, so the URL must then
// point at a throwaway database.
const dbEnv = "TEST_DATABASE_URL"

func adminExec(ctx context.Context, dsn string, statements ...string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	for _, stmt := range statements {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("%q: %w", stmt, err)
		}
	}
	return nil
}

// openPostgres opens a Postgres store for one test (skipping when
// TEST_DATABASE_URL is not set).
func openPostgres(t *testing.T) store.Store {
	t.Helper()
	dsn := os.Getenv(dbEnv)
	if dsn == "" {
		t.Skipf("%s is not set: skipping the Postgres variant", dbEnv)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	target, isolated := dsn, false
	if u, err := url.Parse(dsn); err == nil && u.Host != "" {
		schema := fmt.Sprintf("tf_it_%d_%d", os.Getpid(), time.Now().UnixNano())
		if err := adminExec(ctx, dsn, "CREATE SCHEMA "+schema); err == nil {
			q := u.Query()
			q.Set("search_path", schema)
			u.RawQuery = q.Encode()
			target, isolated = u.String(), true
			t.Cleanup(func() {
				dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := adminExec(dropCtx, dsn, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
					t.Logf("dropping schema %s: %v", schema, err)
				}
			})
		} else {
			t.Logf("cannot create a private schema (%v): using the shared room_snapshots table", err)
		}
	}
	pg, err := store.NewPostgres(ctx, target)
	if err != nil {
		t.Fatalf("store.NewPostgres: %v", err)
	}
	t.Cleanup(pg.Close)
	if !isolated {
		if err := adminExec(ctx, dsn, "TRUNCATE room_snapshots"); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	return pg
}

// forEachStore runs fn once with in-memory stores and once with Postgres
// ones (skipped unless TEST_DATABASE_URL is set). fn calls open for every
// fresh store it needs (one per test: snapshots outlive a stack).
func forEachStore(t *testing.T, fn func(t *testing.T, open func(t *testing.T) store.Store)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		fn(t, func(*testing.T) store.Store { return store.NewMemory() })
	})
	t.Run("postgres", func(t *testing.T) {
		if os.Getenv(dbEnv) == "" {
			t.Skipf("%s is not set: skipping the Postgres variant", dbEnv)
		}
		fn(t, openPostgres)
	})
}

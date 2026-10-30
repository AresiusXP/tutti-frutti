package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

func newTestSafety(t *testing.T, dsn string) *Postgres {
	t.Helper()
	p := openTestPostgres(t, dsn)
	adminExec(t, dsn, `TRUNCATE room_snapshots, users, reports, terms_acceptances, blocks, restrictions, moderation_actions CASCADE`)
	return p
}

func TestPostgresSafetyContract(t *testing.T) {
	dsn := testDatabaseURL(t)
	testSafetyContract(t, func(t *testing.T) Safety { return newTestSafety(t, dsn) })
}

func TestPostgresModerationContract(t *testing.T) {
	dsn := testDatabaseURL(t)
	testModerationContract(t, func(t *testing.T) moderationBackend { return newTestSafety(t, dsn) })
}

func TestPostgresModerationSchema(t *testing.T) {
	dsn := testDatabaseURL(t)
	newTestSafety(t, dsn)

	t.Run("no foreign keys: blocks and restrictions outlive rooms and accounts", func(t *testing.T) {
		for _, table := range []string{"blocks", "restrictions", "moderation_actions"} {
			if n := adminQueryInt(t, dsn, `SELECT count(*) FROM information_schema.table_constraints
				WHERE table_schema = current_schema() AND table_name = $1 AND constraint_type = 'FOREIGN KEY'`, table); n != 0 {
				t.Errorf("%s has %d foreign keys", table, n)
			}
		}
	})

	t.Run("keys and indexes", func(t *testing.T) {
		if n := adminQueryInt(t, dsn, indexOn("blocks", "i.indisprimary", "blocker_guest,blocked_guest")); n != 1 {
			t.Error("PRIMARY KEY (blocker_guest, blocked_guest) not found")
		}
		if n := adminQueryInt(t, dsn, indexOn("blocks", "ic.relname = 'blocks_blocked_guest_idx'", "blocked_guest")); n != 1 {
			t.Error("index blocks_blocked_guest_idx on (blocked_guest) not found")
		}
		if n := adminQueryInt(t, dsn, indexOn("restrictions", "ic.relname = 'restrictions_guest_idx'", "guest_id")); n != 1 {
			t.Error("index restrictions_guest_idx on (guest_id) not found")
		}
		if n := adminQueryInt(t, dsn, indexOn("moderation_actions", "ic.relname = 'moderation_actions_at_idx'", "at")); n != 1 {
			t.Error("index moderation_actions_at_idx on (at) not found")
		}
	})

	t.Run("the database refuses a block of oneself", func(t *testing.T) {
		err := adminExecArgs(t, dsn, `INSERT INTO blocks (blocker_guest, blocked_guest, created_at) VALUES ('g', 'g', now())`)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Errorf("error = %v, want a check violation", err)
		}
	})
}

// TestPostgresRestrictionGuardWaitsForTheGuestLock holds the per-guest advisory
// lock in another transaction together with an uncommitted active restriction
// of that guest: CreateRestriction must queue behind the lock (not read around
// it) and, once the holder commits, see the row and refuse. Without the lock
// the insert would sail through unseen.
func TestPostgresRestrictionGuardWaitsForTheGuestLock(t *testing.T) {
	dsn := testDatabaseURL(t)
	p := newTestSafety(t, dsn)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, lockBlockerSQL, "restrict:contested"); err != nil {
		t.Fatal(err)
	}
	held := sampleRestriction(1, func(r *safety.Restriction) { r.GuestID = "contested" })
	if _, err := tx.Exec(ctx, insertRestrictionSQL, held.ID, held.GuestID, nil, held.Reason, nil, held.CreatedAt, nil, nil, held.Operator); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- p.CreateRestriction(ctx, sampleRestriction(2, func(r *safety.Restriction) { r.GuestID = "contested" }),
			sampleAction(2, safety.ActionRestrict, func(a *safety.Action) { a.TargetID = "contested" }))
	}()
	for waiting := 0; waiting == 0; {
		select {
		case err := <-done:
			t.Fatalf("CreateRestriction finished (%v) while another transaction held the guest's lock", err)
		case <-time.After(10 * time.Millisecond):
		}
		waiting = adminQueryInt(t, dsn, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`)
		if ctx.Err() != nil {
			t.Fatal("CreateRestriction never queued behind the guest's lock")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrAlreadyRestricted) {
		t.Errorf("CreateRestriction after the holder committed = %v, want ErrAlreadyRestricted", err)
	}
	if n := adminQueryInt(t, dsn, `SELECT count(*) FROM restrictions WHERE guest_id = 'contested'`); n != 1 {
		t.Errorf("%d restrictions of the guest, want 1", n)
	}
	if n := adminQueryInt(t, dsn, `SELECT count(*) FROM moderation_actions`); n != 0 {
		t.Errorf("%d audit rows, want 0: the refused restriction must leave none", n)
	}
}

func TestPostgresReportsSurviveAccounts(t *testing.T) {
	dsn := testDatabaseURL(t)
	testReportsSurviveAccounts(t, newTestSafety(t, dsn))
}

func TestPostgresSafetySchema(t *testing.T) {
	dsn := testDatabaseURL(t)
	newTestSafety(t, dsn)

	t.Run("no foreign keys: reports survive rooms and accounts", func(t *testing.T) {
		for _, table := range []string{"reports", "terms_acceptances"} {
			if n := adminQueryInt(t, dsn, `SELECT count(*) FROM information_schema.table_constraints
				WHERE table_schema = current_schema() AND table_name = $1 AND constraint_type = 'FOREIGN KEY'`, table); n != 0 {
				t.Errorf("%s has %d foreign keys", table, n)
			}
		}
	})

	// pg_indexes.indexdef is deliberately not used: it formats every index of
	// every schema, and another package's test dropping its private schema at the
	// same moment makes that fail ("could not open relation with OID").
	t.Run("the idempotency pair is unique and the listing index exists", func(t *testing.T) {
		if n := adminQueryInt(t, dsn, indexOn("reports", "ic.relname = 'reports_status_created_idx'", "status,created_at")); n != 1 {
			t.Error("index reports_status_created_idx on (status, created_at) not found")
		}
		if n := adminQueryInt(t, dsn, indexOn("reports", "i.indisunique", "reporter_guest,idempotency_key")); n != 1 {
			t.Error("UNIQUE (reporter_guest, idempotency_key) not found")
		}
		if n := adminQueryInt(t, dsn, indexOn("terms_acceptances", "i.indisprimary", "guest_id,version")); n != 1 {
			t.Error("PRIMARY KEY (guest_id, version) not found")
		}
	})

	t.Run("the database itself refuses an acceptance without the age confirmation", func(t *testing.T) {
		err := adminExecArgs(t, dsn, `INSERT INTO terms_acceptances (guest_id, version, accepted_at, locale, age13_confirmed)
			VALUES ('g', 'v', now(), 'en', false)`)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Errorf("error = %v, want a check violation", err)
		}
	})

	t.Run("jsonb refuses a NUL, which the store therefore never sends", func(t *testing.T) {
		p := mustSafetyPG(t, dsn)
		bad := sampleReport(60)
		bad.Evidence = []byte(`{"text":"a\u0000b"}`)
		if _, _, err := p.CreateReport(t.Context(), bad); !errors.Is(err, ErrInvalidReport) || strings.Contains(err.Error(), "SQLSTATE") {
			t.Errorf("error = %v: the store must refuse it before the database does", err)
		}
	})
}

func mustSafetyPG(t *testing.T, dsn string) *Postgres { return newTestSafety(t, dsn) }

// indexOn counts the indexes of table that satisfy cond (over i = pg_index, ic =
// the index relation) and cover exactly the columns, in that order.
func indexOn(table, cond, columns string) string {
	return `SELECT count(*) FROM pg_index i
		JOIN pg_class ic ON ic.oid = i.indexrelid
		JOIN pg_class c ON c.oid = i.indrelid
		JOIN pg_namespace ns ON ns.oid = c.relnamespace
		WHERE ns.nspname = current_schema() AND c.relname = '` + table + `' AND ` + cond + `
		AND (SELECT string_agg(a.attname::text, ',' ORDER BY k.ord)
		     FROM unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord)
		     JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.attnum) = '` + columns + `'`
}

package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
)

func newTestAccounts(t *testing.T, dsn string) *Postgres {
	t.Helper()
	p := openTestPostgres(t, dsn)
	adminExec(t, dsn, `TRUNCATE room_snapshots, users, reports, terms_acceptances, blocks, restrictions, moderation_actions CASCADE`)
	return p
}

func TestPostgresAccountsContract(t *testing.T) {
	dsn := testDatabaseURL(t)
	testAccountsContract(t, func(t *testing.T) Accounts { return newTestAccounts(t, dsn) })
}

func TestPostgresAccountsSchema(t *testing.T) {
	dsn := testDatabaseURL(t)
	newTestAccounts(t, dsn)

	t.Run("no email, no token, no secret column", func(t *testing.T) {
		got := adminQueryInt(t, dsn, `SELECT count(*) FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name IN ('users', 'user_stats', 'user_games')`)
		// users 8 + user_stats 7 + user_games 7
		if got != 22 {
			t.Errorf("the account tables have %d columns, want 22: a new column needs a privacy review", got)
		}
		if n := adminQueryInt(t, dsn, `SELECT count(*) FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'users'
			AND (column_name ~* 'mail|token|secret|password|picture|locale')`); n != 0 {
			t.Errorf("users has %d columns that look like personal data or credentials", n)
		}
	})

	t.Run("a half-saved profile is impossible", func(t *testing.T) {
		u := mustUser(t, mustAccounts(t, dsn), "sub-half", "Ana")
		for _, stmt := range []string{
			`UPDATE users SET nickname = 'x' WHERE id = $1`,
			`UPDATE users SET avatar_emoji = 'x', avatar_color = 'y' WHERE id = $1`,
		} {
			err := adminExecArgs(t, dsn, stmt, u.ID)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Errorf("%s: error = %v, want a check violation", stmt, err)
			}
		}
	})

	t.Run("foreign keys cascade", func(t *testing.T) {
		a := mustAccounts(t, dsn)
		ana := mustUser(t, a, "sub-cascade", "Ana")
		mustRecord(t, a, rec(1, res(ana, true, 10, 10, 1)))
		count := func(table string) int {
			return adminQueryInt(t, dsn, fmt.Sprintf(`SELECT count(*) FROM %s WHERE user_id = $1`, table), ana.ID)
		}
		if count("user_stats") != 1 || count("user_games") != 1 {
			t.Fatal("the game was not recorded")
		}
		adminExec(t, dsn, fmt.Sprintf(`DELETE FROM users WHERE id = '%s'`, ana.ID))
		if count("user_stats") != 0 || count("user_games") != 0 {
			t.Error("deleting the user row left statistics behind")
		}
	})

	t.Run("a user_games row is only written together with the statistics", func(t *testing.T) {
		a := mustAccounts(t, dsn)
		ana := mustUser(t, a, "sub-atomic", "Ana")
		// A statistic the integer column cannot hold makes the second statement
		// of the transaction fail: nothing of the first may remain.
		huge := rec(1, UserResult{UserID: ana.ID, Won: true, Points: 1, BestRound: 1})
		adminExec(t, dsn, fmt.Sprintf(`INSERT INTO user_stats (user_id, games_played, total_points) VALUES ('%s', 0, 0)`, ana.ID),
			fmt.Sprintf(`UPDATE user_stats SET games_played = 2147483647 WHERE user_id = '%s'`, ana.ID))
		if err := a.RecordGame(t.Context(), huge); err == nil {
			t.Fatal("RecordGame succeeded although games_played overflows")
		}
		if n := adminQueryInt(t, dsn, `SELECT count(*) FROM user_games WHERE user_id = $1`, ana.ID); n != 0 {
			t.Errorf("a failed transaction left %d user_games rows behind", n)
		}
	})
}

func mustAccounts(t *testing.T, dsn string) *Postgres {
	t.Helper()
	return newTestAccounts(t, dsn)
}

func adminExecArgs(t *testing.T, dsn, stmt string, args ...any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p := openTestPostgres(t, dsn)
	_, err := p.pool.Exec(ctx, stmt, args...)
	return err
}

// TestPostgresRecordGameDuringDeleteUser: a user deleted while a game is being
// recorded never leaves rows behind and never makes the recording fail.
func TestPostgresRecordGameDuringDeleteUser(t *testing.T) {
	dsn := testDatabaseURL(t)
	a := newTestAccounts(t, dsn)
	stayer := mustUser(t, a, "sub-stayer", "Stayer")

	const rounds = 20
	for i := range rounds {
		leaver := mustUser(t, a, fmt.Sprintf("sub-leaver-%d", i), "Leaver")
		var wg sync.WaitGroup
		wg.Go(func() {
			if err := a.DeleteUser(context.Background(), leaver.ID); err != nil {
				t.Errorf("DeleteUser: %v", err)
			}
		})
		wg.Go(func() {
			if err := a.RecordGame(context.Background(), rec(i+1, res(leaver, true, 5, 5, 0), res(stayer, false, 5, 5, 0))); err != nil {
				t.Errorf("RecordGame: %v", err)
			}
		})
		wg.Wait()
	}
	if got := mustStats(t, a, stayer.ID); got.GamesPlayed != rounds {
		t.Errorf("stayer = %+v, want %d games (a record concurrent with a deletion was lost)", got, rounds)
	}
	if n := adminQueryInt(t, dsn, `SELECT count(*) FROM user_games WHERE user_id <> $1`, stayer.ID); n != 0 {
		t.Errorf("%d user_games rows of deleted users remain", n)
	}
}

func TestPostgresAccountsSurviveARestart(t *testing.T) {
	dsn := testDatabaseURL(t)
	first := newTestAccounts(t, dsn)
	ana := mustUser(t, first, "sub-ana", "Ana")
	if err := first.SaveProfile(t.Context(), ana.ID, profile("Anita")); err != nil {
		t.Fatal(err)
	}
	mustRecord(t, first, rec(1, res(ana, true, 50, 30, 3)))
	first.Close()

	second := openTestPostgres(t, dsn)
	got, err := second.GetUser(t.Context(), ana.ID)
	if err != nil || got.Profile == nil || got.Profile.Nickname != "Anita" {
		t.Fatalf("GetUser after the restart = %+v, %v", got, err)
	}
	// The game is still known: replaying it does not count twice.
	mustRecord(t, second, rec(1, res(ana, true, 50, 30, 3)))
	if got := mustStats(t, second, ana.ID); got != (api.Stats{GamesPlayed: 1, Wins: 1, TotalPoints: 50, BestRound: 30, UniqueAnswers: 3}) {
		t.Errorf("stats = %+v", got)
	}
	// And the sub still maps to the same user.
	if again := mustUser(t, second, "sub-ana", "Ana"); again.ID != ana.ID {
		t.Errorf("login after the restart got id %s, want %s", again.ID, ana.ID)
	}
}

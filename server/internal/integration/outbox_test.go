package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/auth/authtest"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// What the two-round game of TestFullGameThreePlayersTwoRounds (and
// playSecondRound) gives its players, by hand:
//
//	Ana   25 + 25 = 50 points, best round 25, 4 unique answers, wins
//	Beto   5 + 25 = 30 points, best round 25, 2 unique answers
//	Cami  20 + 20 = 40 points, best round 20, 4 unique answers
var (
	anaTotals  = api.Stats{GamesPlayed: 1, Wins: 1, TotalPoints: 50, BestRound: 25, UniqueAnswers: 4}
	betoTotals = api.Stats{GamesPlayed: 1, Wins: 0, TotalPoints: 30, BestRound: 25, UniqueAnswers: 2}
	camiTotals = api.Stats{GamesPlayed: 1, Wins: 0, TotalPoints: 40, BestRound: 20, UniqueAnswers: 4}
)

// rig is a stack with Ana, Beto and Cami at one table; linked says which of
// them signed in with Google (the others are guests).
type rig struct {
	t     *testing.T
	flaky *flakyBackend
	b     [3]*browser
	me    [3]api.UserInfo
	tb    *table
}

func newRig(t *testing.T, st store.Store, linked [3]bool) *rig {
	t.Helper()
	backend, ok := st.(store.Backend)
	if !ok {
		t.Fatal("the store does not keep accounts")
	}
	r := &rig{t: t, flaky: &flakyBackend{Backend: backend}}
	idp := authtest.New(t, "123-integration.apps.googleusercontent.com", "fake-integration-secret")
	s := newStack(t, stackOpts{store: r.flaky, idp: idp})
	for i, name := range []string{"Ana", "Beto", "Cami"} {
		r.b[i] = s.newBrowser(name)
		if linked[i] {
			r.b[i].googleLogin("/", authtest.User{Sub: fmt.Sprintf("20000000000000000000%d", i), Name: name})
			var status int
			if r.me[i], status = r.b[i].me(); status != 200 {
				t.Fatalf("GET /api/me for %s = %d", name, status)
			}
		}
	}
	r.tb = newTableOf(t, s, 0, r.b[0].p, r.b[1].p, r.b[2].p)
	return r
}

// playToTheLastSummary plays both rounds and leaves the table in the round
// summary of round 2, one step from game over.
func (r *rig) playToTheLastSummary() {
	r.tb.advanceTo(stageSummary)
	r.tb.playSecondRound()
}

// endTheGame is the host's last "next_round": game over.
func (r *rig) endTheGame() {
	r.tb.mustCmd(0, game.CmdNextRound, nil)
	r.tb.waitPhase(game.PhaseFinal)
}

// follow points the browsers at the stack the table talks to now.
func (r *rig) follow() {
	for _, b := range r.b {
		b.use(r.tb.s)
	}
}

func rawSnapshotOf(t *testing.T, st store.Store, code string) (store.Snapshot, bool) {
	t.Helper()
	snaps, err := st.LoadAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, sn := range snaps {
		if sn.Code == code {
			return sn, true
		}
	}
	return store.Snapshot{}, false
}

func (f *flakyBackend) firstAttempt() store.GameRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts[0]
}

// M1. The process dies after the statistics were committed but before the final
// snapshot was durable: the room comes back from the snapshot of the last round
// summary and plays the end of the game again. The game must be recorded under
// the id it already has, and count once.
func TestAGameReplayedAfterACrashCountsOnce(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		r := newRig(t, open(t), [3]bool{true, true, false})
		tb := r.tb
		r.playToTheLastSummary()

		var pre store.Snapshot
		eventually(t, "the snapshot of the last round summary", func() bool {
			g, ok := snapshot(t, tb.s.store, tb.code)
			if !ok || g.Phase != game.PhaseRoundSummary || len(g.History) != 2 {
				return false
			}
			pre, _ = rawSnapshotOf(t, tb.s.store, tb.code)
			return true
		})
		if p := persistedOf(t, tb.s.store, tb.code); p.GameID == "" {
			t.Fatal("the snapshot of a game in progress has no game id")
		}

		r.endTheGame()
		r.b[0].waitStats(anaTotals)
		r.b[1].waitStats(betoTotals)
		first := r.flaky.lastRecord()
		if p := persistedOf(t, tb.s.store, tb.code); p.GameID != first.ID {
			t.Fatalf("the game was recorded as %s but the room says %q", first.ID, p.GameID)
		}

		// The crash: the store only has the snapshot from before the end.
		tb.restartWith(time.Minute, func() {
			pre.UpdatedAt = tb.s.clock.Now()
			if err := r.flaky.Save(context.Background(), pre); err != nil {
				t.Fatal(err)
			}
		})
		r.follow()
		tb.rejoinAll()
		if v := tb.anyView(); v.Phase != game.PhaseRoundSummary || len(v.History) != 2 {
			t.Fatalf("restored in phase %s with %d rounds, want the last round summary", v.Phase, len(v.History))
		}
		r.endTheGame() // the same ending, played again

		eventually(t, "the replayed game to be recorded again", func() bool {
			return tb.s.metricValue("tuttifrutti_stats_recorded_total", map[string]string{"result": "ok"}) == 1
		})
		if a, n := r.flaky.counts(); a != 2 || n != 2 {
			t.Fatalf("attempts %d, committed %d: want the game offered twice", a, n)
		}
		again := r.flaky.lastRecord()
		if again.ID != first.ID || len(again.Results) != len(first.Results) {
			t.Fatalf("the replay recorded %+v, the first time %+v: they must be the same game", again, first)
		}
		if st := r.b[0].stats(); st != anaTotals {
			t.Errorf("Ana's stats after the replay = %+v, want %+v (counted once)", st, anaTotals)
		}
		if st := r.b[1].stats(); st != betoTotals {
			t.Errorf("Beto's stats after the replay = %+v, want %+v (counted once)", st, betoTotals)
		}
	})
}

// M2. The game ends while the statistics store is down; the host starts the
// rematch (which clears the final phase) and the process restarts, still with
// the store down. The finished game lives in the room's snapshot and is recorded
// by the new process once the store is back, exactly once.
func TestAFinishedGameSurvivesARematchAndARestartWhileTheStatsStoreIsDown(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		r := newRig(t, open(t), [3]bool{true, false, true})
		tb := r.tb
		r.flaky.setError(errors.New("database is down"))
		r.playToTheLastSummary()
		r.endTheGame()
		eventually(t, "the first failed attempt", func() bool { a, _ := r.flaky.counts(); return a >= 1 })
		first := r.flaky.firstAttempt()

		tb.mustCmd(0, game.CmdRematch, nil) // the game is not recorded and its final phase is gone
		tb.waitPhase(game.PhaseLobby)
		eventually(t, "the lobby snapshot to carry the unrecorded game", func() bool {
			p := persistedOf(t, tb.s.store, tb.code)
			return p.GameID == "" && len(p.Pending) == 1 && p.Pending[0].ID == first.ID
		})

		tb.restart(30 * time.Second) // the store is still down
		r.follow()
		r.flaky.setError(nil)
		r.b[0].waitStats(anaTotals)
		r.b[2].waitStats(camiTotals)

		eventually(t, "the outbox of the snapshot to empty", func() bool {
			return len(persistedOf(t, tb.s.store, tb.code).Pending) == 0
		})
		r.flaky.mu.Lock()
		defer r.flaky.mu.Unlock()
		if len(r.flaky.recorded) != 1 || r.flaky.recorded[0].ID != first.ID {
			t.Errorf("committed %+v, want the game %s exactly once", r.flaky.recorded, first.ID)
		}
		if got := r.flaky.recorded[0]; got.Results[0].UserID != r.me[0].ID || got.Results[1].UserID != r.me[2].ID || len(got.Results) != 2 {
			t.Errorf("record = %+v, want Ana and Cami (Beto is a guest)", got)
		}
	})
}

// M3. Whoever holds a player's guest cookie (the bearer credential of the seat)
// can take the seat over; signing in as somebody else, or signing out, must not
// move the credit of the game, whether the takeover happens while round 1 is
// still being played or after it was scored. The thief keeps the seat to the end
// of the game, so the link at game over is the thief's.
func TestAStolenGuestCookieCannotMoveTheCreditOfAPlayedRound(t *testing.T) {
	for _, tc := range []struct {
		name        string
		withSession bool
		when        stage
	}{
		{"as another account during round 1", true, stagePlaying},
		{"signed out during round 1", false, stagePlaying},
		{"as another account after round 1 was scored", true, stageSummary},
		{"signed out after round 1 was scored", false, stageSummary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
				r := newRig(t, open(t), [3]bool{true, false, false})
				tb := r.tb
				mallory := tb.s.newBrowser("Mallory")
				mallory.googleLogin("/", authtest.User{Sub: "300000000000000000099", Name: "Mallory"})
				malloryMe, status := mallory.me()
				if status != 200 {
					t.Fatalf("Mallory GET /api/me = %d", status)
				}

				tb.advanceTo(tc.when) // Ana is linked to her own account and has played
				cookie := "tf_guest=" + r.b[0].cookie("tf_guest").Value
				if tc.withSession {
					cookie += "; tf_session=" + mallory.cookie("tf_session").Value
				}
				thief := &player{name: "Ana", avatar: r.b[0].p.avatar, id: r.b[0].p.id, cookie: cookie}
				c := tb.s.connect(thief)
				tb.clients = append(tb.clients, c)
				c.join(tb.code)
				tb.seats[0].c = c // the thief plays Ana's seat, host included, to the end

				tb.advanceTo(stageSummary)
				tb.playSecondRound()
				r.endTheGame()
				r.b[0].waitStats(anaTotals)
				rec := r.flaky.lastRecord()
				for _, res := range rec.Results {
					if res.UserID == malloryMe.ID {
						t.Errorf("Mallory got credit for Ana's game: %+v", res)
					}
				}
				if len(rec.Results) != 1 || rec.Results[0].UserID != r.me[0].ID || !rec.Results[0].Won {
					t.Errorf("record = %+v, want Ana only (Beto and Cami are guests)", rec)
				}
				if st := mallory.stats(); st != (api.Stats{}) {
					t.Errorf("Mallory's stats = %+v: she played nothing", st)
				}
			})
		})
	}
}

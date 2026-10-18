package room

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

func counterOf(t testing.TB, e *env, name string, labels ...string) float64 {
	t.Helper()
	return metricValue(t, e.reg, name, labels...)
}

func gamesFinished(t testing.TB, e *env, reason string) float64 {
	t.Helper()
	return counterOf(t, e, "tuttifrutti_games_finished_total", "reason", reason)
}

// playRound plays the round startRound began (letter A) up to its summary.
func (l *lobby3) playRound() {
	l.e.t.Helper()
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	l.send(0, game.CmdFinishReview, nil)
}

func sorted(v []string) []string {
	out := slices.Clone(v)
	slices.Sort(out)
	return out
}

// ---- games ----

func TestFinishReasonIsToldFromTheState(t *testing.T) {
	all := func(g *game.Game) []string {
		return game.AvailableLetters(g.Settings.Lang, g.Settings.ExcludeHardLetters, nil)
	}
	tests := []struct {
		name string
		mod  func(g *game.Game)
		want string
	}{
		{"every configured round was played", func(g *game.Game) { g.Settings.Rounds, g.RoundNum = 3, 3 }, finishCompleted},
		{"the last round, ended by hand", func(g *game.Game) { g.Settings.Rounds, g.RoundNum = 1, 1 }, finishCompleted},
		{"rounds and letters ran out together", func(g *game.Game) {
			g.Settings.Rounds, g.RoundNum, g.UsedLetters = 2, 2, all(g)
		}, finishCompleted},
		{"no letter left, unlimited rounds", func(g *game.Game) {
			g.Settings.Rounds, g.RoundNum, g.UsedLetters = 0, 20, all(g)
		}, finishLettersExhausted},
		{"no letter left before the configured rounds", func(g *game.Game) {
			g.Settings.Rounds, g.RoundNum, g.UsedLetters = 5, 2, all(g)
		}, finishLettersExhausted},
		{"no letter left in Dutch", func(g *game.Game) {
			g.Settings.Lang, g.Settings.Rounds, g.UsedLetters = game.LangNL, 0, game.AvailableLetters(game.LangNL, g.Settings.ExcludeHardLetters, nil)
		}, finishLettersExhausted},
		{"host ended early, rounds left", func(g *game.Game) {
			g.Settings.Rounds, g.RoundNum, g.UsedLetters = 5, 2, []string{"A", "B"}
		}, finishEndedByHost},
		{"host ended early, unlimited rounds", func(g *game.Game) {
			g.Settings.Rounds, g.RoundNum, g.UsedLetters = 0, 2, []string{"A", "B"}
		}, finishEndedByHost},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := game.NewGame(game.Player{ID: "h", Name: "Host", Avatar: testAvatar}, t0)
			tt.mod(g)
			if got := finishReason(g); got != tt.want {
				t.Errorf("finishReason = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGamesStartedAndFinished(t *testing.T) {
	t.Run("a game that plays every round is completed", func(t *testing.T) {
		l := newLobby3(t)
		if got := counterOf(t, l.e, "tuttifrutti_games_started_total"); got != 0 {
			t.Fatalf("started = %v before any game", got)
		}
		finishGame(t, l)
		if got := counterOf(t, l.e, "tuttifrutti_games_started_total"); got != 1 {
			t.Errorf("started = %v, want 1", got)
		}
		for reason, want := range map[string]float64{finishCompleted: 1, finishEndedByHost: 0, finishLettersExhausted: 0} {
			if got := gamesFinished(t, l.e, reason); got != want {
				t.Errorf("finished{%s} = %v, want %v", reason, got, want)
			}
		}
	})

	t.Run("end_game on the last round is completed too", func(t *testing.T) {
		l := newLobby3(t)
		l.startRound(nil) // one round
		l.playRound()
		l.send(0, game.CmdEndGame, nil)
		if got := gamesFinished(t, l.e, finishCompleted); got != 1 {
			t.Errorf("finished{completed} = %v, want 1 (the web client ends the last round with end_game)", got)
		}
	})

	t.Run("the host ends the game early", func(t *testing.T) {
		l := newLobby3(t)
		l.startRound(func(s *game.Settings) { s.Rounds = 3 })
		l.playRound()
		l.send(0, game.CmdEndGame, nil)
		for reason, want := range map[string]float64{finishCompleted: 0, finishEndedByHost: 1, finishLettersExhausted: 0} {
			if got := gamesFinished(t, l.e, reason); got != want {
				t.Errorf("finished{%s} = %v, want %v", reason, got, want)
			}
		}
	})

	t.Run("the letters run out", func(t *testing.T) {
		l := newLobby3(t)
		l.startRound(func(s *game.Settings) { s.Rounds = 0 })
		l.playRound()
		query(l.e, l.code, func(r *room) struct{} {
			r.g.UsedLetters = game.AvailableLetters(r.g.Settings.Lang, r.g.Settings.ExcludeHardLetters, nil)
			return struct{}{}
		})
		l.send(0, game.CmdNextRound, nil)
		for reason, want := range map[string]float64{finishCompleted: 0, finishEndedByHost: 0, finishLettersExhausted: 1} {
			if got := gamesFinished(t, l.e, reason); got != want {
				t.Errorf("finished{%s} = %v, want %v", reason, got, want)
			}
		}
	})

	t.Run("a rejected command and a game in progress count nothing", func(t *testing.T) {
		l := newLobby3(t)
		l.startRound(func(s *game.Settings) { s.Rounds = 3 })
		l.send(1, game.CmdEndGame, nil) // not the host, not the summary
		for _, reason := range finishReasons {
			if got := gamesFinished(t, l.e, reason); got != 0 {
				t.Errorf("finished{%s} = %v while the game is running", reason, got)
			}
		}
		if got := counterOf(t, l.e, "tuttifrutti_games_started_total"); got != 1 {
			t.Errorf("started = %v, want 1", got)
		}
	})

	t.Run("a rematch starts another game", func(t *testing.T) {
		l := newLobby3(t)
		finishGame(t, l)
		l.send(0, game.CmdRematch, nil)
		finishGame(t, l)
		if got := counterOf(t, l.e, "tuttifrutti_games_started_total"); got != 2 {
			t.Errorf("started = %v, want 2", got)
		}
		if got := gamesFinished(t, l.e, finishCompleted); got != 2 {
			t.Errorf("finished{completed} = %v, want 2", got)
		}
	})
}

func TestTimeUpsAreCountedWhenTheLimitExpires(t *testing.T) {
	l := newLobby3(t)
	l.startRound(func(s *game.Settings) { s.TimeLimitSeconds = 60; s.Rounds = 3 })
	l.send(0, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Ana"})

	l.e.advance(60*time.Second - time.Millisecond)
	if got := counterOf(t, l.e, "tuttifrutti_time_ups_total"); got != 0 {
		t.Fatalf("time_ups = %v before the limit", got)
	}
	l.e.advance(time.Millisecond)
	if got := counterOf(t, l.e, "tuttifrutti_time_ups_total"); got != 1 {
		t.Errorf("time_ups = %v, want 1", got)
	}
	l.e.advance(game.LateUpdateAllowance) // the grace period ending is not another time-up
	if got := counterOf(t, l.e, "tuttifrutti_time_ups_total"); got != 1 {
		t.Errorf("time_ups = %v after the review began, want still 1", got)
	}
	if got := counterOf(t, l.e, "tuttifrutti_basta_total"); got != 0 {
		t.Errorf("basta = %v: a time-up is not a Basta", got)
	}
	if got := counterOf(t, l.e, "tuttifrutti_letter_auto_stops_total"); got != 0 {
		t.Errorf("letter_auto_stops = %v: the chooser tapped", got)
	}
}

func TestABastaIsNotATimeUp(t *testing.T) {
	l := newLobby3(t)
	l.startRound(func(s *game.Settings) { s.TimeLimitSeconds = 60 })
	l.playRound()
	l.e.advance(2 * time.Minute)
	if got := counterOf(t, l.e, "tuttifrutti_time_ups_total"); got != 0 {
		t.Errorf("time_ups = %v, want 0 (the round ended by Basta)", got)
	}
	if got := counterOf(t, l.e, "tuttifrutti_basta_total"); got != 1 {
		t.Errorf("basta = %v, want 1", got)
	}
}

func TestLetterAutoStopsAreCountedWhenTheChooserDoesNotTap(t *testing.T) {
	l := newLobby3(t)
	l.send(0, game.CmdStartGame, nil)
	l.e.advance(game.ChooserTimeout - time.Millisecond)
	if got := counterOf(t, l.e, "tuttifrutti_letter_auto_stops_total"); got != 0 {
		t.Fatalf("letter_auto_stops = %v before the deadline", got)
	}
	l.e.advance(time.Millisecond)
	if got := counterOf(t, l.e, "tuttifrutti_letter_auto_stops_total"); got != 1 {
		t.Errorf("letter_auto_stops = %v, want 1", got)
	}
	if got := counterOf(t, l.e, "tuttifrutti_time_ups_total"); got != 0 {
		t.Errorf("time_ups = %v", got)
	}
}

func TestATappedLetterIsNotAnAutoStop(t *testing.T) {
	l := newLobby3(t)
	l.startRound(nil)
	if got := counterOf(t, l.e, "tuttifrutti_letter_auto_stops_total"); got != 0 {
		t.Errorf("letter_auto_stops = %v after the chooser tapped", got)
	}
	// The tap does not leave a mark that hides a later auto-stop.
	l.playRound()
	l.send(0, game.CmdEndGame, nil)
	l.send(0, game.CmdRematch, nil)
	l.send(0, game.CmdUpdateSettings, threeCats(nil))
	l.send(0, game.CmdStartGame, nil)
	l.e.advance(game.ChooserTimeout)
	if got := counterOf(t, l.e, "tuttifrutti_letter_auto_stops_total"); got != 1 {
		t.Errorf("letter_auto_stops = %v, want 1 after the second game's chooser let the time run out", got)
	}
}

// A tap that arrives after the deadline but before the room's timer ran is
// refused, and the auto-stop the engine applied first is counted once.
func TestALateTapCountsTheAutoStopOnce(t *testing.T) {
	l := newLobby3(t)
	l.send(0, game.CmdStartGame, nil)
	query(l.e, l.code, func(r *room) struct{} { r.stopWake(); return struct{}{} })
	l.e.clock.Set(t0.Add(game.ChooserTimeout + time.Second))
	l.send(0, game.CmdStopLetter, nil)
	if got := counterOf(t, l.e, "tuttifrutti_letter_auto_stops_total"); got != 1 {
		t.Errorf("letter_auto_stops = %v, want 1", got)
	}
	if got := counterOf(t, l.e, "tuttifrutti_commands_total", "type", "stop_letter", "result", "wrong_phase"); got != 1 {
		t.Errorf("stop_letter wrong_phase = %v, want 1", got)
	}
}

// ---- snapshots ----

func TestSnapshotSaveMetrics(t *testing.T) {
	e := newEnv(t)
	if n := seriesCount(t, e.reg, "tuttifrutti_snapshot_last_success_timestamp_seconds"); n != 1 {
		t.Fatalf("the last-success gauge is not exported before the first save")
	}
	if got := counterOf(t, e, "tuttifrutti_snapshot_last_success_timestamp_seconds"); got != 0 {
		t.Fatalf("last success = %v before any save, want 0", got)
	}
	e.store.update(func(f *faultStore) { f.saveTook = 250 * time.Millisecond })

	code := e.create("p1", "Ana")
	e.waitSaved(code)
	saves := float64(e.store.saveCount(code))
	if saves < 1 {
		t.Fatal("setup: nothing was saved")
	}
	if got := counterOf(t, e, "tuttifrutti_snapshot_saves_total", "result", "ok"); got != saves {
		t.Errorf("saves{ok} = %v, want %v", got, saves)
	}
	if got := counterOf(t, e, "tuttifrutti_snapshot_saves_total", "result", "error"); got != 0 {
		t.Errorf("saves{error} = %v, want 0", got)
	}
	count, sum := histogramOf(t, e.reg, "tuttifrutti_snapshot_save_duration_seconds")
	if float64(count) != saves || math.Abs(sum-0.25*saves) > 1e-9 {
		t.Errorf("duration histogram: %d observations summing %v, want %v of 0.25 s", count, sum, saves)
	}
	if got, want := counterOf(t, e, "tuttifrutti_snapshot_last_success_timestamp_seconds"), float64(e.clock.Now().UnixNano())/1e9; got != want {
		t.Errorf("last success = %v, want the time the last save finished: %v", got, want)
	}
	if got := counterOf(t, e, "tuttifrutti_snapshot_save_queue"); got != 0 {
		t.Errorf("save queue = %v with everything written", got)
	}
}

func TestSnapshotBucketsSuitADatabaseWrite(t *testing.T) {
	e := newEnv(t)
	e.waitSaved(e.create("p1", "Ana"))
	got := bucketBounds(t, e.reg, "tuttifrutti_snapshot_save_duration_seconds")
	if len(got) < 2 || got[0] != 0.001 || got[len(got)-1] != 5 {
		t.Errorf("buckets = %v, want 1 ms ... 5 s", got)
	}
}

func TestFailedSnapshotWritesAreCountedAndDoNotMoveTheLastSuccess(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", "Ana")
	e.join(code, "p1", "Ana")
	e.waitSaved(code)
	okBefore := counterOf(t, e, "tuttifrutti_snapshot_saves_total", "result", "ok")
	lastOK := counterOf(t, e, "tuttifrutti_snapshot_last_success_timestamp_seconds")
	if lastOK == 0 {
		t.Fatal("setup: no successful save yet")
	}
	n := e.store.saveCount(code)
	e.store.update(func(f *faultStore) { f.failSaves = 2; f.saveTook = 40 * time.Millisecond })

	e.clock.Advance(time.Minute) // time passes, so a wrongly moved gauge would show
	e.join(code, "p2", "Beto")
	eventually(t, "the first failed attempt", func() bool { return e.store.saveCount(code) == n+1 })
	eventually(t, "the saver to back off", func() bool { return e.otherTimers() == 1 })

	if got := counterOf(t, e, "tuttifrutti_snapshot_saves_total", "result", "error"); got != 1 {
		t.Errorf("saves{error} = %v, want 1", got)
	}
	if got := counterOf(t, e, "tuttifrutti_snapshot_saves_total", "result", "ok"); got != okBefore {
		t.Errorf("saves{ok} = %v, want it unchanged at %v", got, okBefore)
	}
	if got := counterOf(t, e, "tuttifrutti_snapshot_last_success_timestamp_seconds"); got != lastOK {
		t.Errorf("last success = %v after a failed write, want it unchanged at %v", got, lastOK)
	}
	if got := counterOf(t, e, "tuttifrutti_snapshot_save_queue"); got != 1 {
		t.Errorf("save queue = %v while a snapshot waits for its retry, want 1", got)
	}
	count, sum := histogramOf(t, e.reg, "tuttifrutti_snapshot_save_duration_seconds")
	if float64(count) != okBefore+1 || sum < 0.04 {
		t.Errorf("a failed write must be observed too: %d observations, sum %v", count, sum)
	}

	e.clock.Advance(e.hub.minBackoff) // the second attempt fails as well
	eventually(t, "the second failed attempt", func() bool { return e.store.saveCount(code) == n+2 })
	eventually(t, "the saver to back off again", func() bool { return e.otherTimers() == 1 })
	if got := counterOf(t, e, "tuttifrutti_snapshot_saves_total", "result", "error"); got != 2 {
		t.Errorf("saves{error} = %v, want 2 (each attempt counts)", got)
	}
	if got := counterOf(t, e, "tuttifrutti_snapshot_save_queue"); got != 1 {
		t.Errorf("save queue = %v while retrying, want 1 (one room, not one per attempt)", got)
	}

	e.clock.Advance(2 * e.hub.minBackoff)
	e.waitSaved(code)
	if got := counterOf(t, e, "tuttifrutti_snapshot_saves_total", "result", "ok"); got != okBefore+1 {
		t.Errorf("saves{ok} = %v, want %v", got, okBefore+1)
	}
	if got := counterOf(t, e, "tuttifrutti_snapshot_last_success_timestamp_seconds"); got <= lastOK {
		t.Errorf("last success = %v after the retry succeeded, want more than %v", got, lastOK)
	}
	if got := counterOf(t, e, "tuttifrutti_snapshot_save_queue"); got != 0 {
		t.Errorf("save queue = %v after the retry succeeded", got)
	}
}

func TestSnapshotQueueCountsRoomsNotSnapshots(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.CodeSource = scriptReader(0, 0, 0, 0, 1, 1, 1, 1) })
	gate := make(chan struct{})
	e.store.update(func(f *faultStore) { f.gate = gate })

	a := e.create("a", "A")
	b := e.create("b", "B")
	e.join(a, "a", "A")
	e.join(a, "x", "X") // a newer snapshot replaces the one waiting
	eventually(t, "both rooms to wait for the store", func() bool { return counterOf(t, e, "tuttifrutti_snapshot_save_queue") == 2 })
	close(gate)
	e.waitSaved(a)
	e.waitSaved(b)
	eventually(t, "the queue to drain", func() bool { return counterOf(t, e, "tuttifrutti_snapshot_save_queue") == 0 })
}

// An encoding failure cannot be provoked through a real game, so the metric
// helper is checked directly: it is a failed save and a persistence error.
func TestAnEncodingFailureIsASaveError(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newMetrics(reg, newTestLogger(&logBuf{}))
	m.snapshotEncodeFailed()
	if got := metricValue(t, reg, "tuttifrutti_snapshot_saves_total", "result", "error"); got != 1 {
		t.Errorf("saves{error} = %v, want 1", got)
	}
	if got := metricValue(t, reg, "tuttifrutti_snapshot_errors_total"); got != 1 {
		t.Errorf("snapshot_errors = %v, want 1", got)
	}
	if got := metricValue(t, reg, "tuttifrutti_snapshot_saves_total", "result", "ok"); got != 0 {
		t.Errorf("saves{ok} = %v, want 0", got)
	}
	if count, _ := histogramOf(t, reg, "tuttifrutti_snapshot_save_duration_seconds"); count != 0 {
		t.Errorf("an encoding failure is not a write: %d observations", count)
	}
}

func TestSnapshotsDeletedBySource(t *testing.T) {
	t.Run("a closed room removes its own", func(t *testing.T) {
		e := newEnv(t, func(c *Config) { c.CodeSource = scriptReader(0, 0, 0, 0) })
		code := e.create("a", "A")
		e.waitSaved(code)
		e.store.update(func(f *faultStore) { f.failDeletes = 2 })
		e.advance(defaultUnclaimedTTL)
		eventually(t, "the first failed deletion", func() bool { return e.store.deleteCount(code) == 1 })
		eventually(t, "the saver to back off", func() bool { return e.otherTimers() == 1 })
		if got := counterOf(t, e, "tuttifrutti_snapshots_deleted_total", "source", "room_closed"); got != 0 {
			t.Errorf("room_closed = %v after a failed deletion", got)
		}
		e.clock.Advance(e.hub.minBackoff)
		eventually(t, "the second failed deletion", func() bool { return e.store.deleteCount(code) == 2 })
		eventually(t, "the saver to back off again", func() bool { return e.otherTimers() == 1 })
		e.clock.Advance(2 * e.hub.minBackoff)
		eventually(t, "the snapshot to be deleted", func() bool { return !e.store.has(code) })
		eventually(t, "the deletion to be counted", func() bool {
			return counterOf(t, e, "tuttifrutti_snapshots_deleted_total", "source", "room_closed") == 1
		})
		if got := counterOf(t, e, "tuttifrutti_snapshots_deleted_total", "source", "janitor"); got != 0 {
			t.Errorf("janitor = %v, nothing was swept", got)
		}
	})

	t.Run("the janitor sweeps stale rows", func(t *testing.T) {
		e := newEnv(t, func(c *Config) { c.JanitorEvery = time.Hour; c.SnapshotMaxAge = 6 * time.Hour })
		ctx := context.Background()
		if _, err := e.hub.Start(ctx); err != nil {
			t.Fatal(err)
		}
		for _, code := range []string{"ZZZZ", "XXXX", "WWWW"} {
			if err := e.store.Memory.Save(ctx, store.Snapshot{Code: code, State: []byte(`{}`), UpdatedAt: t0.Add(-7 * time.Hour)}); err != nil {
				t.Fatal(err)
			}
		}
		e.store.update(func(f *faultStore) { f.failJanitor = true })
		e.clock.Advance(time.Hour)
		eventually(t, "a failing sweep", func() bool { return counterOf(t, e, "tuttifrutti_snapshot_errors_total") == 1 })
		if got := counterOf(t, e, "tuttifrutti_snapshots_deleted_total", "source", "janitor"); got != 0 {
			t.Errorf("janitor = %v after a failing sweep", got)
		}
		eventually(t, "the janitor to re-arm", func() bool { return e.clock.Pending() == 1 })
		e.store.update(func(f *faultStore) { f.failJanitor = false })
		e.clock.Advance(time.Hour)
		eventually(t, "the sweep to count the rows it removed", func() bool {
			return counterOf(t, e, "tuttifrutti_snapshots_deleted_total", "source", "janitor") == 3
		})
		if got := counterOf(t, e, "tuttifrutti_snapshots_deleted_total", "source", "room_closed"); got != 0 {
			t.Errorf("room_closed = %v, no room closed", got)
		}
	})
}

// ---- restore ----

func TestRestoreMetrics(t *testing.T) {
	e := newEnv(t)
	save := func(code string, state []byte) {
		t.Helper()
		if err := e.store.Memory.Save(context.Background(), store.Snapshot{Code: code, State: state, UpdatedAt: t0}); err != nil {
			t.Fatal(err)
		}
	}
	save("HHHH", newPersistedGame(t, "HHHH", func(*game.Game) {}))
	save("JJJJ", newPersistedGame(t, "JJJJ", func(*game.Game) {}))
	save("BBBB", []byte(`"not an object"`))
	save("CCCC", newPersistedGame(t, "CCCC", func(g *game.Game) { g.Phase = "bogus" }))
	e.store.update(func(f *faultStore) { f.loadTook = 1500 * time.Millisecond })

	reg := prometheus.NewRegistry()
	h := e.newHubOn(func(c *Config) { c.Registerer = reg })
	for _, name := range []string{"tuttifrutti_rooms_restored_total", "tuttifrutti_restore_duration_seconds", "tuttifrutti_restore_errors_total"} {
		if seriesCount(t, reg, name) != 1 || metricValue(t, reg, name) != 0 {
			t.Fatalf("%s must exist and be 0 before the restore", name)
		}
	}
	n, err := h.Start(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("Start = %d, %v; want the two good rooms", n, err)
	}
	if got := metricValue(t, reg, "tuttifrutti_rooms_restored_total"); got != 2 {
		t.Errorf("rooms_restored = %v, want 2", got)
	}
	if got := metricValue(t, reg, "tuttifrutti_restore_errors_total"); got != 2 {
		t.Errorf("restore_errors = %v, want 2 (the unusable snapshots)", got)
	}
	if got := metricValue(t, reg, "tuttifrutti_restore_duration_seconds"); got != 1.5 {
		t.Errorf("restore_duration = %v, want 1.5 s", got)
	}
}

func TestRestoreCountsTheRoomsBeyondTheCapAsErrors(t *testing.T) {
	e := newEnv(t)
	for _, code := range []string{"HHHH", "JJJJ", "KKKK"} {
		if err := e.store.Memory.Save(context.Background(), store.Snapshot{Code: code, State: newPersistedGame(t, code, func(*game.Game) {}), UpdatedAt: t0}); err != nil {
			t.Fatal(err)
		}
	}
	reg := prometheus.NewRegistry()
	h := e.newHubOn(func(c *Config) { c.Registerer = reg; c.MaxRooms = 1 })
	if n, err := h.Start(context.Background()); err != nil || n != 1 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	if got := metricValue(t, reg, "tuttifrutti_rooms_restored_total"); got != 1 {
		t.Errorf("rooms_restored = %v, want 1", got)
	}
	if got := metricValue(t, reg, "tuttifrutti_restore_errors_total"); got != 2 {
		t.Errorf("restore_errors = %v, want 2 (left in the store)", got)
	}
}

func TestRestoreOfAnEmptyStoreIsAllZero(t *testing.T) {
	e := newEnv(t)
	reg := prometheus.NewRegistry()
	h := e.newHubOn(func(c *Config) { c.Registerer = reg })
	if n, err := h.Start(context.Background()); err != nil || n != 0 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	for _, name := range []string{"tuttifrutti_rooms_restored_total", "tuttifrutti_restore_duration_seconds", "tuttifrutti_restore_errors_total"} {
		if got := metricValue(t, reg, name); got != 0 {
			t.Errorf("%s = %v, want 0", name, got)
		}
	}
}

// ---- statistics backlog ----

func TestStatsQueueAndOutboxGauges(t *testing.T) {
	rec := &fakeRecorder{gate: make(chan struct{})}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	gauge := func(name string) float64 { return counterOf(t, l.e, name) }
	if gauge("tuttifrutti_stats_queue_depth") != 0 || gauge("tuttifrutti_stats_outbox_pending") != 0 {
		t.Fatal("the backlog gauges must start at 0")
	}

	finishGame(t, l)
	eventually(t, "the first game to be in the outbox and with the recorder", func() bool {
		attempts, _ := rec.counts()
		return attempts == 1 && gauge("tuttifrutti_stats_outbox_pending") == 1
	})
	if got := gauge("tuttifrutti_stats_queue_depth"); got != 0 {
		t.Errorf("queue depth = %v, want 0 (the recorder took the game it is working on)", got)
	}

	l.send(0, game.CmdRematch, nil)
	finishGame(t, l)
	eventually(t, "the second game to queue up behind the first", func() bool {
		return gauge("tuttifrutti_stats_queue_depth") == 1 && gauge("tuttifrutti_stats_outbox_pending") == 2
	})

	close(rec.gate)
	waitDone(t, rec, 2)
	eventually(t, "the backlog to drain", func() bool {
		return gauge("tuttifrutti_stats_queue_depth") == 0 && gauge("tuttifrutti_stats_outbox_pending") == 0
	})
}

func TestTheOutboxGaugeFollowsARestoredRoomAndAClosedOne(t *testing.T) {
	rec := &fakeRecorder{fail: 1000}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	finishGame(t, l)
	eventually(t, "the game in the outbox", func() bool { return counterOf(t, l.e, "tuttifrutti_stats_outbox_pending") == 1 })
	l.e.waitSaved(l.code)

	// A restart: the next process finds the record in the snapshot.
	e2 := restartedEnv(t, l.e, withRecorder(&fakeRecorder{fail: 1000}))
	if got := counterOf(t, e2, "tuttifrutti_stats_outbox_pending"); got != 1 {
		t.Errorf("outbox after the restore = %v, want 1", got)
	}

	// Everybody leaves: the room closes and its records are the recorder's.
	l2 := &lobby3{e: e2, code: l.code}
	for i, name := range []string{"Ana", "Beto", "Cami"} {
		l2.c[i], l2.s[i] = e2.joinAs(l.code, []string{"p1", "p2", "p3"}[i], name, "")
	}
	for i := range l2.s {
		l2.send(i, game.CmdLeave, nil)
	}
	eventually(t, "the closed room to leave the outbox", func() bool { return counterOf(t, e2, "tuttifrutti_stats_outbox_pending") == 0 })
}

// ---- label sets ----

func TestNewMetricsHaveFixedLabelSets(t *testing.T) {
	l := newLobby3(t)
	finishGame(t, l)
	l.send(0, game.CmdRematch, nil)
	l.send(0, game.CmdUpdateSettings, threeCats(func(s *game.Settings) { s.Rounds = 3 }))
	l.send(0, game.CmdStartGame, nil)
	l.e.advance(game.ChooserTimeout)
	l.e.waitSaved(l.code)

	want := map[string]struct {
		label  string
		values []string
	}{
		"tuttifrutti_games_finished_total":    {"reason", []string{"completed", "ended_by_host", "letters_exhausted"}},
		"tuttifrutti_snapshot_saves_total":    {"result", []string{"error", "ok"}},
		"tuttifrutti_snapshots_deleted_total": {"source", []string{"janitor", "room_closed"}},
	}
	for name, w := range want {
		if got := sorted(labelValuesOf(t, l.e.reg, name, w.label)); !slices.Equal(got, sorted(w.values)) {
			t.Errorf("%s{%s} = %v, want exactly %v", name, w.label, got, sorted(w.values))
		}
	}
	for _, name := range []string{
		"tuttifrutti_games_started_total", "tuttifrutti_time_ups_total", "tuttifrutti_letter_auto_stops_total",
		"tuttifrutti_snapshot_save_duration_seconds", "tuttifrutti_snapshot_last_success_timestamp_seconds",
		"tuttifrutti_snapshot_save_queue", "tuttifrutti_rooms_restored_total", "tuttifrutti_restore_duration_seconds",
		"tuttifrutti_restore_errors_total", "tuttifrutti_stats_queue_depth", "tuttifrutti_stats_outbox_pending",
	} {
		if n := seriesCount(t, l.e.reg, name); n != 1 {
			t.Errorf("%s has %d series, want exactly 1 (no labels)", name, n)
		}
	}
}

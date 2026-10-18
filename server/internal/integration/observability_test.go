package integration

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// Metric names the observability tests read.
const (
	mStarted    = "tuttifrutti_games_started_total"
	mFinished   = "tuttifrutti_games_finished_total"
	mTimeUps    = "tuttifrutti_time_ups_total"
	mAutoStops  = "tuttifrutti_letter_auto_stops_total"
	mSaves      = "tuttifrutti_snapshot_saves_total"
	mSaveSecs   = "tuttifrutti_snapshot_save_duration_seconds"
	mLastOK     = "tuttifrutti_snapshot_last_success_timestamp_seconds"
	mSaveQueue  = "tuttifrutti_snapshot_save_queue"
	mDeleted    = "tuttifrutti_snapshots_deleted_total"
	mRestored   = "tuttifrutti_rooms_restored_total"
	mRestoreSec = "tuttifrutti_restore_duration_seconds"
	mRestoreErr = "tuttifrutti_restore_errors_total"
	mSendDrops  = "tuttifrutti_ws_send_drops_total"
)

func finishedBy(s *stack, reason string) float64 {
	return s.metricValue(mFinished, map[string]string{"reason": reason})
}

// playToSummary plays round 1 and leaves the table at its round summary.
func playToSummary(t *testing.T, tb *table, mod func(*game.Settings)) {
	t.Helper()
	s := roundSettings()
	if mod != nil {
		mod(&s)
	}
	tb.configure(s)
	tb.stage = stageLobby
	tb.advanceTo(stageSummary)
}

// TestGameMetricsAfterAGame: a game is counted when it starts and, when it
// ends, once under the reason that tells how.
func TestGameMetricsAfterAGame(t *testing.T) {
	reasons := []string{"completed", "ended_by_host", "letters_exhausted"}
	want := func(t *testing.T, s *stack, started float64, got map[string]float64) {
		t.Helper()
		if v := s.metricValue(mStarted, nil); v != started {
			t.Errorf("games_started_total = %v, want %v", v, started)
		}
		for _, r := range reasons {
			if v := finishedBy(s, r); v != got[r] {
				t.Errorf("games_finished_total{reason=%q} = %v, want %v", r, v, got[r])
			}
		}
	}

	t.Run("the host ends the game after round 1 of 2", func(t *testing.T) {
		s := newStack(t, stackOpts{})
		tb := newTable(t, s, "Ana", "Beto", "Cami")
		want(t, s, 0, nil)
		playToSummary(t, tb, nil)
		want(t, s, 1, nil) // started, not finished
		tb.mustCmd(0, game.CmdEndGame, nil)
		tb.waitPhase(game.PhaseFinal)
		want(t, s, 1, map[string]float64{"ended_by_host": 1})

		tb.mustCmd(0, game.CmdRematch, nil)
		tb.waitPhase(game.PhaseLobby)
		want(t, s, 1, map[string]float64{"ended_by_host": 1}) // a rematch is not a start
		tb.start()
		want(t, s, 2, map[string]float64{"ended_by_host": 1})
	})

	t.Run("every configured round was played", func(t *testing.T) {
		s := newStack(t, stackOpts{})
		tb := newTable(t, s, "Ana", "Beto", "Cami")
		playToSummary(t, tb, func(g *game.Settings) { g.Rounds = 1 })
		tb.mustCmd(0, game.CmdNextRound, nil) // next_round and end_game both finish the last round
		tb.waitPhase(game.PhaseFinal)
		want(t, s, 1, map[string]float64{"completed": 1})
	})

	t.Run("end_game on the last round is completed as well", func(t *testing.T) {
		s := newStack(t, stackOpts{})
		tb := newTable(t, s, "Ana", "Beto", "Cami")
		playToSummary(t, tb, func(g *game.Settings) { g.Rounds = 1 })
		tb.mustCmd(0, game.CmdEndGame, nil)
		tb.waitPhase(game.PhaseFinal)
		want(t, s, 1, map[string]float64{"completed": 1})
	})
}

// TestTimeUpAndLetterAutoStopMetrics: a letter the server picked because the
// chooser did not tap, and a round the clock ended, are counted when they
// happen; a tap and a Basta are not.
func TestTimeUpAndLetterAutoStopMetrics(t *testing.T) {
	t.Run("the chooser lets the 10 s run out, then the time limit ends the round", func(t *testing.T) {
		s := newStack(t, stackOpts{})
		tb := newTable(t, s, "Ana", "Beto", "Cami")
		tb.configure(func() game.Settings { r := roundSettings(); r.TimeLimitSeconds = 60; return r }())
		tb.stage = stageLobby
		tb.advanceTo(stageLetter)

		s.advance(chooserFor - time.Millisecond)
		if got := s.metricValue(mAutoStops, nil); got != 0 {
			t.Fatalf("letter_auto_stops_total = %v before the 10 s", got)
		}
		s.advance(time.Millisecond)
		tb.waitPhase(game.PhasePlaying)
		eventually(t, "the auto-stop to be counted", func() bool { return s.metricValue(mAutoStops, nil) == 1 })

		s.advance(59 * time.Second)
		if got := s.metricValue(mTimeUps, nil); got != 0 {
			t.Fatalf("time_ups_total = %v before the limit", got)
		}
		s.advance(time.Second)
		tb.waitPhase(game.PhaseGrace)
		eventually(t, "the time-up to be counted", func() bool { return s.metricValue(mTimeUps, nil) == 1 })
		s.advance(lateFor)
		tb.waitPhase(game.PhaseReview)
		if got := s.metricValue(mTimeUps, nil); got != 1 {
			t.Errorf("time_ups_total = %v after the review began, want 1", got)
		}
		if got := s.metricValue("tuttifrutti_basta_total", nil); got != 0 {
			t.Errorf("basta_total = %v: nobody called Basta", got)
		}
	})

	t.Run("a tap and a Basta count neither", func(t *testing.T) {
		s := newStack(t, stackOpts{})
		tb := newTable(t, s, "Ana", "Beto", "Cami")
		tb.advanceTo(stageReview) // the chooser tapped, Ana called Basta
		if got := s.metricValue(mAutoStops, nil); got != 0 {
			t.Errorf("letter_auto_stops_total = %v after a tap", got)
		}
		if got := s.metricValue(mTimeUps, nil); got != 0 {
			t.Errorf("time_ups_total = %v after a Basta", got)
		}
		if got := s.metricValue("tuttifrutti_basta_total", nil); got != 1 {
			t.Errorf("basta_total = %v, want 1", got)
		}
	})
}

// faultySnapshots is a store whose Save can be made to fail, and to take time
// on the stack's ManualClock.
type faultySnapshots struct {
	store.Store
	clock *room.ManualClock

	mu    sync.Mutex
	fail  bool
	took  time.Duration
	saves int
}

func (f *faultySnapshots) setFail(v bool) {
	f.mu.Lock()
	f.fail = v
	f.mu.Unlock()
}

func (f *faultySnapshots) setTook(d time.Duration) {
	f.mu.Lock()
	f.took = d
	f.mu.Unlock()
}

func (f *faultySnapshots) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saves
}

func (f *faultySnapshots) Save(ctx context.Context, sn store.Snapshot) error {
	f.mu.Lock()
	f.saves++
	fail, took := f.fail, f.took
	f.mu.Unlock()
	if took > 0 {
		f.clock.Set(f.clock.Now().Add(took))
	}
	if fail {
		return errors.New("database is down")
	}
	return f.Store.Save(ctx, sn)
}

// TestSnapshotMetricsFollowTheStore plays with a store that goes down and
// comes back: the save counters, the duration histogram, the queue and the
// last-success timestamp tell what the store did, as /metrics exposes them.
func TestSnapshotMetricsFollowTheStore(t *testing.T) {
	clk := room.NewManualClock(epoch)
	fs := &faultySnapshots{Store: store.NewMemory(), clock: clk}
	s := newStack(t, stackOpts{store: fs, clock: clk})
	saves := func(result string) float64 { return s.metricValue(mSaves, map[string]string{"result": result}) }
	unix := func(at time.Time) float64 { return float64(at.UnixNano()) / 1e9 }

	if got := s.metricValue(mLastOK, nil); got != 0 {
		t.Fatalf("last success = %v before the first save, want 0", got)
	}

	fs.setTook(20 * time.Millisecond)
	tb := newTable(t, s, "Ana", "Beto")
	tb.configure(roundSettings())
	eventually(t, "every save to be counted", func() bool {
		return saves("ok") == float64(fs.count()) && s.metricValue(mSaveQueue, nil) == 0
	})
	if saves("error") != 0 {
		t.Fatalf("saves{error} = %v with a healthy store", saves("error"))
	}
	healthy := s.metricValue(mLastOK, nil)
	if healthy == 0 || healthy != unix(clk.Now()) {
		t.Fatalf("last success = %v, want the time of the last save %v", healthy, unix(clk.Now()))
	}
	text := s.scrape()
	if got := promSeries(text, mSaveSecs+"_count")[""]; got != "" && got != strconv.Itoa(fs.count()) {
		t.Errorf("duration observations = %q, want %d", got, fs.count())
	}
	if len(promSeries(text, mSaveSecs+"_bucket")) < 10 {
		t.Errorf("the duration histogram is not exported with buckets:\n%v", promSeries(text, mSaveSecs+"_bucket"))
	}

	// The database goes down: the saver retries with backoff, nothing succeeds
	// and the timestamp stays where it was while the clock moves on.
	t.Cleanup(func() { fs.setFail(false) }) // the shutdown flushes a final snapshot
	fs.setFail(true)
	okBefore := saves("ok")
	tb.mustCmd(0, game.CmdUpdateSettings, game.UpdateSettings{Settings: roundSettings()})
	eventually(t, "a failed save", func() bool { return saves("error") >= 1 })
	// Each retry waits for the clock (a backoff of at most 10 s).
	eventually(t, "the failed attempts to pile up", func() bool {
		s.advance(10 * time.Second)
		return saves("error") >= 3
	})
	if got := saves("ok"); got != okBefore {
		t.Errorf("saves{ok} = %v while the store is down, want %v", got, okBefore)
	}
	if got := s.metricValue(mLastOK, nil); got != healthy {
		t.Errorf("last success = %v while the store is down, want it frozen at %v", got, healthy)
	}
	if got := s.metricValue(mSaveQueue, nil); got != 1 {
		t.Errorf("save queue = %v: one room is waiting for the store, want 1", got)
	}
	if got := s.metricValue("tuttifrutti_snapshot_errors_total", nil); got < 3 {
		t.Errorf("snapshot_errors_total = %v, want at least the failed saves", got)
	}

	// It comes back: the retry succeeds, the queue empties and the timestamp moves.
	fs.setFail(false)
	eventually(t, "the store to take the snapshot again", func() bool {
		s.advance(10 * time.Second)
		return saves("ok") > okBefore && s.metricValue(mSaveQueue, nil) == 0
	})
	if got := s.metricValue(mLastOK, nil); got <= healthy || got > unix(clk.Now()) {
		t.Errorf("last success = %v after the recovery, want within (%v, %v]", got, healthy, unix(clk.Now()))
	}
}

// TestRestoreMetricsAfterARestart: the next process counts the rooms it
// brought back and the snapshots it could not, and starts the last-success
// timestamp from zero until it has saved something.
func TestRestoreMetricsAfterARestart(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		testRestoreMetrics(t, open(t))
	})
}

func testRestoreMetrics(t *testing.T, st store.Store) {
	s := newStack(t, stackOpts{store: st})
	if got := s.metricValue(mRestored, nil); got != 0 {
		t.Fatalf("rooms_restored_total = %v on a fresh store", got)
	}
	if _, ok := s.metric(mRestoreSec, nil); !ok {
		t.Fatal("restore_duration_seconds is not exported")
	}
	tb := newTable(t, s, "Ana", "Beto", "Cami")
	tb.advanceTo(stageGrace)

	tb.restartWith(30*time.Second, func() {
		if err := s.store.Save(context.Background(), store.Snapshot{Code: "ZZZZ", State: []byte(`"junk"`), UpdatedAt: s.clock.Now()}); err != nil {
			t.Fatal(err)
		}
	})
	next := tb.s
	if got := next.metricValue(mRestored, nil); got != 1 {
		t.Errorf("rooms_restored_total = %v, want the one room", got)
	}
	if got := next.metricValue(mRestoreErr, nil); got != 1 {
		t.Errorf("restore_errors_total = %v, want the one junk snapshot", got)
	}
	if got := next.metricValue(mRestoreSec, nil); got < 0 {
		t.Errorf("restore_duration_seconds = %v", got)
	}
	// The restored room saves itself as it starts: the timestamp is set again.
	eventually(t, "the restored room to save", func() bool {
		return next.metricValue(mLastOK, nil) == float64(next.clock.Now().UnixNano())/1e9 && next.metricValue(mSaveQueue, nil) == 0
	})
	if got := next.metricValue(mSaves, map[string]string{"result": "error"}); got != 0 {
		t.Errorf("saves{error} = %v after the restart", got)
	}
	// The first process' counters did not leak into the second one.
	if got := next.metricValue(mFinished, map[string]string{"reason": "completed"}); got != 0 {
		t.Errorf("games_finished_total = %v in the new process", got)
	}
}

// TestSnapshotDeletionMetrics: the janitor's sweeps and the snapshots rooms
// delete when they close are counted apart.
func TestSnapshotDeletionMetrics(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		testSnapshotDeletionMetrics(t, open(t))
	})
}

func testSnapshotDeletionMetrics(t *testing.T, st store.Store) {
	s := newStack(t, stackOpts{store: st})
	deleted := func(source string) float64 { return s.metricValue(mDeleted, map[string]string{"source": source}) }

	stale := epoch.Add(-7 * time.Hour)
	for _, code := range []string{"ZZZZ", "XXXX"} {
		if err := s.store.Save(context.Background(), store.Snapshot{Code: code, State: []byte(`{}`), UpdatedAt: stale}); err != nil {
			t.Fatal(err)
		}
	}
	s.advance(time.Hour) // the first sweep
	eventually(t, "the janitor to count what it swept", func() bool { return deleted("janitor") == 2 })
	if deleted("room_closed") != 0 {
		t.Fatalf("room_closed = %v, no room closed", deleted("room_closed"))
	}

	p := s.newPlayer("Ana")
	code := s.createRoom(p, "")
	eventually(t, "the first snapshot", func() bool { return hasSnapshot(t, s.store, code) })
	s.advance(unclaimedTTL)
	expectGone(t, s, code)
	eventually(t, "the closed room's deletion to be counted", func() bool { return deleted("room_closed") == 1 })
	if deleted("janitor") != 2 {
		t.Errorf("janitor = %v, want 2", deleted("janitor"))
	}
}

// TestNewMetricsAreExposedWithFixedLabels reads /metrics like Prometheus after
// a whole game and checks that every new series is there and that the labelled
// ones only carry the documented values.
func TestNewMetricsAreExposedWithFixedLabels(t *testing.T) {
	s := newStack(t, stackOpts{})
	tb := newTable(t, s, "Ana", "Beto", "Cami")
	playToSummary(t, tb, nil)
	tb.mustCmd(0, game.CmdEndGame, nil)
	tb.waitPhase(game.PhaseFinal)

	text := s.scrape()
	for _, name := range []string{
		mStarted, mFinished, mTimeUps, mAutoStops, mSaves, mSaveSecs, mLastOK, mSaveQueue, mDeleted, mRestored,
		mRestoreSec, mRestoreErr, mSendDrops, "tuttifrutti_stats_queue_depth", "tuttifrutti_stats_outbox_pending",
	} {
		if !strings.Contains(text, "# TYPE "+name+" ") {
			t.Errorf("/metrics has no %s", name)
		}
	}
	for name, want := range map[string][]string{
		mFinished: {"completed", "ended_by_host", "letters_exhausted"},
		mSaves:    {"error", "ok"},
		mDeleted:  {"janitor", "room_closed"},
	} {
		label := map[string]string{mFinished: "reason", mSaves: "result", mDeleted: "source"}[name]
		got := map[string]bool{}
		for block := range promSeries(text, name) {
			got[labelsOf(block)[label]] = true
		}
		if len(got) != len(want) {
			t.Errorf("%s{%s} values = %v, want exactly %v", name, label, got, want)
		}
		for _, v := range want {
			if !got[v] {
				t.Errorf("%s{%s=%q} is missing", name, label, v)
			}
		}
	}
}

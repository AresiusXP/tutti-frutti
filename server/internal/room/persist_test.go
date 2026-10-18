package room

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// ---- what triggers a snapshot ----

func TestSnapshotTriggers(t *testing.T) {
	l := newLobby3(t)
	e, code := l.e, l.code

	stored := func() (Persisted, *game.Game) {
		e.waitSaved(code)
		return e.stored(code)
	}
	saves := func() int { return e.store.saveCount(code) }

	p, g := stored()
	if p.Version != persistedVersion || p.Code != code || !p.CreatedAt.Equal(t0) {
		t.Errorf("header = %+v", p)
	}
	if g.Phase != game.PhaseLobby || len(g.Players) != 3 {
		t.Errorf("after the joins: phase %s, %d players", g.Phase, len(g.Players))
	}
	if !p.LastActive.Equal(t0) {
		t.Errorf("lastActive = %v, want now while clients are connected", p.LastActive)
	}

	l.send(0, game.CmdUpdateSettings, threeCats(nil))
	if _, g := stored(); g.Settings.Rounds != 1 || len(g.Settings.Categories) != 3 {
		t.Errorf("after update_settings: %+v", g.Settings)
	}
	l.send(0, game.CmdStartGame, nil)
	if _, g := stored(); g.Phase != game.PhaseLetter || g.RoundNum != 1 {
		t.Errorf("after start_game: phase %s round %d", g.Phase, g.RoundNum)
	}
	l.send(0, game.CmdStopLetter, nil)
	if _, g := stored(); g.Phase != game.PhasePlaying || g.Round.Letter != "A" {
		t.Errorf("after the letter: phase %s letter %q", g.Phase, g.Round.Letter)
	}

	// Answers do not trigger a snapshot by themselves.
	before := saves()
	l.fillAnswers()
	e.sync(code)
	if got := saves(); got != before {
		t.Errorf("answers caused %d snapshots, want none until the next trigger", got-before)
	}
	if _, g := stored(); g.Round.Answers["p1"][0] != "" {
		t.Errorf("stored answers = %v, want the old state", g.Round.Answers["p1"])
	}

	l.send(0, game.CmdBasta, nil)
	if _, g := stored(); g.Phase != game.PhaseGrace || g.Round.Answers["p1"][0] != "Ana" {
		t.Errorf("after basta: phase %s answers %v", g.Phase, g.Round.Answers["p1"])
	}
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	if _, g := stored(); g.Phase != game.PhaseReview {
		t.Errorf("after the lock: phase %s", g.Phase)
	}
	l.send(0, game.CmdFinishReview, nil)
	if _, g := stored(); g.Phase != game.PhaseRoundSummary || g.Players[0].TotalScore == 0 {
		t.Errorf("after finish_review: phase %s score %d", g.Phase, g.Players[0].TotalScore)
	}
	l.send(0, game.CmdNextRound, nil)
	if _, g := stored(); g.Phase != game.PhaseFinal || len(g.Winners) == 0 {
		t.Errorf("after the last round: phase %s winners %v", g.Phase, g.Winners)
	}
	l.send(0, game.CmdRematch, nil)
	if _, g := stored(); g.Phase != game.PhaseLobby || g.Players[0].TotalScore != 0 {
		t.Errorf("after the rematch: phase %s", g.Phase)
	}
	l.send(0, game.CmdKick, game.Kick{PlayerID: "p3"})
	if _, g := stored(); len(g.Players) != 2 || len(g.Banned) != 1 {
		t.Errorf("after the kick: %d players, banned %v", len(g.Players), g.Banned)
	}
	l.send(1, game.CmdLeave, nil)
	if _, g := stored(); len(g.Players) != 1 {
		t.Errorf("after the leave: %d players", len(g.Players))
	}
}

func TestSnapshotRecordsLastActiveOfAnEmptyRoom(t *testing.T) {
	e := newEnv(t)
	code := e.create("h", "Host")
	_, s := e.join(code, "h", "Host")
	e.advance(10 * time.Minute)
	s.Disconnect()
	e.sync(code)
	e.advance(5 * time.Minute)
	// Any trigger saves the room; nobody is connected, so lastActive stays at
	// the time the last client left.
	r := e.room(code)
	r.post(func(r *room) { r.persist(r.clock.Now()) }, nil)
	e.waitSaved(code)
	p, _ := e.stored(code)
	if want := t0.Add(10 * time.Minute); !p.LastActive.Equal(want) {
		t.Errorf("lastActive = %v, want %v", p.LastActive.Sub(t0), want.Sub(t0))
	}
}

func TestPeriodicSnapshotWhilePlaying(t *testing.T) {
	l := newLobby3(t)
	e, code := l.e, l.code

	// Lobby: nothing periodic.
	e.waitSaved(code)
	n := e.store.saveCount(code)
	e.advance(time.Minute)
	e.waitSaved(code)
	if got := e.store.saveCount(code); got != n {
		t.Fatalf("the lobby was saved %d times in a minute", got-n)
	}

	l.startRound(nil)
	e.waitSaved(code)
	n = e.store.saveCount(code)

	l.send(1, game.CmdSetAnswer, game.SetAnswer{Category: 1, Text: "Austria"})
	e.advance(defaultSnapshotEvery - time.Millisecond)
	e.waitSaved(code)
	if got := e.store.saveCount(code); got != n {
		t.Fatalf("saved %d times before SnapshotEvery elapsed", got-n)
	}
	e.advance(time.Millisecond)
	e.waitSaved(code)
	if got := e.store.saveCount(code); got != n+1 {
		t.Fatalf("saves after SnapshotEvery = %d, want %d", got, n+1)
	}
	if _, g := e.stored(code); g.Round.Answers["p2"][1] != "Austria" {
		t.Errorf("the periodic snapshot missed the answer: %v", g.Round.Answers["p2"])
	}

	// And again, every SnapshotEvery, even when nothing changed.
	for i := 2; i <= 4; i++ {
		e.advance(defaultSnapshotEvery)
		e.waitSaved(code)
		if got := e.store.saveCount(code); got != n+i {
			t.Fatalf("after %d periods: %d saves, want %d", i, got, n+i)
		}
	}
}

func TestPeriodicSnapshotInGraceToo(t *testing.T) {
	l := newLobby3(t, func(c *Config) { c.SnapshotEvery = time.Second })
	l.startRound(nil)
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.waitSaved(l.code)
	n := l.e.store.saveCount(l.code)
	l.e.advance(time.Second)
	l.e.waitSaved(l.code)
	if got := l.e.store.saveCount(l.code); got != n+1 {
		t.Errorf("grace: saves = %d, want %d", got, n+1)
	}
}

func TestReviewIsSavedOnlyWhenSomethingChanged(t *testing.T) {
	l := newLobby3(t)
	e, code := l.e, l.code
	l.startRound(nil)
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	e.advance(game.GraceDuration + game.LateUpdateAllowance)
	e.waitSaved(code)
	n := e.store.saveCount(code)

	e.advance(time.Minute) // idle review: no snapshots
	e.waitSaved(code)
	if got := e.store.saveCount(code); got != n {
		t.Fatalf("an unchanged review was saved %d times", got-n)
	}

	l.send(1, game.CmdVoteReject, game.VoteReject{Category: 0, Target: "p1"})
	e.advance(defaultSnapshotEvery)
	e.waitSaved(code)
	if got := e.store.saveCount(code); got != n+1 {
		t.Fatalf("saves after a vote = %d, want %d", got, n+1)
	}
	if _, g := e.stored(code); len(g.Round.RejectVotes) != 1 {
		t.Errorf("the vote was not in the snapshot: %v", g.Round.RejectVotes)
	}
	e.advance(time.Minute)
	e.waitSaved(code)
	if got := e.store.saveCount(code); got != n+1 {
		t.Errorf("a quiet review kept being saved: %d", got-n-1)
	}
}

// ---- the saver ----

func TestSaverIsLatestWins(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", "Ana")
	_, host := e.join(code, "p1", "Ana")
	e.waitSaved(code)
	n := e.store.saveCount(code)

	// Hold the store: the first snapshot is in flight while more changes pile up.
	gate := make(chan struct{})
	e.store.update(func(f *faultStore) { f.gate = gate })
	e.join(code, "p2", "Beto")
	eventually(t, "a write to be in flight", func() bool { return e.store.saveCount(code) == n+1 })

	e.join(code, "p3", "Cami")
	e.join(code, "p4", "Dani")
	for range 20 {
		e.handle(code, host, game.CmdUpdateSettings, threeCats(nil))
	}
	if got := e.store.saveCount(code); got != n+1 {
		t.Fatalf("writes while the store was held = %d, want the one in flight", got-n)
	}
	close(gate)
	e.waitSaved(code)

	// The in-flight write and one write of the newest state: nothing in between.
	if got := e.store.saveCount(code); got != n+2 {
		t.Errorf("writes = %d, want 2 (in flight + latest)", got-n)
	}
	_, g := e.stored(code)
	if len(g.Players) != 4 || len(g.Settings.Categories) != 3 {
		t.Errorf("stored state is not the newest: %d players, %d categories", len(g.Players), len(g.Settings.Categories))
	}
}

func TestSaverRetriesWithBackoff(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", "Ana")
	e.join(code, "p1", "Ana")
	e.waitSaved(code)
	n := e.store.saveCount(code)
	e.store.update(func(f *faultStore) { f.failSaves = 2 })

	e.join(code, "p2", "Beto")
	eventually(t, "the first failed attempt", func() bool { return e.store.saveCount(code) == n+1 })
	eventually(t, "the saver to back off", func() bool { return e.otherTimers() == 1 })
	if got := metricValue(t, e.reg, "tuttifrutti_snapshot_errors_total"); got != 1 {
		t.Errorf("snapshot errors = %v, want 1", got)
	}

	// While it waits, a newer state arrives: the retry carries it.
	e.join(code, "p3", "Cami")
	e.clock.Advance(e.hub.minBackoff - time.Millisecond)
	if got := e.store.saveCount(code); got != n+1 {
		t.Fatalf("retried before the backoff elapsed")
	}
	e.clock.Advance(time.Millisecond)
	eventually(t, "the second failed attempt", func() bool { return e.store.saveCount(code) == n+2 })
	// The backoff doubled.
	eventually(t, "the saver to back off again", func() bool { return e.otherTimers() == 1 })
	e.clock.Advance(e.hub.minBackoff)
	if got := e.store.saveCount(code); got != n+2 {
		t.Fatalf("the second backoff was not longer than the first")
	}
	e.clock.Advance(e.hub.minBackoff)
	e.waitSaved(code)
	if got := e.store.saveCount(code); got != n+3 {
		t.Errorf("attempts = %d, want 3 (2 failures + 1 success)", got-n)
	}
	if _, g := e.stored(code); len(g.Players) != 3 {
		t.Errorf("stored %d players, want the newest state (3)", len(g.Players))
	}
	if got := metricValue(t, e.reg, "tuttifrutti_snapshot_errors_total"); got != 2 {
		t.Errorf("snapshot errors = %v, want 2", got)
	}
	if !strings.Contains(e.logs.String(), "saving room snapshot failed") {
		t.Error("the failure was not logged")
	}
}

func TestSaverBackoffIsCapped(t *testing.T) {
	e := newEnv(t)
	e.hub.minBackoff, e.hub.maxBackoff = time.Second, 2*time.Second
	code := e.create("p1", "Ana")
	e.join(code, "p1", "Ana")
	e.waitSaved(code)
	n := e.store.saveCount(code)
	e.store.update(func(f *faultStore) { f.failSaves = 100 })
	e.join(code, "p2", "Beto")

	attempts := n + 1
	for _, wait := range []time.Duration{time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second} {
		eventually(t, "an attempt", func() bool { return e.store.saveCount(code) == attempts })
		eventually(t, "the saver to back off", func() bool { return e.otherTimers() == 1 })
		e.clock.Advance(wait - time.Millisecond)
		if e.store.saveCount(code) != attempts {
			t.Fatalf("attempt %d came earlier than %v", attempts, wait)
		}
		e.clock.Advance(time.Millisecond)
		attempts++
	}

	// Let it succeed so that Shutdown's final flush does not wait for a dead store.
	eventually(t, "the last attempt", func() bool { return e.store.saveCount(code) == attempts })
	eventually(t, "the saver to back off", func() bool { return e.otherTimers() == 1 })
	e.store.update(func(f *faultStore) { f.failSaves = 0 })
	e.clock.Advance(e.hub.maxBackoff)
	e.waitSaved(code)
}

func TestSaverNeverBlocksTheRoomWhenTheStoreHangs(t *testing.T) {
	l := newLobby3(t)
	l.e.store.update(func(f *faultStore) { f.hang = true })

	// A whole round: every phase change triggers a snapshot that never lands.
	l.startRound(nil)
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	l.send(0, game.CmdFinishReview, nil)
	v := lastState(t, l.c[1].drain()).View
	if v.Phase != game.PhaseRoundSummary {
		t.Fatalf("phase = %s: the room stopped making progress", v.Phase)
	}

	// Shutdown gives up at its deadline instead of waiting for the store, but
	// still closes every client.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := l.e.hub.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Shutdown = %v, want DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Shutdown took %v with a hung store", d)
	}
	// The clients are closed even though Shutdown gave up waiting (the close
	// tasks are already queued).
	for i, c := range l.c {
		eventually(t, "the client to be closed", func() bool { return len(c.closeCodes()) > 0 })
		if got := c.closeCodes(); len(got) != 1 || got[0] != protocol.CloseServiceRestart {
			t.Errorf("p%d closes = %v, want [%d]", i+1, got, protocol.CloseServiceRestart)
		}
	}
	// The hung writes were aborted: the goroutine check in the cleanup passes.
}

func TestSnapshotDeletionRetriesAndReservesTheCode(t *testing.T) {
	e := newEnv(t, func(c *Config) {
		c.CodeSource = scriptReader(0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2)
	})
	code := e.create("a", "A")
	if code != "BBBB" {
		t.Fatalf("code = %q", code)
	}
	e.waitSaved(code)
	e.store.update(func(f *faultStore) { f.failDeletes = 2 })

	e.advance(defaultUnclaimedTTL) // the room closes; deleting its snapshot fails
	eventually(t, "the first failed deletion", func() bool { return e.store.deleteCount(code) == 1 })
	eventually(t, "the saver to back off", func() bool { return e.otherTimers() == 1 })
	if !e.store.has(code) {
		t.Fatal("the snapshot vanished although the deletion failed")
	}

	// The code is still reserved: BBBB is skipped.
	if got := e.create("b", "B"); got != "CCCC" {
		t.Errorf("a new room got %q, want CCCC while BBBB's snapshot is being deleted", got)
	}

	e.clock.Advance(e.hub.minBackoff)
	eventually(t, "the second failed deletion", func() bool { return e.store.deleteCount(code) == 2 })
	eventually(t, "the saver to back off again", func() bool { return e.clock.Pending() == 2 }) // + the new room's timer
	e.clock.Advance(2 * e.hub.minBackoff)
	eventually(t, "the snapshot to be deleted", func() bool { return !e.store.has(code) })
	if got := e.store.deleteCount(code); got != 3 {
		t.Errorf("delete attempts = %d, want 3", got)
	}
	if got := metricValue(t, e.reg, "tuttifrutti_snapshot_errors_total"); got != 2 {
		t.Errorf("snapshot errors = %v, want 2", got)
	}
	// Once the deletion is done, the code is free again.
	eventually(t, "the code to be released", func() bool {
		e.hub.mu.Lock()
		defer e.hub.mu.Unlock()
		return len(e.hub.retiring) == 0
	})
}

func TestSaveAfterTheRoomClosedDoesNotResurrectIt(t *testing.T) {
	e := newEnv(t)
	code := e.create("a", "A")
	e.waitSaved(code)
	r := e.room(code)
	// Hold the store, queue a snapshot, then close the room.
	gate := make(chan struct{})
	e.store.update(func(f *faultStore) { f.gate = gate })
	r.post(func(r *room) { r.persist(r.clock.Now()) }, nil)
	eventually(t, "a write in flight", func() bool { return e.store.saveCount(code) == 2 })
	r.post(func(r *room) { r.persist(r.clock.Now()) }, nil) // queued behind it
	r.post(func(r *room) { r.closeRoom(protocol.CloseRoomClosed, "test", true) }, nil)
	eventually(t, "the room to close", func() bool { _, ok := e.hub.Info(code); return !ok })
	close(gate)
	eventually(t, "the snapshot to be deleted", func() bool { return !e.store.has(code) })
	if got := e.store.saveCount(code); got != 2 {
		t.Errorf("the queued snapshot of a closed room was written (%d saves)", got)
	}
}

// ---- restore ----

func TestRestoreMidRound(t *testing.T) {
	l := newLobby3(t)
	e, code := l.e, l.code
	l.startRound(nil)
	l.send(1, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Almeja"})
	l.send(0, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Ana"})

	// A restart: the final snapshot is what the next process finds.
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, g := e.stored(code); !g.Players[0].Connected {
		t.Fatal("setup: the final snapshot should still say who was connected")
	}

	e.hub = e.newHubOn()
	n, err := e.hub.Start(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("Start = %d, %v; want 1 room restored", n, err)
	}
	info, ok := e.hub.Info(code)
	if !ok || info.Phase != game.PhasePlaying || info.Players != 3 {
		t.Fatalf("Info = %+v, %v", info, ok)
	}
	connected := query(e, code, func(r *room) []bool {
		var out []bool
		for _, p := range r.g.Players {
			out = append(out, p.Connected)
		}
		return out
	})
	if len(connected) != 3 || connected[0] || connected[1] || connected[2] {
		t.Fatalf("restored seats connected = %v, want all disconnected", connected)
	}
	if st := e.hub.Stats(); st != (Stats{Rooms: 1}) {
		t.Errorf("Stats = %+v", st)
	}

	// The players reconnect and get their state back.
	c2, _ := e.join(code, "p2", "Beto")
	frames := c2.drain()
	if got := kinds(frames); len(got) != 1 || got[0] != "state" {
		t.Fatalf("frames = %v", got)
	}
	v := lastState(t, frames).View
	if v.Phase != game.PhasePlaying || v.Round.Letter != "A" {
		t.Errorf("restored view: phase %s letter %q", v.Phase, v.Round.Letter)
	}
	if got := v.Round.Answers["p2"]; len(got) == 0 || got[0] != "Almeja" {
		t.Errorf("p2's answers after the restart = %v", got)
	}
	if !playerView(t, v, "p2").Connected || playerView(t, v, "p1").Connected || playerView(t, v, "p3").Connected {
		t.Errorf("connected flags after one reconnect: %+v", v.Players)
	}
	if v.Round.Filled["p1"] != 1 {
		t.Errorf("filled = %v, want p1's answer counted", v.Round.Filled)
	}
	// p1 comes back too and sees only its own answers.
	c1, _ := e.join(code, "p1", "Ana")
	v1 := lastState(t, c1.drain()).View
	if got := v1.Round.Answers["p1"]; len(got) == 0 || got[0] != "Ana" {
		t.Errorf("p1's answers = %v", got)
	}
	if _, ok := v1.Round.Answers["p2"]; ok {
		t.Error("a restored room leaks answers")
	}
	if v := lastState(t, c2.drain()).View; !playerView(t, v, "p1").Connected {
		t.Error("p2 was not told that p1 is back")
	}
}

func TestRestoredLetterPhaseAutoStops(t *testing.T) {
	l := newLobby3(t)
	e, code := l.e, l.code
	l.send(0, game.CmdUpdateSettings, threeCats(nil))
	l.send(0, game.CmdStartGame, nil)
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	e.hub = e.newHubOn()
	if n, err := e.hub.Start(context.Background()); err != nil || n != 1 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	if info, _ := e.hub.Info(code); info.Phase != game.PhaseLetter {
		t.Fatalf("restored phase = %s", info.Phase)
	}
	if got := query(e, code, func(r *room) time.Time { return r.wakeAt }); !got.Equal(t0.Add(game.ChooserTimeout)) {
		t.Fatalf("the restored room waits for %v, want the letter deadline", got.Sub(t0))
	}

	e.advance(game.ChooserTimeout)
	info, _ := e.hub.Info(code)
	if info.Phase != game.PhasePlaying {
		t.Fatalf("phase after the chooser deadline = %s, want playing", info.Phase)
	}
	// The change is persisted again.
	e.waitSaved(code)
	if _, g := e.stored(code); g.Phase != game.PhasePlaying || g.Round.Letter == "" {
		t.Errorf("stored phase %s letter %q", g.Phase, g.Round.Letter)
	}
}

func TestRestoreAfterLongDowntimeCatchesUp(t *testing.T) {
	l := newLobby3(t)
	e, code := l.e, l.code
	l.send(0, game.CmdUpdateSettings, threeCats(nil))
	l.send(0, game.CmdStartGame, nil)
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(20 * time.Minute) // the server was down

	e.hub = e.newHubOn()
	if n, err := e.hub.Start(context.Background()); err != nil || n != 1 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	info, ok := e.hub.Info(code)
	if !ok || info.Phase != game.PhasePlaying {
		t.Fatalf("Info = %+v, %v; the overdue chooser deadline should have been applied", info, ok)
	}
	// lastActive restarts at the restore, so the room is not reaped at once.
	la := query(e, code, func(r *room) time.Time { return r.lastActive })
	if !la.Equal(e.clock.Now()) {
		t.Errorf("lastActive = %v, want the restore time %v", la, e.clock.Now())
	}
	e.advance(defaultIdleTTL - time.Second)
	if _, ok := e.hub.Info(code); !ok {
		t.Fatal("the restored room was reaped before its idle TTL")
	}
	e.advance(time.Second)
	if _, ok := e.hub.Info(code); ok {
		t.Fatal("the restored room outlived its idle TTL")
	}
	eventually(t, "the snapshot to be deleted", func() bool { return !e.store.has(code) })
}

func TestRestoreSkipsAndDeletesUnusableSnapshots(t *testing.T) {
	e := newEnv(t)
	good := newPersistedGame(t, "HHHH", func(g *game.Game) {})
	badPhase := newPersistedGame(t, "FFFF", func(g *game.Game) { g.Phase = "bogus" })
	noRound := newPersistedGame(t, "GGGG", func(g *game.Game) { g.Phase = game.PhasePlaying })
	noPlayers := newPersistedGame(t, "JJJJ", func(g *game.Game) { g.Players = nil })
	badLang := newPersistedGame(t, "KKKK", func(g *game.Game) { g.Settings.Lang = "klingon" })

	unknownVersion := mustJSON(t, Persisted{Version: 99, Code: "CCCC", Game: json.RawMessage(`{}`)})
	wrongCode := mustJSON(t, Persisted{Version: persistedVersion, Code: "LLLL", Game: json.RawMessage(`{}`)})

	save := func(code string, state []byte) {
		t.Helper()
		if err := e.store.Memory.Save(context.Background(), store.Snapshot{Code: code, State: state, UpdatedAt: t0}); err != nil {
			t.Fatal(err)
		}
	}
	save("BBBB", []byte(`"not an object"`))
	save("CCCC", unknownVersion)
	save("DDDD", wrongCode)
	save("FFFF", badPhase)
	save("GGGG", noRound)
	save("HHHH", good)
	save("JJJJ", noPlayers)
	save("KKKK", badLang)
	save("MMMM", []byte(`{"version":1,"code":"MMMM","game":"x"}`))
	e.store.update(func(f *faultStore) { f.failDeletes = 1 }) // one deletion fails: still not fatal

	n, err := e.hub.Start(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("Start = %d, %v; want exactly the good room", n, err)
	}
	if _, ok := e.hub.Info("HHHH"); !ok {
		t.Error("the good snapshot was not restored")
	}
	for _, bad := range []string{"BBBB", "CCCC", "DDDD", "FFFF", "GGGG", "JJJJ", "KKKK", "MMMM"} {
		if _, ok := e.hub.Info(bad); ok {
			t.Errorf("room %s was restored from an unusable snapshot", bad)
		}
		if e.store.deleteCount(bad) != 1 {
			t.Errorf("deletes of %s = %d", bad, e.store.deleteCount(bad))
		}
	}
	// The failed deletion is logged and the snapshot is left for the janitor.
	if got := strings.Count(e.logs.String(), "discarding unusable room snapshot"); got != 8 {
		t.Errorf("discard warnings = %d, want 8", got)
	}
	if !strings.Contains(e.logs.String(), "deleting unusable room snapshot failed") {
		t.Error("the failed deletion was not logged")
	}
	if !e.store.has("HHHH") {
		t.Error("the good snapshot was deleted")
	}
}

// newPersistedGame returns the JSON of a snapshot of a 2-player lobby after mod.
func newPersistedGame(t *testing.T, code string, mod func(*game.Game)) []byte {
	t.Helper()
	g := game.NewGame(game.Player{ID: "h", Name: "Host", Avatar: testAvatar}, t0)
	if _, err := g.Apply(t0, "g", game.Join{Name: "Guest", Avatar: testAvatar}); err != nil {
		t.Fatal(err)
	}
	mod(g)
	b, err := encodePersisted(code, t0, t0, g)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRestoreHandlesSnapshotWithNilRoundAnswers(t *testing.T) {
	g := game.NewGame(game.Player{ID: "h", Name: "Host", Avatar: testAvatar}, t0)
	g.Phase = game.PhaseLetter
	g.Round = &game.Round{Number: 1, LetterStartedAt: t0}
	b, err := encodePersisted("BBBB", t0, t0, g)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"answers":null`) {
		t.Fatalf("setup: expected a null answers map in %s", b)
	}
	_, got, err := decodePersisted(store.Snapshot{Code: "BBBB", State: b})
	if err != nil {
		t.Fatal(err)
	}
	if got.Round.Answers == nil {
		t.Error("a nil answers map must be re-created (assigning to it would panic)")
	}
}

func TestRestoreMarksLeftSeatsDisconnectedToo(t *testing.T) {
	e := newEnv(t)
	state := newPersistedGame(t, "BBBB", func(g *game.Game) {
		g.Players[1].Left = true
		g.Players[1].Connected = true // not something the engine produces, but must be fixed
	})
	if err := e.store.Memory.Save(context.Background(), store.Snapshot{Code: "BBBB", State: state, UpdatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if n, err := e.hub.Start(context.Background()); err != nil || n != 1 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	for _, p := range query(e, "BBBB", func(r *room) []game.Player { return append([]game.Player(nil), r.g.Players...) }) {
		if p.Connected {
			t.Errorf("player %s restored as connected", p.ID)
		}
	}
}

func TestPersistedRoundTrip(t *testing.T) {
	lobby := game.NewGame(game.Player{ID: "h", Name: "Host", Avatar: testAvatar}, t0)

	playing := game.NewGame(game.Player{ID: "h", Name: "Host", Avatar: testAvatar}, t0)
	for _, step := range []struct {
		actor string
		cmd   game.Command
	}{
		{"g", game.Join{Name: "Guest", Avatar: testAvatar}},
		{"h", threeCats(nil)},
		{"h", game.StartGame{}},
		{"h", game.StopLetter{}},
		{"g", game.SetAnswer{Category: 1, Text: "Austria"}},
	} {
		if _, err := playing.Apply(t0, step.actor, step.cmd); err != nil {
			t.Fatalf("%T: %v", step.cmd, err)
		}
	}

	for name, g := range map[string]*game.Game{"lobby": lobby, "playing": playing} {
		t.Run(name, func(t *testing.T) {
			created, active := t0, t0.Add(time.Minute)
			b, err := encodePersisted("BCDF", created, active, g)
			if err != nil {
				t.Fatal(err)
			}
			p, got, err := decodePersisted(store.Snapshot{Code: "BCDF", State: b})
			if err != nil {
				t.Fatal(err)
			}
			if p.Version != persistedVersion || p.Code != "BCDF" || !p.CreatedAt.Equal(created) || !p.LastActive.Equal(active) {
				t.Errorf("header = %+v", p)
			}
			want, _ := json.Marshal(g)
			have, _ := json.Marshal(got)
			if string(want) != string(have) {
				t.Errorf("game changed in the round trip:\n want %s\n  got %s", want, have)
			}
		})
	}
}

// ---- shutdown ----

func TestShuttingDownReportsDrain(t *testing.T) {
	e := newEnv(t)
	code := e.create("h1", "Hugo")
	if e.hub.ShuttingDown() {
		t.Fatal("ShuttingDown before Shutdown")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.hub.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if !e.hub.ShuttingDown() {
		t.Error("ShuttingDown must report true once Shutdown ran")
	}
	if _, ok := e.hub.Info(code); ok {
		t.Error("a drained hub does not know its rooms any more (the HTTP layer maps this to 503, not 404)")
	}
}

func TestShutdown(t *testing.T) {
	l := newLobby3(t)
	e := l.e
	other := e.create("o1", "Olga")
	oc, _ := e.join(other, "o1", "Olga")
	l.startRound(nil)
	l.send(1, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Almeja"}) // not snapshotted yet
	oc.drain()
	l.drain()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.hub.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Shutdown took %v", d)
	}

	for i, c := range append(l.c[:], oc) {
		frames := c.drain()
		if got := kinds(frames); len(got) != 1 || got[0] != "event:server_restarting" {
			t.Errorf("client %d frames = %v, want only the restart event", i, got)
		}
		if got := c.closeCodes(); len(got) != 1 || got[0] != protocol.CloseServiceRestart {
			t.Errorf("client %d closes = %v, want [%d]", i, got, protocol.CloseServiceRestart)
		}
	}

	// The final snapshot has the change no trigger had saved yet, and nothing
	// was deleted.
	for _, code := range []string{l.code, other} {
		if !e.store.has(code) {
			t.Fatalf("the snapshot of %s was lost", code)
		}
		if e.store.deleteCount(code) != 0 {
			t.Errorf("Shutdown deleted the snapshot of %s", code)
		}
	}
	if _, g := e.stored(l.code); g.Round.Answers["p2"][0] != "Almeja" {
		t.Errorf("final snapshot answers = %v", g.Round.Answers["p2"])
	}
	if st := e.hub.Stats(); st != (Stats{}) {
		t.Errorf("Stats after Shutdown = %+v", st)
	}
	if e.clock.Pending() != 0 {
		t.Errorf("pending timers after Shutdown = %d", e.clock.Pending())
	}

	// Nothing new is accepted, and stale frames are dropped.
	if _, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: "x", Name: "X"}); !errors.Is(err, ErrShuttingDown) {
		t.Errorf("Create: %v, want ErrShuttingDown", err)
	}
	if _, err := e.hub.Connect(l.code, newFakeClient("p1")); !errors.Is(err, ErrShuttingDown) {
		t.Errorf("Connect: %v, want ErrShuttingDown", err)
	}
	if _, ok := e.hub.Info(l.code); ok {
		t.Error("Info of a room after Shutdown = true")
	}
	l.s[0].Handle(game.CmdBasta, nil)
	l.s[0].Disconnect()
	if got := l.c[0].drain(); len(got) != 0 {
		t.Errorf("frames after Shutdown: %v", kinds(got))
	}
	if err := e.hub.Shutdown(ctx); err != nil {
		t.Errorf("second Shutdown: %v", err)
	}
}

func TestShutdownThenRestartKeepsTheGame(t *testing.T) {
	l := newLobby3(t)
	e, code := l.e, l.code
	l.startRound(nil)
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	e.hub = e.newHubOn()
	if n, err := e.hub.Start(context.Background()); err != nil || n != 1 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	// The grace timer is re-armed: the review begins on schedule.
	e.advance(game.GraceDuration + game.LateUpdateAllowance)
	if info, _ := e.hub.Info(code); info.Phase != game.PhaseReview {
		t.Errorf("phase = %s, want review", info.Phase)
	}
}

func TestShutdownWithCancelledContext(t *testing.T) {
	l := newLobby3(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := l.e.hub.Shutdown(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Shutdown = %v, want context.Canceled", err)
	}
	for i, c := range l.c {
		eventually(t, "the client to be closed", func() bool { return len(c.closeCodes()) > 0 })
		if got := c.closeCodes(); len(got) != 1 || got[0] != protocol.CloseServiceRestart {
			t.Errorf("p%d closes = %v", i+1, got)
		}
	}
}

func TestShutdownWithoutStartOrRooms(t *testing.T) {
	e := newEnv(t)
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown of an empty hub: %v", err)
	}
}

func TestInfoIsNotJoinableWhileShuttingDown(t *testing.T) {
	e := newEnv(t)
	code := e.create("h", "Host")
	e.hub.mu.Lock()
	e.hub.shuttingDown = true
	e.hub.mu.Unlock()
	info, ok := e.hub.Info(code)
	if !ok || info.Joinable {
		t.Errorf("Info = %+v, %v; want not joinable", info, ok)
	}
}

// ---- janitor ----

func TestJanitorDeletesOldSnapshots(t *testing.T) {
	e := newEnv(t, func(c *Config) {
		c.SnapshotMaxAge = 6 * time.Hour
		c.JanitorEvery = time.Hour
	})
	ctx := context.Background()
	if n, err := e.hub.Start(ctx); err != nil || n != 0 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	stale, fresh := t0.Add(-7*time.Hour), t0.Add(-5*time.Hour)
	for code, at := range map[string]time.Time{"ZZZZ": stale, "XXXX": fresh} {
		if err := e.store.Memory.Save(ctx, store.Snapshot{Code: code, State: []byte(`{}`), UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	if e.clock.Pending() != 1 {
		t.Fatalf("pending timers = %d, want the janitor's", e.clock.Pending())
	}

	e.clock.Advance(time.Hour - time.Millisecond)
	if !e.store.has("ZZZZ") {
		t.Fatal("the janitor ran early")
	}
	e.clock.Advance(time.Millisecond)
	eventually(t, "the janitor to delete the stale snapshot", func() bool { return !e.store.has("ZZZZ") })
	if !e.store.has("XXXX") {
		t.Error("the janitor deleted a recent snapshot")
	}
	// It is periodic: the fresh one goes at the next round.
	eventually(t, "the janitor to re-arm", func() bool { return e.clock.Pending() == 1 })
	e.clock.Advance(time.Hour)
	eventually(t, "the second sweep", func() bool { return !e.store.has("XXXX") })

	e.store.mu.Lock()
	cutoff := e.store.janitorCutoffs[0]
	e.store.mu.Unlock()
	if want := t0.Add(time.Hour).Add(-6 * time.Hour); !cutoff.Equal(want) {
		t.Errorf("first cutoff = %v, want %v", cutoff, want)
	}
}

func TestJanitorSurvivesStoreErrors(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.JanitorEvery = time.Minute })
	if _, err := e.hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.store.update(func(f *faultStore) { f.failJanitor = true })
	e.clock.Advance(time.Minute)
	eventually(t, "a failing sweep", func() bool {
		return metricValue(t, e.reg, "tuttifrutti_snapshot_errors_total") == 1
	})
	eventually(t, "the janitor to re-arm", func() bool { return e.clock.Pending() == 1 })
	e.store.update(func(f *faultStore) { f.failJanitor = false })
	e.clock.Advance(time.Minute)
	eventually(t, "the next sweep", func() bool {
		e.store.mu.Lock()
		defer e.store.mu.Unlock()
		return len(e.store.janitorCutoffs) == 2
	})
}

func TestJanitorStopsWithItsContextAndOnShutdown(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := e.hub.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if e.clock.Pending() != 1 {
		t.Fatalf("pending = %d", e.clock.Pending())
	}
	cancel()
	eventually(t, "the janitor to stop with its context", func() bool { return e.clock.Pending() == 0 })

	e2 := newEnv(t)
	if _, err := e2.hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e2.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e2.clock.Pending() != 0 {
		t.Errorf("the janitor's timer outlived Shutdown")
	}
}

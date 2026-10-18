package room

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// retiringCodes returns the codes the hub still holds for closed rooms.
func retiringCodes(h *Hub) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for code := range h.retiring {
		out = append(out, code)
	}
	slices.Sort(out)
	return out
}

// ---- 1. a failed snapshot deletion keeps the room code reserved ----

func TestFailedDeleteKeepsTheCodeReservedUntilItSucceeds(t *testing.T) {
	// BBBB is the first room. While its snapshot cannot be deleted the code
	// must not be reused: each later Create draws BBBB again and has to skip
	// it. Only once the deletion went through does BBBB become free again.
	const failures = 6 // more than any bounded retry would allow
	e := newEnv(t, func(c *Config) {
		c.CodeSource = codeSource("BBBB", "BBBB", "CCCC", "BBBB", "DDDD", "BBBB")
	})
	a := e.create("a", "A")
	if a != "BBBB" {
		t.Fatalf("first code = %q", a)
	}
	e.waitSaved(a)
	e.store.update(func(f *faultStore) { f.failDeletes = failures })

	e.advance(defaultUnclaimedTTL) // the room closes; its snapshot cannot be deleted
	if _, ok := e.hub.Info(a); ok {
		t.Fatal("setup: the room is still open")
	}

	// The saver backs off 1s, 2s, 4s and then at most 8s (the test caps).
	backoffs := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second, 8 * time.Second}
	attempts := 1
	for i, wait := range backoffs {
		eventually(t, fmt.Sprintf("delete attempt %d", attempts), func() bool { return e.store.deleteCount(a) == attempts })
		eventually(t, "the saver to back off", func() bool { return e.otherTimers() == 1 })

		if got := retiringCodes(e.hub); !slices.Equal(got, []string{a}) {
			t.Fatalf("after %d failures the reserved codes are %v, want [%s]", attempts, got, a)
		}
		if !e.store.has(a) {
			t.Fatalf("after %d failures the stale snapshot is gone", attempts)
		}

		switch i {
		case 0:
			// The next Create draws BBBB (reserved) and moves on to CCCC.
			if got := e.create("b", "B"); got != "CCCC" {
				t.Fatalf("a new room got %q while BBBB is reserved, want CCCC", got)
			}
		case 3:
			if got := e.create("c", "C"); got != "DDDD" {
				t.Fatalf("a new room got %q while BBBB is reserved, want DDDD", got)
			}
			// The row under BBBB is still the old room's, not a new one's.
			if _, g := e.stored(a); g.HostID != "a" {
				t.Errorf("the stale row's host = %q, want the old room's host a", g.HostID)
			}
		}

		e.clock.Advance(wait - time.Millisecond)
		if got := e.store.deleteCount(a); got != attempts {
			t.Fatalf("attempt %d came before its backoff of %v elapsed", got, wait)
		}
		e.clock.Advance(time.Millisecond)
		attempts++
	}

	// The 7th attempt succeeds, and only then the code is free.
	eventually(t, "the snapshot to be deleted", func() bool { return !e.store.has(a) })
	eventually(t, "the code to be released", func() bool { return len(retiringCodes(e.hub)) == 0 })
	if got := e.store.deleteCount(a); got != failures+1 {
		t.Errorf("delete attempts = %d, want %d (the failures and the success)", got, failures+1)
	}
	if got := metricValue(t, e.reg, "tuttifrutti_snapshot_errors_total"); got != failures {
		t.Errorf("snapshot errors = %v, want %d", got, failures)
	}
	if e.otherTimers() != 0 {
		t.Errorf("the saver left %d timers behind", e.otherTimers())
	}
	if got := e.create("d", "D"); got != "BBBB" {
		t.Errorf("the released code was not reusable: got %q", got)
	}
}

func TestShutdownDoesNotWaitForAFailingDelete(t *testing.T) {
	t.Run("deletion keeps failing", func(t *testing.T) {
		e := newEnv(t)
		gone := e.create("a", "A")
		e.waitSaved(gone)
		e.store.update(func(f *faultStore) { f.failDeletes = 1 << 30 })
		e.advance(defaultUnclaimedTTL)
		eventually(t, "the first failed deletion", func() bool { return e.store.deleteCount(gone) == 1 })
		eventually(t, "the saver to back off", func() bool { return e.otherTimers() == 1 })

		// A live room is shut down normally meanwhile.
		live := e.create("b", "B")
		c, _ := e.join(live, "b", "B")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		start := time.Now()
		if err := e.hub.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("Shutdown took %v", d)
		}
		if got := c.closeCodes(); !slices.Equal(got, []int{protocol.CloseServiceRestart}) {
			t.Errorf("live client closes = %v", got)
		}
		if e.clock.Pending() != 0 {
			t.Errorf("%d timers outlived Shutdown", e.clock.Pending())
		}
		// Nothing was retried after Shutdown began; the code stays reserved
		// and the stale snapshot is left for the next process.
		if got := e.store.deleteCount(gone); got != 1 {
			t.Errorf("delete attempts = %d, want the single one before Shutdown", got)
		}
		if got := retiringCodes(e.hub); !slices.Equal(got, []string{gone}) {
			t.Errorf("reserved codes = %v, want [%s]", got, gone)
		}
		if !e.store.has(gone) || !e.store.has(live) {
			t.Error("a snapshot was lost")
		}
	})

	t.Run("deletion hangs", func(t *testing.T) {
		e := newEnv(t)
		gone := e.create("a", "A")
		e.waitSaved(gone)
		e.store.update(func(f *faultStore) { f.hangDeletes = true })
		e.advance(defaultUnclaimedTTL)
		eventually(t, "the deletion to start", func() bool { return e.store.deleteCount(gone) == 1 })

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		start := time.Now()
		if err := e.hub.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Shutdown = %v, want DeadlineExceeded", err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("Shutdown took %v with a hanging delete", d)
		}
		// The hung call is aborted (the leak check of the cleanup verifies the
		// goroutine ends).
	})
}

// ---- 2. the janitor must not take the snapshots of live rooms ----

func TestHeartbeatFor(t *testing.T) {
	for _, tc := range []struct {
		maxAge, want time.Duration
	}{
		{6 * time.Hour, 90 * time.Minute},
		{time.Hour, 15 * time.Minute},
		{40 * time.Second, 10 * time.Second},
		{0, time.Millisecond},
		{time.Microsecond, time.Millisecond},
	} {
		if got := heartbeatFor(tc.maxAge); got != tc.want {
			t.Errorf("heartbeatFor(%v) = %v, want %v", tc.maxAge, got, tc.want)
		}
	}
}

func TestHeartbeatSavesQuietRooms(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) (*env, string)
	}{
		{"lobby with a client", func(t *testing.T) (*env, string) {
			l := newLobby3(t)
			return l.e, l.code
		}},
		{"round summary", func(t *testing.T) (*env, string) {
			l := newLobby3(t)
			l.startRound(nil)
			l.fillAnswers()
			l.send(0, game.CmdBasta, nil)
			l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
			l.send(0, game.CmdFinishReview, nil)
			return l.e, l.code
		}},
		{"final", func(t *testing.T) (*env, string) {
			l := newLobby3(t)
			finishGame(t, l)
			return l.e, l.code
		}},
		{"nobody connected", func(t *testing.T) (*env, string) {
			l := newLobby3(t, func(c *Config) { c.IdleTTL = 1000 * time.Hour })
			for _, s := range l.s {
				s.Disconnect()
			}
			l.e.sync(l.code)
			return l.e, l.code
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, code := tc.setup(t)
			hb := e.hub.heartbeat
			if hb != 90*time.Minute {
				t.Fatalf("heartbeat = %v", hb)
			}
			e.waitSaved(code)
			n := e.store.saveCount(code)

			e.advance(hb - time.Millisecond)
			e.waitSaved(code)
			if got := e.store.saveCount(code); got != n {
				t.Fatalf("saved %d times before the heartbeat", got-n)
			}
			e.advance(time.Millisecond)
			e.waitSaved(code)
			if got := e.store.saveCount(code); got != n+1 {
				t.Fatalf("saves at the heartbeat = %d, want %d", got-n, 1)
			}
			// The write is stamped with the (manual) time of the write.
			snaps, err := e.store.Memory.LoadAll(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range snaps {
				if s.Code == code && !s.UpdatedAt.Equal(e.clock.Now()) {
					t.Errorf("UpdatedAt = %v, want %v", s.UpdatedAt, e.clock.Now())
				}
			}

			// ... and again, every heartbeat, as a normal save.
			e.advance(hb)
			e.waitSaved(code)
			if got := e.store.saveCount(code); got != n+2 {
				t.Errorf("saves after two heartbeats = %d, want 2", got-n)
			}
			if got := metricValue(t, e.reg, "tuttifrutti_snapshot_errors_total"); got != 0 {
				t.Errorf("snapshot errors = %v", got)
			}
			if p, _ := e.stored(code); p.Version != persistedVersion {
				t.Errorf("heartbeat snapshot header = %+v", p)
			}
		})
	}
}

// Any save moves the heartbeat: a room that is saved often needs no extra one.
func TestHeartbeatIsDeferredByOtherSaves(t *testing.T) {
	l := newLobby3(t)
	e := l.e
	hb := e.hub.heartbeat
	e.waitSaved(l.code)
	n := e.store.saveCount(l.code)

	e.advance(hb - time.Minute)
	l.send(0, game.CmdUpdateSettings, threeCats(nil)) // saved now
	e.advance(hb - time.Minute)                       // past the first heartbeat instant, before the moved one
	e.waitSaved(l.code)
	if got := e.store.saveCount(l.code); got != n+1 {
		t.Fatalf("saves = %d, want only the update_settings one", got-n)
	}
	e.advance(time.Minute)
	e.waitSaved(l.code)
	if got := e.store.saveCount(l.code); got != n+2 {
		t.Errorf("saves = %d, want one heartbeat after it", got-n)
	}
}

func TestJanitorNeverTakesTheSnapshotOfALiveRoom(t *testing.T) {
	const maxAge = 6 * time.Hour
	e := newEnv(t, func(c *Config) {
		c.SnapshotMaxAge = maxAge
		c.JanitorEvery = time.Hour
	})
	if n, err := e.hub.Start(context.Background()); err != nil || n != 0 {
		t.Fatalf("Start = %d, %v", n, err)
	}

	// Quiet, connected rooms: an unchanged lobby and a finished game.
	lobby := e.create("l1", "Lia")
	e.join(lobby, "l1", "Lia")
	l := &lobby3{e: e, code: e.create("p1", "Ana")}
	for i, name := range []string{"Ana", "Beto", "Cami"} {
		l.c[i], l.s[i] = e.join(l.code, fmt.Sprintf("p%d", i+1), name)
	}
	finishGame(t, l)
	// A room nobody is in goes idle as before, long before the janitor matters.
	abandoned := e.create("x1", "Xena")
	_, sx := e.join(abandoned, "x1", "Xena")
	sx.Disconnect()
	e.sync(abandoned)
	e.waitSaved(lobby)
	e.waitSaved(l.code)

	e.advance(3 * maxAge)
	eventually(t, "several janitor runs", func() bool {
		e.store.mu.Lock()
		defer e.store.mu.Unlock()
		return len(e.store.janitorCutoffs) >= 6
	})
	e.waitSaved(lobby)
	e.waitSaved(l.code)

	for _, code := range []string{lobby, l.code} {
		if _, ok := e.hub.Info(code); !ok {
			t.Fatalf("room %s is gone", code)
		}
		if !e.store.has(code) {
			t.Fatalf("the janitor deleted the snapshot of the live room %s", code)
		}
	}
	snaps, err := e.store.Memory.LoadAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range snaps {
		if age := e.clock.Now().Sub(s.UpdatedAt); age >= maxAge/2 {
			t.Errorf("snapshot %s is %v old: live rooms must be refreshed well within SnapshotMaxAge (%v)", s.Code, age, maxAge)
		}
	}

	// The idle room closed on its own and its snapshot went with it.
	if _, ok := e.hub.Info(abandoned); ok {
		t.Error("the room nobody was in outlived IdleTTL")
	}
	if e.store.has(abandoned) {
		t.Error("the idle room's snapshot is still stored")
	}

	// A crash now (no Shutdown, so no final snapshot): a new hub over a copy of
	// what the store holds restores both live rooms.
	copyStore := store.NewMemory()
	for _, s := range snaps {
		if err := copyStore.Save(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	h2 := e.newHubOn(func(c *Config) { c.Store = copyStore })
	n, err := h2.Start(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("Start over the surviving snapshots = %d, %v; want both live rooms", n, err)
	}
	for _, code := range []string{lobby, l.code} {
		if _, ok := h2.Info(code); !ok {
			t.Errorf("room %s was not restored", code)
		}
	}
	if info, _ := h2.Info(l.code); info.Phase != game.PhaseFinal {
		t.Errorf("restored phase = %s, want final", info.Phase)
	}
}

func TestAbandonedRoomKeepsItsSnapshotFreshUntilItIdlesOut(t *testing.T) {
	// IdleTTL longer than the heartbeat: the room is still alive, so the
	// janitor must not take its snapshot either.
	const maxAge = 6 * time.Hour
	e := newEnv(t, func(c *Config) {
		c.SnapshotMaxAge = maxAge
		c.JanitorEvery = time.Hour
		c.IdleTTL = 20 * time.Hour
	})
	if _, err := e.hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	code := e.create("h", "Host")
	_, s := e.join(code, "h", "Host")
	s.Disconnect()
	e.sync(code)

	e.advance(3 * maxAge) // 18h: not idle yet
	eventually(t, "janitor runs", func() bool {
		e.store.mu.Lock()
		defer e.store.mu.Unlock()
		return len(e.store.janitorCutoffs) >= 6
	})
	e.waitSaved(code)
	if _, ok := e.hub.Info(code); !ok || !e.store.has(code) {
		t.Fatalf("the room or its snapshot is gone after 18h (idle TTL 20h)")
	}
	e.advance(2 * time.Hour)
	if _, ok := e.hub.Info(code); ok {
		t.Error("the room outlived IdleTTL")
	}
	eventually(t, "the snapshot to be deleted", func() bool { return !e.store.has(code) })
}

func TestPersistRecoversFromEncodingFailureWithoutSpinning(t *testing.T) {
	// A game that cannot be encoded (json refuses a time outside years 0-9999)
	// must not make the room's timer fire in a loop: the next snapshot is only
	// due after its interval, even though the heartbeat is overdue.
	e := newEnv(t)
	code := e.create("h", "Host")
	e.join(code, "h", "Host")
	e.waitSaved(code)
	saves := e.store.saveCount(code)

	query(e, code, func(r *room) struct{} { r.stopWake(); return struct{}{} })
	e.clock.Set(t0.Add(e.hub.heartbeat + time.Minute)) // the heartbeat is overdue
	type result struct {
		next, lastSaved, now time.Time
	}
	res := query(e, code, func(r *room) result {
		r.g.Round = &game.Round{LetterStartedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}
		defer func() { r.g.Round = nil }()
		now := r.clock.Now()
		r.persist(now)
		return result{r.nextWake(), r.lastSaved, now}
	})

	if got := metricValue(t, e.reg, "tuttifrutti_snapshot_errors_total"); got != 1 {
		t.Errorf("snapshot errors = %v, want 1 (the failed encoding)", got)
	}
	if !strings.Contains(e.logs.String(), "encoding the snapshot failed") {
		t.Error("the failure was not logged")
	}
	if !res.lastSaved.Equal(res.now) || !res.next.After(res.now) {
		t.Errorf("after a failed encoding the room wants to wake at %v (now %v, lastSaved %v): it would spin", res.next, res.now, res.lastSaved)
	}
	if got := e.store.saveCount(code); got != saves {
		t.Errorf("a snapshot that could not be encoded was written (%d new saves)", got-saves)
	}
}

// ---- 3. connections that never join ----

func TestJoinTimeoutClosesOnlyUnjoinedSessions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout time.Duration // Config.JoinTimeout
		want    time.Duration // effective
	}{
		{"default", 0, 10 * time.Second},
		{"custom", 3 * time.Second, 3 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, func(c *Config) { c.JoinTimeout = tc.timeout })
			code := e.create("p1", "Ana")
			joined, _ := e.join(code, "p1", "Ana")
			joined.drain()

			x, sx := e.connect(code, "x") // at 0
			e.advance(tc.want / 2)
			y, _ := e.connect(code, "y") // each connection has its own deadline

			e.advance(tc.want/2 - time.Millisecond)
			if len(x.closeCodes()) != 0 {
				t.Fatalf("x closed %v early", x.closeRecs())
			}
			e.advance(time.Millisecond)
			if got := x.closeRecs(); len(got) != 1 || got[0].code != protocol.ClosePolicy || got[0].reason != "join timeout" {
				t.Fatalf("x closes = %+v, want one policy close with reason \"join timeout\"", got)
			}
			if len(y.closeCodes()) != 0 {
				t.Errorf("y closed with x: %v", y.closeRecs())
			}
			if st := e.hub.Stats(); st.Connections != 2 {
				t.Errorf("connections = %d, want the joined one and y", st.Connections)
			}

			e.advance(tc.want / 2)
			if got := y.closeCodes(); !slices.Equal(got, []int{protocol.ClosePolicy}) {
				t.Errorf("y closes = %v", got)
			}
			// The joined player is untouched and was told nothing.
			if len(joined.closeCodes()) != 0 || len(joined.drain()) != 0 {
				t.Errorf("the joined player was disturbed: closes %v", joined.closeCodes())
			}
			if st := e.hub.Stats(); st != (Stats{Rooms: 1, Connections: 1}) {
				t.Errorf("Stats = %+v", st)
			}

			// What a closed socket still sends goes nowhere.
			sx.Handle(game.CmdJoin, mustJSON(t, protocol.JoinRequest{Code: code, Name: "X", Avatar: testAvatar}))
			sx.Disconnect()
			e.sync(code)
			if len(x.drain()) != 0 || len(joined.drain()) != 0 {
				t.Error("frames of an expired session reached the room")
			}
			if info, _ := e.hub.Info(code); info.Players != 1 {
				t.Errorf("players = %d, an expired session must not be able to join", info.Players)
			}
		})
	}
}

func TestPingsCannotExtendTheJoinDeadline(t *testing.T) {
	// The transport answers ping itself and never tells the room, so a socket
	// that only pings is, to the room, a socket that is silent.
	e := newEnv(t)
	code := e.create("h", "Host")
	c, _ := e.connect(code, "h")
	for range 9 {
		e.advance(time.Second)
	}
	if len(c.closeCodes()) != 0 {
		t.Fatalf("closed at 9s: %v", c.closeRecs())
	}
	e.advance(time.Second)
	if got := c.closeRecs(); len(got) != 1 || got[0].code != protocol.ClosePolicy {
		t.Errorf("closes at 10s = %+v, want a policy close", got)
	}
}

func TestRejectedJoinDoesNotExtendTheDeadline(t *testing.T) {
	t.Run("invalid name", func(t *testing.T) {
		e := newEnv(t)
		code := e.create("h", "Host")
		c, s := e.connect(code, "newbie")

		e.advance(6 * time.Second)
		e.handle(code, s, game.CmdJoin, protocol.JoinRequest{Code: code, Name: "", Avatar: testAvatar})
		e.advance(3 * time.Second)
		e.handle(code, s, game.CmdJoin, protocol.JoinRequest{Code: code, Name: "", Avatar: testAvatar})
		if got := kinds(c.drain()); !slices.Equal(got, []string{"error:invalid_name", "error:invalid_name"}) {
			t.Fatalf("frames = %v", got)
		}
		if len(c.closeCodes()) != 0 {
			t.Fatalf("closed at 9s: %v", c.closeRecs())
		}
		e.advance(time.Second)
		if got := c.closeRecs(); len(got) != 1 || got[0].code != protocol.ClosePolicy || got[0].reason != "join timeout" {
			t.Errorf("closes at 10s = %+v: the rejections must not have moved the deadline", got)
		}
	})

	t.Run("lobby full", func(t *testing.T) {
		e := newEnv(t)
		code := e.create("p0", "P")
		e.join(code, "p0", "P")
		for i := 1; i < game.MaxPlayers; i++ {
			e.join(code, playerN(i), "P")
		}
		c, s := e.join(code, "late", "Late")
		if got := kinds(c.drain()); !slices.Equal(got, []string{"error:lobby_full"}) {
			t.Fatalf("frames = %v", got)
		}
		if st := e.hub.Stats(); st.Connections != game.MaxPlayers+1 {
			t.Fatalf("connections = %d: the rejected socket should still be attached", st.Connections)
		}
		e.advance(10*time.Second - time.Millisecond)
		if len(c.closeCodes()) != 0 {
			t.Fatalf("closed early: %v", c.closeRecs())
		}
		e.advance(time.Millisecond)
		if got := c.closeCodes(); !slices.Equal(got, []int{protocol.ClosePolicy}) {
			t.Errorf("closes = %v", got)
		}
		s.Disconnect()
		e.sync(code)
		if st := e.hub.Stats(); st.Connections != game.MaxPlayers {
			t.Errorf("connections = %d, want %d", st.Connections, game.MaxPlayers)
		}
	})
}

func TestSuccessfulJoinCancelsTheDeadline(t *testing.T) {
	e := newEnv(t)
	code := e.create("h", "Host")
	c, s := e.connect(code, "h")
	e.advance(3 * time.Second)
	e.handle(code, s, game.CmdJoin, protocol.JoinRequest{Code: code, Name: "Host", Avatar: testAvatar})
	c.drain()

	joinBy := e.clock.Now().Add(7 * time.Second)
	if got := query(e, code, func(r *room) time.Time { return r.wakeAt }); !got.After(joinBy) {
		t.Errorf("the room still waits for %v (the join deadline was %v)", got.Sub(t0), joinBy.Sub(t0))
	}
	e.advance(time.Hour)
	if len(c.closeCodes()) != 0 {
		t.Errorf("a joined session was closed: %v", c.closeRecs())
	}
	if st := e.hub.Stats(); st.Connections != 1 {
		t.Errorf("connections = %d", st.Connections)
	}
	if e.clock.Pending() != 1 {
		t.Errorf("pending timers = %d, want the room's single timer", e.clock.Pending())
	}
}

func TestUnjoinedSessionsDoNotClaimOrKeepTheRoomAlive(t *testing.T) {
	t.Run("expired sockets leave the unclaimed deadline alone", func(t *testing.T) {
		e := newEnv(t, func(c *Config) { c.UnclaimedTTL = time.Minute; c.IdleTTL = 10 * time.Minute })
		code := e.create("h", "Host")
		c, _ := e.connect(code, "h")
		e.advance(10 * time.Second)
		if got := c.closeCodes(); !slices.Equal(got, []int{protocol.ClosePolicy}) {
			t.Fatalf("closes = %v", got)
		}
		e.advance(49*time.Second + 999*time.Millisecond)
		if _, ok := e.hub.Info(code); !ok {
			t.Fatal("closed before UnclaimedTTL")
		}
		// Still unclaimed: it closes one minute after creation, not ten
		// minutes after the connection.
		e.advance(time.Millisecond)
		if _, ok := e.hub.Info(code); ok {
			t.Fatal("connecting without joining claimed the room")
		}
		eventually(t, "the snapshot to be deleted", func() bool { return !e.store.has(code) })
	})

	t.Run("an attached unjoined socket does not hold an idle room open", func(t *testing.T) {
		e := newEnv(t, func(c *Config) {
			c.UnclaimedTTL = time.Minute
			c.JoinTimeout = 5 * time.Minute
		})
		code := e.create("h", "Host")
		c, _ := e.connect(code, "h")
		e.advance(59 * time.Second)
		if _, ok := e.hub.Info(code); !ok {
			t.Fatal("closed early")
		}
		e.advance(time.Second)
		if _, ok := e.hub.Info(code); ok {
			t.Fatal("an unjoined socket kept the room alive")
		}
		if got := c.closeCodes(); !slices.Equal(got, []int{protocol.CloseRoomClosed}) {
			t.Errorf("the socket's closes = %v, want the room-closed code", got)
		}
		if st := e.hub.Stats(); st != (Stats{}) {
			t.Errorf("Stats = %+v", st)
		}
	})

	t.Run("a join claims the room", func(t *testing.T) {
		e := newEnv(t, func(c *Config) { c.UnclaimedTTL = time.Minute; c.IdleTTL = 10 * time.Minute })
		code := e.create("h", "Host")
		_, s := e.join(code, "h", "Host")
		s.Disconnect()
		e.sync(code)
		e.advance(9*time.Minute + 59*time.Second)
		if _, ok := e.hub.Info(code); !ok {
			t.Fatal("a claimed room closed before IdleTTL")
		}
		e.advance(time.Second)
		if _, ok := e.hub.Info(code); ok {
			t.Fatal("a claimed room outlived IdleTTL")
		}
	})
}

func TestUnjoinedCap(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.JoinTimeout = time.Hour })
	code := e.create("h", "Host")
	host, hs := e.join(code, "h", "Host") // joined sessions do not count
	host.drain()

	var clients []*fakeClient
	var sessions []Session
	for i := range maxUnjoinedSessions {
		c, s := e.connect(code, fmt.Sprintf("u%d", i))
		clients = append(clients, c)
		sessions = append(sessions, s)
	}
	if st := e.hub.Stats(); st.Connections != maxUnjoinedSessions+1 {
		t.Fatalf("connections = %d, want %d", st.Connections, maxUnjoinedSessions+1)
	}
	for i, c := range clients {
		if len(c.drain()) != 0 || len(c.closeCodes()) != 0 {
			t.Fatalf("connection %d (within the cap) was disturbed", i)
		}
	}

	// One more is turned away: told, closed, and given a session that does nothing.
	over := newFakeClient("over")
	s, err := e.hub.Connect(code, over)
	if err != nil || s == nil {
		t.Fatalf("Connect over the cap = %v, %v; want a session and no error", s, err)
	}
	frames := over.drain()
	if len(frames) != 1 || frames[0].T != protocol.TypeError {
		t.Fatalf("frames = %v, want one error", kinds(frames))
	}
	if eb := frames[0].errBody(t); eb.Code != protocol.ErrServerBusy || eb.Ref != game.CmdJoin {
		t.Errorf("error = %+v, want server_busy in answer to join", eb)
	}
	if got := over.closeCodes(); !slices.Equal(got, []int{protocol.ClosePolicy}) {
		t.Errorf("closes = %v, want [%d]", got, protocol.ClosePolicy)
	}
	s.Handle(game.CmdJoin, mustJSON(t, protocol.JoinRequest{Code: code, Name: "Over", Avatar: testAvatar}))
	s.Disconnect()
	e.sync(code)
	if len(over.drain()) != 0 {
		t.Error("the refused session answered a frame")
	}
	if info, _ := e.hub.Info(code); info.Players != 1 {
		t.Errorf("players = %d: a refused connection must not be able to join", info.Players)
	}
	if st := e.hub.Stats(); st.Connections != maxUnjoinedSessions+1 {
		t.Errorf("connections = %d: a refused connection must not be counted", st.Connections)
	}
	if got := metricValue(t, e.reg, "tuttifrutti_commands_total", "type", "join", "result", "server_busy"); got != 1 {
		t.Errorf("join/server_busy = %v, want 1", got)
	}
	if got := metricValue(t, e.reg, "tuttifrutti_connections"); got != float64(maxUnjoinedSessions+1) {
		t.Errorf("connections gauge = %v", got)
	}

	// Room is closed to nobody else: the host can still act, and an unjoined
	// socket that disconnects or joins frees a place.
	e.handle(code, hs, game.CmdUpdateSettings, threeCats(nil))
	if got := kinds(host.drain()); !slices.Equal(got, []string{"state"}) {
		t.Errorf("host frames = %v", got)
	}
	sessions[0].Disconnect()
	e.sync(code)
	if c, _ := e.connect(code, "again"); len(c.drain()) != 0 || len(c.closeCodes()) != 0 {
		t.Error("a connection after a place was freed was refused")
	}
	if c, _ := e.connect(code, "full"); len(c.closeCodes()) != 1 {
		t.Error("the cap did not apply again once the freed place was taken")
	}
	e.handle(code, sessions[1], game.CmdJoin, protocol.JoinRequest{Code: code, Name: "U1", Avatar: testAvatar})
	if c, _ := e.connect(code, "after-join"); len(c.drain()) != 0 || len(c.closeCodes()) != 0 {
		t.Error("a successful join did not free a place")
	}
}

func TestUnjoinedCapIsPerRoom(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.JoinTimeout = time.Hour })
	a, b := e.create("a", "A"), e.create("b", "B")
	for i := range maxUnjoinedSessions {
		e.connect(a, fmt.Sprintf("u%d", i))
	}
	if c, _ := e.connect(b, "u"); len(c.closeCodes()) != 0 {
		t.Error("a full room made another room refuse connections")
	}
}

func TestLeavingLastClientRestartsTheIdleClock(t *testing.T) {
	// A player who was connected for hours and then leaves (rather than
	// dropping) is the last one out: the idle time counts from the leave.
	e := newEnv(t, func(c *Config) { c.IdleTTL = 30 * time.Minute })
	code := e.create("p1", "Ana")
	_, s1 := e.join(code, "p1", "Ana")
	_, s2 := e.join(code, "p2", "Beto")
	s2.Disconnect() // the second seat stays, disconnected
	e.sync(code)
	e.advance(5 * time.Hour)
	if _, ok := e.hub.Info(code); !ok {
		t.Fatal("the room closed with a player in it")
	}

	e.handle(code, s1, game.CmdLeave, nil)
	if _, ok := e.hub.Info(code); !ok {
		t.Fatal("the room closed the moment its last client left; the idle time must start then")
	}
	e.advance(30*time.Minute - time.Second)
	if _, ok := e.hub.Info(code); !ok {
		t.Fatal("closed before IdleTTL after the leave")
	}
	e.advance(time.Second)
	if _, ok := e.hub.Info(code); ok {
		t.Error("the room outlived IdleTTL")
	}
}

// ---- 4. Start does not replace live rooms ----

func TestStartSkipsLiveRooms(t *testing.T) {
	e := newEnv(t)
	live := e.create("h", "Host")
	c, s := e.join(live, "h", "Host")
	e.waitSaved(live)
	r := e.room(live)

	// A stored snapshot of another game under the live code, and an unrelated
	// one that is fine to restore.
	other := newPersistedGame(t, live, func(g *game.Game) { g.HostID = "someone-else" })
	if err := e.store.Memory.Save(context.Background(), store.Snapshot{Code: live, State: other, UpdatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Memory.Save(context.Background(), store.Snapshot{Code: "HHHH", State: newPersistedGame(t, "HHHH", func(*game.Game) {}), UpdatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	c.drain()

	n, err := e.hub.Start(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("Start = %d, %v; want only the unrelated room restored", n, err)
	}
	if e.room(live) != r {
		t.Fatal("Start replaced the live room (orphaning its actor)")
	}
	if st := e.hub.Stats(); st.Rooms != 2 {
		t.Errorf("rooms = %d, want 2", st.Rooms)
	}
	if !strings.Contains(e.logs.String(), "its code is already in use") {
		t.Error("the skipped snapshot was not logged")
	}
	if e.store.deleteCount(live) != 0 || !e.store.has(live) {
		t.Error("Start touched the snapshot of the live room")
	}
	// The live room still works and still has its player.
	e.handle(live, s, game.CmdUpdateSettings, threeCats(nil))
	frames := c.drain()
	if got := kinds(frames); !slices.Equal(got, []string{"state"}) {
		t.Fatalf("frames = %v", got)
	}
	if v := lastState(t, frames).View; v.HostID != "h" || !playerView(t, v, "h").Connected {
		t.Errorf("live room state after Start: host %q", v.HostID)
	}
}

func TestStartLeavesAnUnusableSnapshotOfALiveRoomAlone(t *testing.T) {
	// A corrupt snapshot is normally deleted; not when its code is a live
	// room's (the delete would take the live room's snapshot).
	e := newEnv(t)
	live := e.create("h", "Host")
	e.waitSaved(live)
	if err := e.store.Memory.Save(context.Background(), store.Snapshot{Code: live, State: []byte(`"garbage"`), UpdatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if n, err := e.hub.Start(context.Background()); err != nil || n != 0 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	if e.store.deleteCount(live) != 0 || !e.store.has(live) {
		t.Error("the snapshot of a live room was deleted as corrupt")
	}
	if _, ok := e.hub.Info(live); !ok {
		t.Error("the live room is gone")
	}
}

func TestStartSkipsCodesOfRoomsBeingClosed(t *testing.T) {
	e := newEnv(t)
	gone := e.create("a", "A")
	e.waitSaved(gone)
	e.store.update(func(f *faultStore) { f.failDeletes = 1 << 30 })
	e.advance(defaultUnclaimedTTL) // closed; the snapshot stays, the code is reserved
	eventually(t, "the failed deletion", func() bool { return e.store.deleteCount(gone) == 1 })
	if got := retiringCodes(e.hub); !slices.Equal(got, []string{gone}) {
		t.Fatalf("setup: reserved codes = %v", got)
	}

	n, err := e.hub.Start(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("Start = %d, %v; the reserved code must not be restored under the closing room", n, err)
	}
	if _, ok := e.hub.Info(gone); ok {
		t.Error("a room was restored under a reserved code")
	}
	if !e.store.has(gone) {
		t.Error("the snapshot was removed by Start")
	}
}

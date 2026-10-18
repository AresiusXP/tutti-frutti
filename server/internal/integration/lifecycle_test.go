package integration

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// The documented lifetimes (docs/DESIGN.md 9a).
const (
	unclaimedTTL = 2 * time.Minute
	idleTTL      = 30 * time.Minute
)

func hasSnapshot(t *testing.T, st store.Store, code string) bool {
	t.Helper()
	return slices.Contains(snapshotCodes(t, st), code)
}

// expectGone checks that a room is closed: not served over REST, not counted,
// no snapshot left, and a WebSocket join is answered with room_not_found/4004.
func expectGone(t *testing.T, s *stack, code string) {
	t.Helper()
	eventually(t, "room "+code+" to be gone", func() bool {
		_, ok := s.roomInfo(code)
		return !ok
	})
	eventually(t, "the snapshot of "+code+" to be deleted", func() bool { return !hasSnapshot(t, s.store, code) })
	p := s.newPlayer("Late")
	c := s.connect(p)
	c.send(game.CmdJoin, c.joinRequest(code))
	c.waitFor("room_not_found", isError(protocol.ErrRoomNotFound))
	c.expectClose(protocol.CloseRoomClosed)
}

// TestUnclaimedRoomExpires: a room created over REST that nobody connects to
// is closed after UnclaimedTTL, and its snapshot with it.
func TestUnclaimedRoomExpires(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		s := newStack(t, stackOpts{store: open(t)})
		p := s.newPlayer("Ana")
		code := s.createRoom(p, "")
		eventually(t, "the first snapshot", func() bool { return hasSnapshot(t, s.store, code) })

		s.advance(unclaimedTTL - time.Second)
		if info, ok := s.roomInfo(code); !ok || info.Players != 1 {
			t.Fatalf("room one second before the TTL = %+v (found %v)", info, ok)
		}
		if got := s.metricValue("tuttifrutti_rooms", nil); got != 1 {
			t.Fatalf("rooms gauge = %v", got)
		}
		s.advance(time.Second)
		expectGone(t, s, code)
		if got := s.hub.Stats().Rooms; got != 0 {
			t.Fatalf("stats rooms = %d", got)
		}
		if got := s.metricValue("tuttifrutti_rooms", nil); got != 0 {
			t.Fatalf("rooms gauge = %v after the room closed", got)
		}
	})
}

// TestIdleRoomExpires: a room whose players are all gone is closed after
// IdleTTL counted from the last disconnect; a connected player keeps it alive
// however long the clock runs, and a room somebody once claimed is not subject
// to the 2-minute unclaimed TTL.
func TestIdleRoomExpires(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		s := newStack(t, stackOpts{store: open(t)})
		tb := newTable(t, s, "Ana", "Beto")
		tb.advanceTo(stageLobby)

		// Connected players keep the room, whatever the clock does.
		s.advance(3 * idleTTL)
		if _, ok := s.roomInfo(tb.code); !ok {
			t.Fatal("a room with connected players was closed")
		}
		// (3 x 30 minutes also cover 5 hourly janitor runs, which only touch snapshots older than 6 h.)

		tb.seats[0].c.drop()
		waitConnected(t, tb.seats[0].p.id, false, tb.seats[1].c)
		s.advance(10 * time.Minute)
		tb.seats[1].c.drop()
		eventually(t, "both connections to be gone", func() bool { return s.hub.Stats().Connections == 0 })
		s.flush()

		// The idle clock starts at the last disconnect.
		s.advance(unclaimedTTL + 5*time.Minute)
		if _, ok := s.roomInfo(tb.code); !ok {
			t.Fatal("a claimed room was closed by the unclaimed TTL")
		}
		s.advance(idleTTL - unclaimedTTL - 5*time.Minute - time.Second)
		if _, ok := s.roomInfo(tb.code); !ok {
			t.Fatal("the room closed before IdleTTL")
		}
		eventually(t, "the live room's snapshot to be in the store", func() bool { return hasSnapshot(t, s.store, tb.code) })

		// Somebody coming back in time resets the clock.
		tb.reconnect(0)
		tb.seats[0].c.drop()
		eventually(t, "the connection to be gone", func() bool { return s.hub.Stats().Connections == 0 })
		s.flush()
		s.advance(idleTTL - time.Second)
		if _, ok := s.roomInfo(tb.code); !ok {
			t.Fatal("the idle clock was not restarted by the reconnect")
		}
		s.advance(time.Second)
		expectGone(t, s, tb.code)
	})
}

// TestEveryoneLeavingClosesTheRoom: when the last player leaves, the room and
// its snapshot go away at once.
func TestEveryoneLeavingClosesTheRoom(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		s := newStack(t, stackOpts{store: open(t)})
		tb := newTable(t, s, "Ana", "Beto")
		eventually(t, "the snapshot", func() bool { return hasSnapshot(t, s.store, tb.code) })

		tb.seats[1].c.send(game.CmdLeave, nil)
		tb.seats[1].c.expectClose(protocol.CloseLeft)
		tb.seats[0].c.waitState("alone", func(st *protocol.State) bool { return len(st.View.Players) == 1 })
		if info, ok := s.roomInfo(tb.code); !ok || info.Players != 1 {
			t.Fatalf("room after the first leave = %+v (found %v)", info, ok)
		}
		tb.seats[0].c.send(game.CmdLeave, nil)
		tb.seats[0].c.expectClose(protocol.CloseLeft)
		expectGone(t, s, tb.code)
		if got := s.hub.Stats(); got.Rooms != 0 || got.Connections != 0 {
			t.Fatalf("stats = %+v", got)
		}
	})
}

// TestMaxRooms: past MaxRooms, creating a room answers 503 server_busy until
// one closes.
func TestMaxRooms(t *testing.T) {
	s := newStack(t, stackOpts{hub: func(c *room.Config) { c.MaxRooms = 3 }})
	p := s.newPlayer("Ana")
	for i := range 3 {
		if _, err := s.tryCreateRoom(p, ""); err != nil {
			t.Fatalf("room %d: %v", i+1, err)
		}
	}
	for range 2 {
		r := s.do(http.MethodPost, "/api/rooms", []byte(validBody), withCookie(p))
		if r.status != http.StatusServiceUnavailable || r.errCode(t) != protocol.ErrServerBusy {
			t.Fatalf("fourth room = %d %s, want 503 server_busy", r.status, r.body)
		}
	}
	if got := s.hub.Stats().Rooms; got != 3 {
		t.Fatalf("rooms = %d", got)
	}
	s.advance(unclaimedTTL)
	eventually(t, "the unclaimed rooms to close", func() bool { return s.hub.Stats().Rooms == 0 })
	if _, err := s.tryCreateRoom(p, ""); err != nil {
		t.Fatalf("create after the rooms expired: %v", err)
	}
}

// counterBytes is a deterministic io.Reader whose bytes are the bits of a
// counter: with an alphabet draw of one byte per code character it yields
// every code of the two-letter alphabet {B, C}, in order.
type counterBytes struct {
	mu sync.Mutex
	n  int
}

func (c *counterBytes) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range p {
		p[i] = byte((c.n / 4) >> (c.n % 4) & 1)
		c.n++
	}
	return len(p), nil
}

var codeRE = regexp.MustCompile(`^[BCDFGHJKLMNPQRSTVWXZ2-9]{4}$`)

// TestRoomCodesAreUnique: many concurrent creates never hand out the same
// code twice, and when the code space is exhausted creation fails cleanly.
func TestRoomCodesAreUnique(t *testing.T) {
	t.Run("concurrent creates", func(t *testing.T) {
		s := newStack(t, stackOpts{})
		const workers, each = 8, 50
		codes := make(chan string, workers*each)
		errs := make(chan error, workers)
		var wg sync.WaitGroup
		for range workers {
			p, err := s.tryNewPlayer("Host")
			if err != nil {
				t.Fatal(err)
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range each {
					code, err := s.tryCreateRoom(p, "")
					if err != nil {
						errs <- err
						return
					}
					codes <- code
				}
			}()
		}
		wg.Wait()
		close(codes)
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for code := range codes {
			if !codeRE.MatchString(code) {
				t.Fatalf("malformed room code %q", code)
			}
			if seen[code] {
				t.Fatalf("room code %q handed out twice", code)
			}
			seen[code] = true
		}
		if len(seen) != workers*each || s.hub.Stats().Rooms != workers*each {
			t.Fatalf("%d distinct codes, %d rooms, want %d", len(seen), s.hub.Stats().Rooms, workers*each)
		}
		for code := range seen {
			if _, ok := s.roomInfo(code); !ok {
				t.Fatalf("room %s is not served", code)
			}
			break
		}
	})

	t.Run("exhausted code space", func(t *testing.T) {
		s := newStack(t, stackOpts{hub: func(c *room.Config) { c.CodeSource = &counterBytes{} }})
		p := s.newPlayer("Ana")
		seen := map[string]bool{}
		for i := range 16 {
			code, err := s.tryCreateRoom(p, "")
			if err != nil {
				t.Fatalf("room %d: %v", i+1, err)
			}
			if seen[code] || !regexp.MustCompile(`^[BC]{4}$`).MatchString(code) {
				t.Fatalf("room %d got code %q (seen %v)", i+1, code, seen[code])
			}
			seen[code] = true
		}
		r := s.do(http.MethodPost, "/api/rooms", []byte(validBody), withCookie(p))
		if r.status != http.StatusServiceUnavailable || r.errCode(t) != protocol.ErrServerBusy {
			t.Fatalf("17th room = %d %s, want 503 server_busy", r.status, r.body)
		}
	})
}

// TestLobbyCapacity: ten players fit, the eleventh is told the lobby is full
// until somebody leaves.
func TestLobbyCapacity(t *testing.T) {
	s := newStack(t, stackOpts{})
	names := []string{"P1", "P2", "P3", "P4", "P5", "P6", "P7", "P8", "P9", "P10"}
	tb := newTable(t, s, names...)
	if info, _ := s.roomInfo(tb.code); info.Players != 10 || info.Joinable {
		t.Fatalf("full lobby info = %+v, want 10 players and not joinable", info)
	}
	extra := s.newPlayer("P11")
	c := s.connect(extra)
	c.send(game.CmdJoin, c.joinRequest(tb.code))
	c.waitFor("lobby_full", isError(game.CodeLobbyFull))

	tb.seats[9].c.send(game.CmdLeave, nil)
	tb.seats[9].c.expectClose(protocol.CloseLeft)
	tb.seats[0].c.waitState("a free seat", func(st *protocol.State) bool { return len(st.View.Players) == 9 })
	if info, _ := s.roomInfo(tb.code); info.Players != 9 || !info.Joinable {
		t.Fatalf("lobby info after a leave = %+v", info)
	}
	st := c.join(tb.code)
	if len(st.View.Players) != 10 || st.View.Players[9].ID != extra.id {
		t.Fatalf("the eleventh player did not get the free seat: %d players", len(st.View.Players))
	}
}

// TestJoiningMidGame: a player who joins during a round has a seat but plays
// from the next round, with 0 points.
func TestJoiningMidGame(t *testing.T) {
	s := newStack(t, stackOpts{})
	tb := newTable(t, s, "Ana", "Beto")
	tb.configure(roundSettings())
	tb.start()
	tb.stopLetter("M")
	tb.answers(0, r1Answers[0][:]...)
	tb.answers(1, r1Answers[1][:]...)

	tb.seats = append(tb.seats, &seat{p: s.newPlayer("Cami")})
	st := tb.reconnect(2)
	cami := tb.seats[2].p
	if len(st.View.Players) != 3 || len(st.View.Round.Participants) != 2 || slices.Contains(st.View.Round.Participants, cami.id) {
		t.Fatalf("mid-round join: players %d participants %v", len(st.View.Players), st.View.Round.Participants)
	}
	if p, _ := seatOf(st.View, cami.id); p.JoinedRound != 2 || p.TotalScore != 0 {
		t.Fatalf("Cami's seat = %+v, want JoinedRound 2 and 0 points", p)
	}
	tb.waitAll("three players", func(v game.View) bool { return len(v.Players) == 3 })
	tb.wantCmdError(2, game.CodeNotParticipant, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Mauro"})
	tb.wantCmdError(2, game.CodeNotParticipant, game.CmdBasta, nil)

	tb.mustCmd(0, game.CmdBasta, nil)
	tb.s.advance(graceFor + lateFor)
	tb.waitPhase(game.PhaseReview)
	tb.wantCmdError(2, game.CodeNotParticipant, game.CmdVoteReject, game.VoteReject{Category: 0, Target: tb.seats[0].p.id})
	tb.mustCmd(0, game.CmdFinishReview, nil)
	tb.waitPhase(game.PhaseRoundSummary)
	v := tb.view(2)
	if _, in := v.History[0].Players[cami.id]; in || v.History[0].Players[tb.seats[0].p.id].Total == 0 {
		t.Fatalf("round 1 result = %+v", v.History[0].Players)
	}
	wantInts(t, "totals", totalScores(v, cami.id), []int{0})

	tb.mustCmd(0, game.CmdNextRound, nil)
	tb.waitPhase(game.PhaseLetter)
	v = tb.view(2)
	if len(v.Round.Participants) != 3 || v.ChooserID != tb.seats[1].p.id {
		t.Fatalf("round 2: participants %v chooser %q, want all three and Beto", v.Round.Participants, v.ChooserID)
	}
	tb.assertNoAnswerLeaks()
}

// TestSnapshotsFollowTheGame: the room is saved on every phase and lobby
// change, and every 5 s while a round is played (answers are not saved one by
// one).
func TestSnapshotsFollowTheGame(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		s := newStack(t, stackOpts{store: open(t)})
		tb := newTable(t, s, "Ana", "Beto", "Cami")
		ids := tb.ids()
		phaseIs := func(want game.Phase) func() bool {
			return func() bool {
				g, ok := snapshot(t, s.store, tb.code)
				return ok && g.Phase == want
			}
		}

		tb.advanceTo(stageLobby)
		eventually(t, "the settings to be saved", func() bool {
			g, ok := snapshot(t, s.store, tb.code)
			return ok && g.Phase == game.PhaseLobby && g.Settings.Rounds == 2 && len(g.Settings.Categories) == 3 && len(g.Players) == 3
		})
		tb.advanceTo(stageLetter)
		eventually(t, "the letter phase to be saved", phaseIs(game.PhaseLetter))

		tb.advanceTo(stagePlaying)
		eventually(t, "the playing phase to be saved", phaseIs(game.PhasePlaying))
		// The answers typed since are only in memory until the next periodic snapshot.
		if g, _ := snapshot(t, s.store, tb.code); len(g.Round.Answers[ids[0]]) != 3 || slices.ContainsFunc(g.Round.Answers[ids[0]], func(a string) bool { return a != "" }) {
			t.Fatalf("answers saved before the periodic snapshot: %v", g.Round.Answers)
		}
		s.advance(5 * time.Second) // SnapshotEvery
		eventually(t, "the periodic snapshot with the answers", func() bool {
			g, _ := snapshot(t, s.store, tb.code)
			return slices.Equal(g.Round.Answers[ids[0]], r1Answers[0][:]) && slices.Equal(g.Round.Answers[ids[2]], r1Answers[2][:])
		})
		// A phase change saves everything again.
		tb.answer(2, 1, "Mapache")
		tb.advanceTo(stageGrace)
		eventually(t, "the grace phase to be saved", func() bool {
			g, ok := snapshot(t, s.store, tb.code)
			return ok && g.Phase == game.PhaseGrace && g.Round.CallerID == ids[0] && g.Round.Answers[ids[2]][1] == "Mapache"
		})
		tb.advanceTo(stageReview)
		eventually(t, "the review to be saved", phaseIs(game.PhaseReview))
		tb.advanceTo(stageVoted)
		s.advance(5 * time.Second) // votes only mark the review dirty: they are saved by the next periodic snapshot
		eventually(t, "the votes to be saved", func() bool {
			g, _ := snapshot(t, s.store, tb.code)
			return g.Phase == game.PhaseReview && len(g.Round.RejectVotes) == 2 && len(g.Round.Proposals) == 1
		})
		tb.advanceTo(stageSummary)
		eventually(t, "the summary to be saved", func() bool {
			g, _ := snapshot(t, s.store, tb.code)
			return g.Phase == game.PhaseRoundSummary && len(g.History) == 1 && g.Players[0].TotalScore == 25
		})
		tb.mustCmd(0, game.CmdEndGame, nil)
		eventually(t, "the final to be saved", func() bool {
			g, _ := snapshot(t, s.store, tb.code)
			// Cami's extra Mapache makes her the winner: 30 against Ana's 25.
			return g.Phase == game.PhaseFinal && slices.Equal(g.Winners, []string{ids[2]})
		})
	})
}

// TestRestoredRoomIsClaimed: after a restart nobody has to reconnect within
// the 2-minute unclaimed TTL; the idle clock of 30 minutes starts at restore.
func TestRestoredRoomIsClaimed(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		s := newStack(t, stackOpts{store: open(t)})
		tb := newTable(t, s, "Ana", "Beto")
		tb.restart(0)
		next := tb.s

		next.advance(unclaimedTTL + time.Minute)
		if info, ok := next.roomInfo(tb.code); !ok || info.Players != 2 {
			t.Fatalf("restored room after 3 minutes = %+v (found %v)", info, ok)
		}
		next.advance(idleTTL - unclaimedTTL - time.Minute - time.Second)
		if _, ok := next.roomInfo(tb.code); !ok {
			t.Fatal("restored room closed before IdleTTL")
		}
		next.advance(time.Second)
		expectGone(t, next, tb.code)
	})
}

// spyStore records the DeleteOlderThan calls (the janitor's) after they ran.
type spyStore struct {
	store.Store
	mu      sync.Mutex
	cutoffs []time.Time
	saves   []string // "code@UpdatedAt", for failure reports
}

func (s *spyStore) Save(ctx context.Context, sn store.Snapshot) error {
	s.mu.Lock()
	s.saves = append(s.saves, fmt.Sprintf("%s@%s", sn.Code, sn.UpdatedAt.Sub(epoch)))
	s.mu.Unlock()
	return s.Store.Save(ctx, sn)
}

func (s *spyStore) DeleteOlderThan(ctx context.Context, t time.Time) (int64, error) {
	n, err := s.Store.DeleteOlderThan(ctx, t)
	s.mu.Lock()
	s.cutoffs = append(s.cutoffs, t)
	s.mu.Unlock()
	return n, err
}

func (s *spyStore) runs() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.cutoffs...)
}

// TestJanitorKeepsTheSnapshotOfALiveRoom: the hourly janitor removes snapshots
// older than 6 hours; the snapshot of a room that is still open (somebody is
// connected, nothing changed for hours) must survive it, or the room would be
// lost at the next restart.
func TestJanitorKeepsTheSnapshotOfALiveRoom(t *testing.T) {
	spy := &spyStore{Store: store.NewMemory()}
	s := newStack(t, stackOpts{store: spy})
	tb := newTable(t, s, "Ana", "Beto")
	eventually(t, "the snapshot", func() bool { return hasSnapshot(t, s.store, tb.code) })

	// Eight hours, an hour at a time (a single jump would make the janitor and
	// the room's own refresh due at the same instant, in an arbitrary order):
	// the janitor runs every hour and from hour 7 on it would delete a snapshot
	// that was never refreshed.
	for hour := 1; hour <= 8; hour++ {
		before := len(spy.runs())
		s.advance(time.Hour)
		eventually(t, fmt.Sprintf("the janitor run of hour %d", hour), func() bool { return len(spy.runs()) > before })
		// The room refreshes its snapshot every 1.5 h; the write itself is asynchronous
		// (the clock does not wait for it), so wait for it before the next hour.
		eventually(t, fmt.Sprintf("the snapshot to be younger than 2.5 h at hour %d", hour), func() bool {
			snaps, err := s.store.LoadAll(context.Background())
			if err != nil {
				t.Fatalf("LoadAll: %v", err)
			}
			for _, sn := range snaps {
				if sn.Code == tb.code {
					return s.clock.Now().Sub(sn.UpdatedAt) <= 150*time.Minute
				}
			}
			return false
		})
	}
	if runs := spy.runs(); !runs[len(runs)-1].After(epoch.Add(time.Hour)) {
		t.Fatalf("the janitor's last cutoff is %v: it never looked at snapshots older than 6h", runs[len(runs)-1])
	}
	if _, ok := s.roomInfo(tb.code); !ok {
		t.Fatal("the room closed although players are connected")
	}
	if !hasSnapshot(t, s.store, tb.code) {
		spy.mu.Lock()
		defer spy.mu.Unlock()
		cutoffs := make([]string, len(spy.cutoffs))
		for i, c := range spy.cutoffs {
			cutoffs[i] = c.Sub(epoch).String()
		}
		t.Fatalf("the snapshot janitor deleted the snapshot of a room that is still open\nsaves: %v\njanitor cutoffs: %v", spy.saves, cutoffs)
	}
}

// TestLeakCheckSeesARunningServer makes sure the goroutine-leak check that
// every test relies on is not vacuous: it sees the goroutines of a live stack,
// and none is left once the stack is closed.
func TestLeakCheckSeesARunningServer(t *testing.T) {
	s := newStack(t, stackOpts{})
	newTable(t, s, "Ana", "Beto")
	if len(leakedGoroutines()) == 0 {
		t.Fatal("the leak check does not see the goroutines of a running server")
	}
	s.close()
	eventually(t, "every server and client goroutine to exit", func() bool { return len(leakedGoroutines()) == 0 })
}

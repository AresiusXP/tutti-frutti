package integration

import (
	"slices"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

// waitConnected waits until every listed client sees player id with the given
// connection flag.
func waitConnected(t *testing.T, id string, connected bool, clients ...*client) {
	t.Helper()
	for _, c := range clients {
		c.waitState("player connected="+boolStr(connected), func(s *protocol.State) bool {
			p, ok := seatOf(s.View, id)
			return ok && p.Connected == connected
		})
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestReconnectKeepsSeatAnswersAndScore: a player's socket drops, the others see
// them offline, and a new socket with the same cookie and a "join" gets the
// same seat back with everything it had.
func TestReconnectKeepsSeatAnswersAndScore(t *testing.T) {
	cases := []struct {
		name  string
		stage stage
		check func(t *testing.T, tb *table, st *protocol.State)
	}{
		{"lobby", stageLobby, func(t *testing.T, tb *table, st *protocol.State) {
			if st.View.Phase != game.PhaseLobby || len(st.View.Settings.Categories) != 3 {
				t.Fatalf("lobby after reconnect: %+v", st.View.Settings)
			}
		}},
		{"letter", stageLetter, func(t *testing.T, tb *table, st *protocol.State) {
			if st.View.Phase != game.PhaseLetter || st.View.ChooserID != tb.seats[0].p.id || st.View.Round.Letter != "" {
				t.Fatalf("letter phase after reconnect: chooser %q", st.View.ChooserID)
			}
		}},
		{"playing", stagePlaying, func(t *testing.T, tb *table, st *protocol.State) {
			v := st.View
			if v.Phase != game.PhasePlaying || v.Round.Letter != "M" {
				t.Fatalf("playing after reconnect: %s %q", v.Phase, v.Round.Letter)
			}
			if got := v.Round.Answers[tb.seats[1].p.id]; !slices.Equal(got, r1Answers[1][:]) {
				t.Fatalf("Beto's answers after reconnect = %v, want %v", got, r1Answers[1])
			}
			if len(v.Round.Answers) != 1 {
				t.Fatalf("reconnect view carries others' answers: %v", v.Round.Answers)
			}
			if v.Round.Filled[tb.seats[0].p.id] != 3 || v.Round.Filled[tb.seats[2].p.id] != 2 {
				t.Fatalf("progress after reconnect = %v", v.Round.Filled)
			}
			// The seat is fully functional: Beto keeps typing and the others see the progress.
			tb.answer(1, 2, "")
			tb.seats[0].c.waitState("Beto's new progress", func(s *protocol.State) bool { return s.View.Round.Filled[tb.seats[1].p.id] == 2 })
		}},
		{"review", stageVoted, func(t *testing.T, tb *table, st *protocol.State) {
			v := st.View
			if v.Phase != game.PhaseReview || len(v.Round.RejectVotes) != 2 || len(v.Round.Proposals) != 1 {
				t.Fatalf("review after reconnect: %s votes %d proposals %d", v.Phase, len(v.Round.RejectVotes), len(v.Round.Proposals))
			}
			// Votes are evaluated against the connected players: with Beto back his
			// "yes" counts again and the merge passes again.
			wantInts(t, "totals with Beto back", reviewTotals(v, tb.ids()...), r1Voted)
		}},
		{"round summary", stageSummary, func(t *testing.T, tb *table, st *protocol.State) {
			v := st.View
			wantInts(t, "running totals after reconnect", totalScores(v, tb.ids()...), r1Voted)
			if v.Phase != game.PhaseRoundSummary || len(v.History) != 1 {
				t.Fatalf("summary after reconnect: %s history %d", v.Phase, len(v.History))
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStack(t, stackOpts{})
			tb := newTable(t, s, "Ana", "Beto", "Cami")
			tb.advanceTo(tc.stage)
			ana, beto, cami := tb.seats[0], tb.seats[1], tb.seats[2]
			before, _ := seatOf(tb.view(1), beto.p.id)
			playersBefore := tb.view(0).Players

			beto.c.drop()
			waitConnected(t, beto.p.id, false, ana.c, cami.c)
			eventually(t, "the hub to count 2 connections", func() bool { return s.hub.Stats().Connections == 2 })
			if info, ok := s.roomInfo(tb.code); !ok || info.Players != 3 {
				t.Fatalf("info while Beto is offline = %+v (found %v)", info, ok)
			}
			if tc.stage == stageVoted {
				// His yes vote stops counting while he is away: the merge falls through.
				ana.c.waitState("merge unpassed while Beto is offline", func(s *protocol.State) bool {
					return !s.View.Round.Proposals[0].Passed
				})
				wantInts(t, "totals while Beto is offline", reviewTotals(ana.c.view(), tb.ids()...), []int{30, 10, 20})
			}

			st := tb.reconnect(1)
			if st.View.YouID != beto.p.id || st.View.HostID != ana.p.id {
				t.Fatalf("reconnect view: you %q host %q", st.View.YouID, st.View.HostID)
			}
			after, ok := seatOf(st.View, beto.p.id)
			if !ok || !after.Connected || after.JoinedSeq != before.JoinedSeq || after.TotalScore != before.TotalScore || after.Left {
				t.Fatalf("Beto's seat before %+v, after %+v", before, after)
			}
			if got := len(st.View.Players); got != len(playersBefore) {
				t.Fatalf("players after reconnect = %d, want %d", got, len(playersBefore))
			}
			for i, p := range st.View.Players {
				if p.ID != playersBefore[i].ID {
					t.Fatalf("seat order changed: %v vs %v", p.ID, playersBefore[i].ID)
				}
			}
			waitConnected(t, beto.p.id, true, ana.c, cami.c)
			tc.check(t, tb, st)
			if tc.stage == stagePlaying {
				tb.assertNoAnswerLeaks()
			}
		})
	}
}

// TestSecondConnectionReplacesTheFirst: a second live socket of the same
// player takes the seat over; the first one is told (replaced) and closed
// with 4001, and the others never see the player go offline.
func TestSecondConnectionReplacesTheFirst(t *testing.T) {
	s := newStack(t, stackOpts{})
	tb := newTable(t, s, "Ana", "Beto", "Cami")
	tb.advanceTo(stagePlaying)
	ana, beto, cami := tb.seats[0], tb.seats[1], tb.seats[2]
	old := cami.c
	marks := tb.marks()

	st := tb.reconnect(2)
	fresh := cami.c
	if fresh == old {
		t.Fatal("reconnect did not open a new socket")
	}
	if got := st.View.Round.Answers[cami.p.id]; !slices.Equal(got, r1Answers[2][:]) || st.View.YouID != cami.p.id {
		t.Fatalf("replacement view: you %q answers %v", st.View.YouID, got)
	}
	old.waitFor("replaced event", isEvent(protocol.EventReplaced))
	old.expectClose(protocol.CloseReplaced)
	for _, st := range []*seat{ana, beto} {
		for _, f := range st.c.snapshotFrames()[marks[tb.index(st.p.id)]:] {
			if f.State == nil {
				continue
			}
			if p, _ := seatOf(f.State.View, cami.p.id); !p.Connected {
				t.Fatalf("%s saw Cami offline during the replacement: %s", st.p.name, f)
			}
		}
	}

	// Once the old transport is fully torn down, its late disconnect must not
	// mark the new connection's seat offline.
	eventually(t, "the old socket's teardown", func() bool {
		return s.metricValue("tuttifrutti_ws_closes_total", map[string]string{"code": "4001"}) == 1
	})
	s.flush()
	if got := s.hub.Stats().Connections; got != 3 {
		t.Fatalf("connections = %d, want 3", got)
	}
	tb.answer(2, 1, "Mapache")
	tb.waitAll("Cami still connected", func(v game.View) bool {
		p, _ := seatOf(v, cami.p.id)
		return p.Connected
	})
	if err := old.trySend(protocol.TypePing, nil); err == nil {
		t.Log("write on the replaced socket did not fail (the close is still in flight)")
	}
	tb.assertNoAnswerLeaks()
}

// TestKick: the host kicks a player; they get "kicked" and close 4002, and the
// seat is gone (lobby) or flagged left (started game). They cannot come back.
func TestKick(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stage stage
	}{{"lobby", stageLobby}, {"playing", stagePlaying}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStack(t, stackOpts{})
			tb := newTable(t, s, "Ana", "Beto", "Cami")
			tb.advanceTo(tc.stage)
			ana, beto, cami := tb.seats[0], tb.seats[1], tb.seats[2]

			tb.wantCmdError(1, game.CodeNotHost, game.CmdKick, game.Kick{PlayerID: cami.p.id})
			tb.wantCmdError(0, game.CodeCannotKickSelf, game.CmdKick, game.Kick{PlayerID: ana.p.id})
			tb.wantCmdError(0, game.CodeUnknownPlayer, game.CmdKick, game.Kick{PlayerID: "nobody"})

			marks := tb.marks()
			if got := tb.cmdExcept(0, []int{2}, game.CmdKick, game.Kick{PlayerID: cami.p.id}); got != "" {
				t.Fatalf("kick rejected: %s", got)
			}
			// The kicked player hears about it, then the socket closes with 4002.
			ev := cami.c.waitFor("kicked event", isEvent(game.EventKicked)).Event
			if ev.PlayerID != cami.p.id {
				t.Fatalf("kicked event = %+v", ev)
			}
			cami.c.expectClose(protocol.CloseKicked)
			for _, st := range []*seat{ana, beto} {
				e, ok := findEvent(st.c.events(marks[tb.index(st.p.id)]), game.EventKicked)
				if !ok || e.PlayerID != cami.p.id {
					t.Fatalf("%s: kicked event = %+v (found %v)", st.p.name, e, ok)
				}
				v := st.c.view()
				p, present := seatOf(v, cami.p.id)
				switch tc.stage {
				case stageLobby:
					if present || len(v.Players) != 2 {
						t.Fatalf("%s: kicked player still in the lobby: %+v", st.p.name, v.Players)
					}
				default:
					if !present || !p.Left || p.Connected {
						t.Fatalf("%s: kicked player's seat = %+v (present %v), want left", st.p.name, p, present)
					}
				}
			}
			if info, _ := s.roomInfo(tb.code); info.Players != 2 {
				t.Fatalf("info players after the kick = %d, want 2", info.Players)
			}

			// The ban holds: the same cookie cannot join again, and nothing changes for the others.
			marks = tb.marks()
			again := s.connect(cami.p)
			again.send(game.CmdJoin, again.joinRequest(tb.code))
			again.waitFor("kicked error", func(f frame) bool {
				return f.Err != nil && f.Err.Code == game.CodeKicked && f.Err.Ref == game.CmdJoin
			})
			again.expectPong() // the connection stays open
			if code := again.roundTrip(game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Mauro"}); code != protocol.ErrNotJoined {
				t.Fatalf("a banned connection sent set_answer: %q, want not_joined", code)
			}
			s.flush()
			for _, st := range []*seat{ana, beto} {
				if n := st.c.mark() - marks[tb.index(st.p.id)]; n != 0 {
					t.Fatalf("%s received %d frames because of the banned join attempt", st.p.name, n)
				}
			}
			if info, _ := s.roomInfo(tb.code); info.Players != 2 {
				t.Fatalf("info players after the banned join = %d", info.Players)
			}
		})
	}
}

// TestLeave: a player leaves on purpose and is closed with 4005; in a started
// game the seat is kept (flagged left) and can be reclaimed.
func TestLeave(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stage stage
	}{{"lobby", stageLobby}, {"playing", stagePlaying}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStack(t, stackOpts{})
			tb := newTable(t, s, "Ana", "Beto", "Cami")
			tb.advanceTo(tc.stage)
			ana, beto, cami := tb.seats[0], tb.seats[1], tb.seats[2]
			seatBefore, _ := seatOf(tb.view(0), beto.p.id)

			beto.c.send(game.CmdLeave, nil)
			beto.c.expectClose(protocol.CloseLeft)
			for _, st := range []*seat{ana, cami} {
				st.c.waitState("Beto gone", func(s *protocol.State) bool {
					p, present := seatOf(s.View, beto.p.id)
					if tc.stage == stageLobby {
						return !present && len(s.View.Players) == 2
					}
					return present && p.Left && !p.Connected
				})
			}
			if info, _ := s.roomInfo(tb.code); info.Players != 2 || info.Phase != tb.anyView().Phase {
				t.Fatalf("info after the leave = %+v", info)
			}
			eventually(t, "connections to drop to 2", func() bool { return s.hub.Stats().Connections == 2 })

			// Coming back.
			st := tb.reconnect(1)
			back, present := seatOf(st.View, beto.p.id)
			if !present || back.Left || !back.Connected {
				t.Fatalf("seat after rejoining = %+v (present %v)", back, present)
			}
			switch tc.stage {
			case stageLobby: // a new seat at the end of the list
				if back.JoinedSeq <= seatBefore.JoinedSeq || len(st.View.Players) != 3 || st.View.Players[2].ID != beto.p.id {
					t.Fatalf("lobby rejoin: seat %+v, players %d", back, len(st.View.Players))
				}
			default: // the old seat, answers included
				if back.JoinedSeq != seatBefore.JoinedSeq || !slices.Equal(st.View.Round.Answers[beto.p.id], r1Answers[1][:]) {
					t.Fatalf("playing rejoin: seat %+v answers %v", back, st.View.Round.Answers[beto.p.id])
				}
			}
			waitConnected(t, beto.p.id, true, ana.c, cami.c)
		})
	}
}

// TestHostLeavesAndTheHostChanges: when the host leaves, the connected player
// who has been connected the longest becomes host (host_changed).
func TestHostLeavesAndTheHostChanges(t *testing.T) {
	cases := []struct {
		name string
		// reconnectBeto makes Beto drop and come back after Cami joined, so he
		// is connected more recently than Cami.
		reconnectBeto bool
		stage         stage
		wantHost      int
	}{
		{"lobby, earliest connection wins", false, stageLobby, 1},
		{"lobby, a reconnect loses seniority", true, stageLobby, 2},
		{"playing, earliest connection wins", false, stagePlaying, 1},
		{"playing, a reconnect loses seniority", true, stagePlaying, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStack(t, stackOpts{})
			tb := newTableGap(t, s, time.Second, "Ana", "Beto", "Cami")
			tb.advanceTo(tc.stage)
			ana, beto, cami := tb.seats[0], tb.seats[1], tb.seats[2]
			if tc.reconnectBeto {
				beto.c.drop()
				waitConnected(t, beto.p.id, false, ana.c, cami.c)
				s.advance(time.Second)
				tb.reconnect(1)
				waitConnected(t, beto.p.id, true, ana.c, cami.c)
			}
			newHost := tb.seats[tc.wantHost]
			other := tb.seats[3-tc.wantHost] // the one that stays a plain player

			marks := tb.marks()
			ana.c.send(game.CmdLeave, nil)
			ana.c.expectClose(protocol.CloseLeft)
			for _, st := range []*seat{newHost, other} {
				st.c.waitState("new host", func(s *protocol.State) bool { return s.View.HostID == newHost.p.id })
				e, ok := findEvent(st.c.events(marks[tb.index(st.p.id)]), game.EventHostChanged)
				if !ok || e.PlayerID != newHost.p.id {
					t.Fatalf("%s: host_changed = %+v (found %v), want %s", st.p.name, e, ok, newHost.p.name)
				}
			}
			if tc.stage == stagePlaying {
				if p, present := seatOf(newHost.c.view(), ana.p.id); !present || !p.Left {
					t.Fatalf("the old host's seat = %+v (present %v), want left", p, present)
				}
			}

			// The new host has the powers: host-only commands work for them, not for the rest.
			if tc.stage == stageLobby {
				settings := roundSettings()
				settings.Rounds = 3
				if code := tb.cmd(tc.wantHost, game.CmdUpdateSettings, game.UpdateSettings{Settings: settings}); code != "" {
					t.Fatalf("the new host cannot update the settings: %s", code)
				}
				if got := tb.cmd(3-tc.wantHost, game.CmdUpdateSettings, game.UpdateSettings{Settings: settings}); got != game.CodeNotHost {
					t.Fatalf("a plain player updating the settings: %q, want not_host", got)
				}
			}

			// The old host coming back in a started game is a plain player now.
			if tc.stage == stagePlaying {
				st := tb.reconnect(0)
				if st.View.HostID != newHost.p.id {
					t.Fatalf("host after the old host rejoined = %q, want %s", st.View.HostID, newHost.p.name)
				}
				if got := tb.cmd(0, game.CmdUpdateSettings, game.UpdateSettings{Settings: roundSettings()}); got != game.CodeNotHost {
					t.Fatalf("the old host's update_settings: %q, want not_host", got)
				}
			}
		})
	}
}

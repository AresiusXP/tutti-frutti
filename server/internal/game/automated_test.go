package game

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func addAutomated(t *testing.T, g *Game, id string) {
	t.Helper()
	if events, err := g.AddAutomated(t0.Add(10*time.Second), Player{ID: id, Name: "Alex", Avatar: Avatar{Emoji: "🍎", Color: "#e11"}}); err != nil || len(events) != 0 {
		t.Fatalf("AddAutomated(%q) = %v, %v", id, events, err)
	}
}

func automatedLobby(t *testing.T) *Game {
	t.Helper()
	g := newLobby(t, 1)
	addAutomated(t, g, "bot:1")
	addAutomated(t, g, "bot:2")
	configure(t, g, threeCategories)
	return g
}

func automatedPlaying(t *testing.T) *Game {
	t.Helper()
	g := automatedLobby(t)
	apply(t, g, tStart, "p1", StartGame{})
	apply(t, g, tStart, "p1", StopLetter{})
	return g
}

func automatedReview(t *testing.T) (*Game, time.Time) {
	t.Helper()
	g := automatedPlaying(t)
	fill(t, g, tStart, "p1", "Ana", "Argentina", "Alce")
	fill(t, g, tStart, "bot:1", "Anna", "Alemania", "Alce")
	fill(t, g, tStart, "bot:2", "Alba", "Angola", "Ardilla")
	return g, lockRound(t, g, tStart.Add(time.Second), "bot:1")
}

func TestAddAutomatedValidationIsAtomic(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*testing.T) *Game
		player Player
		want   error
	}{
		{"missing prefix", automatedLobby, Player{ID: "p2", Name: "Alex"}, ErrUnknownPlayer},
		{"empty suffix", automatedLobby, Player{ID: "bot:", Name: "Alex"}, ErrUnknownPlayer},
		{"control character", automatedLobby, Player{ID: "bot:\x00", Name: "Alex"}, ErrUnknownPlayer},
		{"format character", automatedLobby, Player{ID: "bot:\u200b", Name: "Alex"}, ErrUnknownPlayer},
		{"duplicate", automatedLobby, Player{ID: "bot:1", Name: "Alex"}, ErrUnknownPlayer},
		{"invalid name", automatedLobby, Player{ID: "bot:3"}, ErrInvalidName},
		{"invalid avatar", automatedLobby, Player{ID: "bot:3", Name: "Alex", Avatar: Avatar{Emoji: "\x00"}}, ErrInvalidAvatar},
		{"full lobby", func(t *testing.T) *Game { return newLobby(t, MaxPlayers) }, Player{ID: "bot:3", Name: "Alex"}, ErrLobbyFull},
		{"started game", automatedPlaying, Player{ID: "bot:3", Name: "Alex"}, ErrWrongPhase},
		{"banned", func(t *testing.T) *Game {
			g := automatedLobby(t)
			apply(t, g, t0.Add(20*time.Second), "p1", Kick{PlayerID: "bot:2"})
			return g
		}, Player{ID: "bot:2", Name: "Alex"}, ErrKicked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.setup(t)
			before := snapshot(t, g)
			events, err := g.AddAutomated(tStart, tt.player)
			if !errors.Is(err, tt.want) || len(events) != 0 {
				t.Errorf("AddAutomated = %v, %v; want no events, %v", events, err, tt.want)
			}
			if after := snapshot(t, g); after != before {
				t.Errorf("failed addition changed game:\nbefore %s\nafter %s", before, after)
			}
		})
	}

	g := newLobby(t, 1)
	_, err := g.AddAutomated(t0, Player{ID: "bot:1", Name: "  Alex  ", TotalScore: 999, Left: true, JoinedSeq: 99})
	if err != nil {
		t.Fatal(err)
	}
	p := playerByID(t, g, "bot:1")
	if !g.IsAutomated(p.ID) || p.Name != "Alex" || !p.Connected || p.Left || p.TotalScore != 0 || p.JoinedSeq != 2 || p.JoinedRound != 1 {
		t.Errorf("automated seat does not follow normal join rules: %+v", p)
	}
}

func TestClientCannotJoinOrReclaimAutomated(t *testing.T) {
	g := automatedLobby(t)
	for _, id := range []string{"bot:new", "bot:1", "bot:"} {
		expectErr(t, g, tStart, id, Join{Name: "Intruder"}, ErrNotParticipant)
	}
	apply(t, g, tStart, "p1", StartGame{})
	apply(t, g, tStart, "bot:1", Leave{})
	expectErr(t, g, tStart, "bot:1", Join{Name: "Intruder"}, ErrNotParticipant)
	if !g.IsAutomated("bot:1") || !playerByID(t, g, "bot:1").Left {
		t.Fatal("leaving a started game must retain the automated seat for history")
	}

	// The persisted marker is authoritative even for an older non-reserved id.
	h := newLobby(t, 2)
	h.Automated = []string{"p2"}
	expectErr(t, h, tStart, "p2", Join{Name: "Intruder"}, ErrNotParticipant)
}

func TestAutomatedNeverHost(t *testing.T) {
	for _, phase := range []Phase{PhaseLobby, PhasePlaying} {
		for _, fallback := range []string{"none", "connected", "disconnected"} {
			t.Run(fmt.Sprintf("%s/%s", phase, fallback), func(t *testing.T) {
				g := automatedLobby(t)
				wantHost := ""
				if fallback != "none" {
					apply(t, g, tStart, "p2", Join{Name: "Human"})
					wantHost = "p2"
					if fallback == "disconnected" {
						setConnected(t, g, tStart, "p2", false)
					}
				}
				if phase == PhasePlaying {
					apply(t, g, tStart, "p1", StartGame{})
					apply(t, g, tStart, "p1", StopLetter{})
				}
				events := apply(t, g, tStart, "p1", Leave{})
				if e, ok := hasEvent(events, EventHostChanged); !ok || e.PlayerID != wantHost || g.HostID != wantHost {
					t.Fatalf("host = %q, events = %v, want %q", g.HostID, events, wantHost)
				}
				if fallback == "none" {
					setConnected(t, g, tStart, "bot:1", false)
					setConnected(t, g, tStart, "bot:1", true)
					if phase == PhaseLobby {
						addAutomated(t, g, "bot:3")
					}
					if g.HostID != "" {
						t.Fatalf("automated seat acquired empty host role: %q", g.HostID)
					}
					apply(t, g, tStart, "p1", Join{Name: "Human"})
					if g.HostID != "p1" {
						t.Fatalf("human did not acquire empty host role: %q", g.HostID)
					}
				}
			})
		}
	}
}

func TestAutomatedReviewHumanElectorate(t *testing.T) {
	g, now := automatedReview(t)
	assertStrings(t, "participants", g.Round.Participants, []string{"p1", "bot:1", "bot:2"})
	assertStrings(t, "review voters", g.View("p1").Round.ReviewVoters, []string{"p1"})
	for _, cmd := range []Command{
		VoteReject{Category: 0, Target: "p1"},
		ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "bot:1"},
		VoteMerge{ProposalID: 1, Yes: true},
	} {
		expectErr(t, g, now, "bot:1", cmd, ErrNotParticipant)
	}
	expectErr(t, g, now, "p1", VoteReject{Category: 0, Target: "p1"}, ErrSelfVote)
	apply(t, g, now, "p1", VoteReject{Category: 0, Target: "bot:2"})
	if c := cell(t, liveResult(t, g), "bot:2", 0); c.Status != StatusRejected || c.Points != 0 {
		t.Errorf("sole human vote should reject automated answer: %+v", c)
	}
	apply(t, g, now, "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "bot:1"})
	if pr := g.View("p1").Round.Proposals[0]; !pr.Passed || pr.Yes != 1 {
		t.Errorf("sole human proposal should pass: %+v", pr)
	}
	for _, id := range []string{"p1", "bot:1"} {
		if c := cell(t, liveResult(t, g), id, 0); !c.Shared || c.Points != 5 {
			t.Errorf("merged answer of %s = %+v", id, c)
		}
	}
	setConnected(t, g, now, "p1", false)
	if v := g.View("p1"); len(v.Round.ReviewVoters) != 0 || v.Round.Proposals[0].Passed || v.Round.Proposals[0].Yes != 0 {
		t.Errorf("disconnected human still counted: %+v", v.Round)
	}
	apply(t, g, now, "spectator", Join{Name: "Spectator"})
	if v := viewJSON(t, g, "spectator"); !strings.Contains(v, `"reviewVoters":[]`) {
		t.Errorf("empty human electorate must be an explicit list: %s", v)
	}
	if c := cell(t, liveResult(t, g), "bot:2", 0); c.Status != StatusValid {
		t.Errorf("disconnected human's reject vote still counted: %+v", c)
	}
	setConnected(t, g, now, "p1", true)
	apply(t, g, now, "p1", FinishReview{})
	if p := playerByID(t, g, "bot:1"); p.TotalScore != 20 {
		t.Errorf("automated score = %d, want 20", p.TotalScore)
	}
	apply(t, g, now, "p1", NextRound{})
	if g.ChooserID != "bot:1" {
		t.Fatalf("automated participant skipped as chooser: %q", g.ChooserID)
	}
	apply(t, g, now, "bot:1", StopLetter{})
}

func TestAutomatedStopHumanElectorate(t *testing.T) {
	t.Run("empty electorate", func(t *testing.T) {
		g := automatedPlaying(t)
		expectErr(t, g, tStart, "bot:1", AskStop{}, ErrNotParticipant)
		expectErr(t, g, tStart, "bot:1", VoteStop{Yes: true}, ErrNotParticipant)
		events := apply(t, g, tStart, "p1", AskStop{})
		if len(events) != 1 || events[0].Type != EventBasta || g.Phase != PhaseGrace || g.Round.CallerID != "p1" || g.Round.StopVote != nil {
			t.Errorf("empty human electorate must immediately pass: events=%v, round=%+v", events, g.Round)
		}
	})
	t.Run("only humans may vote", func(t *testing.T) {
		g := automatedLobby(t)
		apply(t, g, tStart, "p2", Join{Name: "Human"})
		apply(t, g, tStart, "p1", StartGame{})
		apply(t, g, tStart, "p1", StopLetter{})
		apply(t, g, tStart, "p1", AskStop{})
		assertStrings(t, "stop voters", g.View("p1").Round.StopVote.Voters, []string{"p2"})
		for _, yes := range []bool{false, true} {
			expectErr(t, g, tStart, "bot:1", VoteStop{Yes: yes}, ErrNotParticipant)
		}
		apply(t, g, tStart, "p2", VoteStop{Yes: true})
		if g.Phase != PhaseGrace {
			t.Fatalf("automated participants blocked stop vote: %s", g.Phase)
		}
	})
}

func TestReviewVotersJoinOrderAndVisibility(t *testing.T) {
	g := automatedLobby(t)
	for _, id := range []string{"p3", "p2"} {
		apply(t, g, tStart, id, Join{Name: "Human"})
	}
	apply(t, g, tStart, "p1", StartGame{})
	apply(t, g, tStart, "p1", StopLetter{})
	if !strings.Contains(viewJSON(t, g, "p1"), `"reviewVoters":[]`) {
		t.Fatal("review electorate must be an empty list before review")
	}
	fill(t, g, tStart, "p1", ansP1...)
	now := lockRound(t, g, tStart, "p1")
	apply(t, g, now, "spectator", Join{Name: "Spectator"})
	assertStrings(t, "review voters in join order", g.View("spectator").Round.ReviewVoters, []string{"p1", "p3", "p2"})
	view := g.View("p1")
	view.Round.ReviewVoters[0] = "mutated"
	assertStrings(t, "view owns its electorate", g.View("p1").Round.ReviewVoters, []string{"p1", "p3", "p2"})
	setConnected(t, g, now, "p3", false)
	apply(t, g, now, "p2", Leave{})
	assertStrings(t, "live review electorate", g.View("p1").Round.ReviewVoters, []string{"p1"})
	apply(t, g, now, "p1", FinishReview{})
	if !strings.Contains(viewJSON(t, g, "p1"), `"reviewVoters":[]`) {
		t.Fatal("review electorate must be an empty list after review")
	}
}

func TestAutomatedSeatCleanup(t *testing.T) {
	for _, kicked := range []bool{false, true} {
		t.Run(fmt.Sprintf("lobby/kicked=%v", kicked), func(t *testing.T) {
			g := automatedLobby(t)
			if kicked {
				apply(t, g, tStart, "p1", Kick{PlayerID: "bot:1"})
			} else {
				apply(t, g, tStart, "bot:1", Leave{})
			}
			if g.IsAutomated("bot:1") || g.seat("bot:1") != nil || !g.IsAutomated("bot:2") {
				t.Fatalf("lobby removal did not clean only removed seat: %s", snapshot(t, g))
			}
		})
	}
	g, now := automatedReview(t)
	apply(t, g, now, "p1", FinishReview{})
	apply(t, g, now, "bot:1", Leave{})
	if !g.IsAutomated("bot:1") || g.History[0].Players["bot:1"].Total != 25 || !playerByID(t, g, "bot:1").Left {
		t.Fatal("started-game removal lost automated identity or history")
	}
	apply(t, g, now, "p1", EndGame{})
	apply(t, g, now, "p1", Rematch{})
	assertStrings(t, "automated after rematch", g.Automated, []string{"bot:2"})
	assertStrings(t, "seats after rematch", playerIDs(g), []string{"p1", "bot:2"})
}

func TestAutomatedJSONRoundTrip(t *testing.T) {
	g := automatedLobby(t)
	check := func() {
		t.Helper()
		before := snapshot(t, g)
		v := viewJSON(t, g, "p1")
		g = restore(t, g)
		if after := snapshot(t, g); before != after || !g.IsAutomated("bot:1") || !g.IsAutomated("bot:2") || g.IsAutomated("p1") {
			t.Fatalf("automated identities did not survive %s round trip", g.Phase)
		}
		if v != viewJSON(t, g, "p1") || strings.Contains(v, `"automated"`) {
			t.Fatalf("view changed or exposed internal automated field in %s: %s", g.Phase, v)
		}
		if g.Round != nil && g.Phase != PhaseReview && !strings.Contains(v, `"reviewVoters":[]`) {
			t.Fatalf("review electorate must be an empty list in %s: %s", g.Phase, v)
		}
		if !strings.Contains(before, `"automated":["bot:1","bot:2"]`) {
			t.Fatal("snapshot omitted automated identities")
		}
	}
	check()
	apply(t, g, tStart, "p1", StartGame{})
	check()
	apply(t, g, tStart, "p1", StopLetter{})
	check()
	fill(t, g, tStart, "bot:1", ansP1...)
	apply(t, g, tStart, "bot:1", Basta{})
	check()
	now := tStart.Add(GraceDuration + LateUpdateAllowance)
	g.Tick(now)
	check()
	apply(t, g, now, "p1", VoteReject{Category: 0, Target: "bot:1"})
	check()
	apply(t, g, now, "p1", FinishReview{})
	check()
	apply(t, g, now, "p1", EndGame{})
	check()
	apply(t, g, now, "p1", Rematch{})
	check()
}

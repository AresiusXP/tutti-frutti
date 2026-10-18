package game

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func restore(t *testing.T, g *Game) *Game {
	t.Helper()
	b, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var out Game
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, b)
	}
	return &out
}

func viewJSON(t *testing.T, g *Game, viewer string) string {
	t.Helper()
	b, err := json.Marshal(g.View(viewer))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// gameInPhase builds a game of three players frozen in the given phase.
func gameInPhase(t *testing.T, name string) *Game {
	t.Helper()
	switch name {
	case "lobby":
		g := newLobby(t, 3)
		configure(t, g, func(s *Settings) {
			s.Lang = LangEN
			s.TimeLimitSeconds = 90
			s.Categories = []CategoryRef{{ID: "pais"}, {Custom: "Cosas rojas"}, {ID: "animal"}}
		})
		return g
	case "letter":
		g := newLobby(t, 3)
		apply(t, g, tStart, "p1", StartGame{})
		return g
	case "playing":
		g := newPlaying(t, 3, threeCategories)
		fill(t, g, tStart.Add(time.Second), "p1", "Ana", "Argentina")
		return g
	case "playing with time limit":
		return newPlaying(t, 3, func(s *Settings) { s.TimeLimitSeconds = 60; threeCategories(s) })
	case "grace":
		g := newPlaying(t, 3, threeCategories)
		fill(t, g, tStart.Add(time.Second), "p1", "Ana", "Argentina", "Alce")
		apply(t, g, tStart.Add(time.Second), "p1", Basta{})
		return g
	case "grace by time limit":
		g := newPlaying(t, 3, func(s *Settings) { s.TimeLimitSeconds = 60; threeCategories(s) })
		g.Tick(tStart.Add(60 * time.Second))
		return g
	case "review":
		g, now := newReview(t, ansP1, []string{"Anna", "Alemania", "Ardilla"}, ansP1)
		apply(t, g, now, "p1", ReviewNav{Category: 1})
		apply(t, g, now, "p2", VoteReject{Category: 1, Target: "p1"})
		apply(t, g, now, "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p2"})
		apply(t, g, now, "p3", VoteMerge{ProposalID: 1, Yes: false})
		setConnected(t, g, now, "p3", false)
		return g
	case "round_summary":
		g, now := newReview(t, ansP1, ansP1, ansP1)
		apply(t, g, now, "p1", FinishReview{})
		return g
	case "final":
		g, now := newReview(t, ansP1, []string{"Alba", "Angola", "Ardilla"}, ansP1)
		apply(t, g, now, "p1", FinishReview{})
		apply(t, g, now, "p1", EndGame{})
		return g
	case "after rematch":
		g := gameInPhase(t, "final")
		apply(t, g, tStart, "p1", Rematch{})
		return g
	}
	t.Fatalf("unknown phase %q", name)
	return nil
}

func TestJSONRoundTripInEveryPhase(t *testing.T) {
	phases := []struct {
		name  string
		phase Phase
	}{
		{"lobby", PhaseLobby},
		{"letter", PhaseLetter},
		{"playing", PhasePlaying},
		{"playing with time limit", PhasePlaying},
		{"grace", PhaseGrace},
		{"grace by time limit", PhaseGrace},
		{"review", PhaseReview},
		{"round_summary", PhaseRoundSummary},
		{"final", PhaseFinal},
		{"after rematch", PhaseLobby},
	}
	for _, tt := range phases {
		t.Run(tt.name, func(t *testing.T) {
			g := gameInPhase(t, tt.name)
			if g.Phase != tt.phase {
				t.Fatalf("setup: phase = %s, want %s", g.Phase, tt.phase)
			}

			first := snapshot(t, g)
			if !strings.Contains(first, fmt.Sprintf(`"phase":%q`, tt.phase)) {
				t.Errorf("phase name missing from %s", first)
			}
			g2 := restore(t, g)
			if second := snapshot(t, g2); second != first {
				t.Errorf("marshal -> unmarshal -> marshal differs:\n first %s\nsecond %s", first, second)
			}

			// What clients see and when the next timer fires survive too.
			for _, viewer := range []string{"p1", "p2", "p3", "nobody"} {
				if a, b := viewJSON(t, g, viewer), viewJSON(t, g2, viewer); a != b {
					t.Errorf("view of %s differs:\n%s\n%s", viewer, a, b)
				}
			}
			d1, ok1 := g.NextDeadline()
			d2, ok2 := g2.NextDeadline()
			if ok1 != ok2 || !d1.Equal(d2) {
				t.Errorf("NextDeadline %v,%v became %v,%v", d1, ok1, d2, ok2)
			}
		})
	}
}

func TestJSONRoundTripKeepsTheGamePlayable(t *testing.T) {
	// The room layer snapshots on every phase change and reloads on restart:
	// restoring after every command must not change how a game unfolds.
	script := func(t *testing.T, g *Game, roundTrip bool) *Game {
		step := func(now time.Time, actor string, cmd Command) {
			t.Helper()
			apply(t, g, now, actor, cmd)
			if roundTrip {
				g = restore(t, g)
			}
		}
		tick := func(now time.Time) {
			t.Helper()
			g.Tick(now)
			if roundTrip {
				g = restore(t, g)
			}
		}

		step(t0.Add(time.Second), "p1", UpdateSettings{Settings: Settings{
			Lang: LangES, ExcludeHardLetters: true, Rounds: 2, TimeLimitSeconds: 60,
			Categories: []CategoryRef{{ID: "nombre"}, {ID: "pais"}, {Custom: "Cosas rojas"}},
		}})
		step(tStart, "p1", StartGame{})
		step(tStart.Add(ms(240)), "p1", StopLetter{}) // C
		now := tStart.Add(5 * time.Second)
		for _, a := range []struct {
			p       string
			answers []string
		}{
			{"p1", []string{"Carla", "Chile", "Cereza"}},
			{"p2", []string{"Carla", "Canadá", "Cereza"}},
			{"p3", []string{"Camila", "Chile", "Coral"}},
		} {
			for i, text := range a.answers {
				step(now, a.p, SetAnswer{Category: i, Text: text})
			}
		}
		step(now, "p3", Basta{})
		tick(now.Add(GraceDuration + LateUpdateAllowance))
		review := now.Add(GraceDuration + LateUpdateAllowance)
		step(review, "p1", ReviewNav{Category: 2})
		step(review, "p1", VoteReject{Category: 0, Target: "p3"})
		step(review, "p2", VoteReject{Category: 0, Target: "p3"})
		step(review, "p1", ProposeMerge{Category: 2, PlayerA: "p1", PlayerB: "p3"})
		step(review, "p2", VoteMerge{ProposalID: 1, Yes: true})
		step(review, "p1", FinishReview{})
		step(review, "p1", NextRound{})
		setConnected(t, g, review.Add(ChooserTimeout), "p2", false) // letter auto-stops here
		if roundTrip {
			g = restore(t, g)
		}
		lim, _ := g.NextDeadline()
		tick(lim.Add(LateUpdateAllowance)) // time limit passes, nobody answered
		step(lim.Add(time.Second), "p1", FinishReview{})
		step(lim.Add(time.Second), "p1", NextRound{})
		step(lim.Add(time.Second), "p1", Rematch{})
		return g
	}

	plain := script(t, newLobby(t, 3), false)
	restored := script(t, newLobby(t, 3), true)
	if a, b := snapshot(t, plain), snapshot(t, restored); a != b {
		t.Errorf("a game restored after every command diverged:\n plain %s\n restored %s", a, b)
	}
	if plain.Phase != PhaseLobby {
		t.Errorf("script ended in %s", plain.Phase)
	}
}

func TestJSONShapeOfTheGame(t *testing.T) {
	// Spot-check the field names a client (or tygo) relies on.
	g := gameInPhase(t, "playing")
	b, _ := json.Marshal(g.View("p1"))
	for _, key := range []string{
		`"phase":"playing"`, `"youId":"p1"`, `"hostId":"p1"`, `"maxPlayers":10`, `"categoryLabels":`,
		`"usedLetters":["A"]`, `"roundNum":1`, `"chooserId":"p1"`, `"round":{"number":1,"letter":"A"`,
		`"participants":["p1","p2","p3"]`, `"filled":{`, `"joinedRound":1`, `"totalScore":0`,
	} {
		if !strings.Contains(string(b), key) {
			t.Errorf("view JSON is missing %s:\n%s", key, b)
		}
	}
	e, _ := json.Marshal(Event{Type: EventBasta, PlayerID: "p1", Name: "Ana"})
	if string(e) != `{"type":"basta","playerId":"p1","name":"Ana"}` {
		t.Errorf("event JSON = %s", e)
	}
}

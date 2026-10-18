package game

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

// t0 is the base instant of every test; tests pass explicit times derived
// from it and never sleep.
var t0 = time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)

// tStart is when the tests start games.
var tStart = t0.Add(time.Minute)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func pid(i int) string { return fmt.Sprintf("p%d", i) }

// newLobby returns a lobby of n connected players p1 (host) ... pn. Player pK
// joined K seconds after t0.
func newLobby(t *testing.T, n int) *Game {
	t.Helper()
	g := NewGame(Player{ID: pid(1), Name: "Player 1", Avatar: Avatar{Emoji: "🍎", Color: "#e11"}}, t0)
	for i := 2; i <= n; i++ {
		apply(t, g, t0.Add(time.Duration(i)*time.Second), pid(i), Join{Name: fmt.Sprintf("Player %d", i), Avatar: Avatar{Emoji: "🍌", Color: "#ee1"}})
	}
	return g
}

// apply runs a command that must succeed.
func apply(t *testing.T, g *Game, now time.Time, actor string, cmd Command) []Event {
	t.Helper()
	events, err := g.Apply(now, actor, cmd)
	if err != nil {
		t.Fatalf("Apply(%s, %T) = %v", actor, cmd, err)
	}
	return events
}

// setConnected reports a connection change that must succeed.
func setConnected(t *testing.T, g *Game, now time.Time, id string, connected bool) []Event {
	t.Helper()
	events, err := g.SetConnected(now, id, connected)
	if err != nil {
		t.Fatalf("SetConnected(%s, %v) = %v", id, connected, err)
	}
	return events
}

// expectErr runs a command that must fail with want and leave the game
// untouched.
func expectErr(t *testing.T, g *Game, now time.Time, actor string, cmd Command, want error) {
	t.Helper()
	before := snapshot(t, g)
	events, err := g.Apply(now, actor, cmd)
	if !errors.Is(err, want) {
		t.Fatalf("Apply(%s, %#v) = %v, want %v", actor, cmd, err, want)
	}
	if len(events) != 0 {
		t.Errorf("a failed command must not emit events, got %v", events)
	}
	if after := snapshot(t, g); after != before {
		t.Errorf("a failed command changed the state:\nbefore %s\n after %s", before, after)
	}
}

func snapshot(t *testing.T, g *Game) string {
	t.Helper()
	b, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func eventTypes(events []Event) []EventType {
	out := make([]EventType, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

func hasEvent(events []Event, typ EventType) (Event, bool) {
	for _, e := range events {
		if e.Type == typ {
			return e, true
		}
	}
	return Event{}, false
}

func threeCategories(s *Settings) { s.Categories = ids("nombre", "pais", "animal") }

// configure sets the lobby settings as the host would.
func configure(t *testing.T, g *Game, mod func(*Settings)) {
	t.Helper()
	s := DefaultSettings()
	if mod != nil {
		mod(&s)
	}
	apply(t, g, t0.Add(30*time.Second), g.HostID, UpdateSettings{Settings: s})
}

// newPlaying returns a game of n players whose first round is in the playing
// phase with letter "A" (the chooser stops the alphabet immediately at
// tStart). mod adjusts the settings.
func newPlaying(t *testing.T, n int, mod func(*Settings)) *Game {
	t.Helper()
	g := newLobby(t, n)
	configure(t, g, mod)
	apply(t, g, tStart, "p1", StartGame{})
	apply(t, g, tStart, "p1", StopLetter{})
	if g.Phase != PhasePlaying || g.Round.Letter != "A" {
		t.Fatalf("setup: phase %s letter %q", g.Phase, g.Round.Letter)
	}
	return g
}

// fill sets a player's answers for the first len(answers) categories.
func fill(t *testing.T, g *Game, now time.Time, player string, answers ...string) {
	t.Helper()
	for i, a := range answers {
		apply(t, g, now, player, SetAnswer{Category: i, Text: a})
	}
}

// lockRound makes caller call basta at now and advances past the lock plus
// the late-update allowance. It returns the instant the review began.
func lockRound(t *testing.T, g *Game, now time.Time, caller string) time.Time {
	t.Helper()
	apply(t, g, now, caller, Basta{})
	end := now.Add(GraceDuration + LateUpdateAllowance)
	g.Tick(end)
	if g.Phase != PhaseReview {
		t.Fatalf("setup: phase after lock = %s", g.Phase)
	}
	return end
}

// newReview returns a game in the review phase of round 1 (letter "A") with
// three categories (nombre, país, animal). answers maps p1..pn to their three
// answers; p1 calls basta so p1's answers must be valid. It returns the game
// and the review start time.
func newReview(t *testing.T, answers ...[]string) (*Game, time.Time) {
	t.Helper()
	g := newPlaying(t, len(answers), threeCategories)
	for i, a := range answers {
		fill(t, g, tStart.Add(time.Second), pid(i+1), a...)
	}
	return g, lockRound(t, g, tStart.Add(2*time.Second), "p1")
}

func cell(t *testing.T, res RoundResult, player string, category int) Cell {
	t.Helper()
	pr, ok := res.Players[player]
	if !ok {
		t.Fatalf("result has no player %s", player)
	}
	return pr.Cells[category]
}

// liveResult is the result the viewer would see in the review.
func liveResult(t *testing.T, g *Game) RoundResult {
	t.Helper()
	v := g.View("p1")
	if v.Round == nil || v.Round.Result == nil {
		t.Fatalf("no result in view (phase %s)", v.Phase)
	}
	return *v.Round.Result
}

func playerByID(t *testing.T, g *Game, id string) Player {
	t.Helper()
	for _, p := range g.Players {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("no player %s", id)
	return Player{}
}

func playerIDs(g *Game) []string {
	var out []string
	for _, p := range g.Players {
		out = append(out, p.ID)
	}
	return out
}

func assertStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

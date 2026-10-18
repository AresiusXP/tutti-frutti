package game

import (
	"testing"
	"time"
)

// finishRound plays a round from the letter phase: the current chooser stops
// the alphabet at once (the first available letter), every player fills in
// answers[player], the first player with a complete answer set calls basta,
// and the host finishes the review. It returns the time after the review.
func finishRound(t *testing.T, g *Game, now time.Time, answers map[string][]string) time.Time {
	t.Helper()
	apply(t, g, now, g.ChooserID, StopLetter{})
	now = now.Add(time.Second)
	var caller string
	for _, id := range g.Round.Participants {
		fill(t, g, now, id, answers[id]...)
		if caller == "" && len(answers[id]) == len(g.Settings.Categories) && g.allAnswered(id) {
			caller = id
		}
	}
	if caller == "" {
		t.Fatal("setup: nobody can call basta")
	}
	now = lockRound(t, g, now.Add(time.Second), caller)
	apply(t, g, now, g.HostID, FinishReview{})
	return now
}

func TestFullGameHappyPath(t *testing.T) {
	g := newLobby(t, 3)
	configure(t, g, func(s *Settings) {
		s.Rounds = 2
		s.Categories = []CategoryRef{{ID: "nombre"}, {ID: "pais"}, {Custom: "Cosas rojas"}}
	})
	apply(t, g, tStart, "p1", StartGame{})
	if g.ChooserID != "p1" {
		t.Fatalf("round 1 chooser = %s, want the host", g.ChooserID)
	}

	// Round 1, letter A.
	now := finishRound(t, g, tStart, map[string][]string{
		"p1": {"Ana", "Argentina", "Amapola"},
		"p2": {"Ana", "Alemania", "Ají"},
		"p3": {"Andrés", "Argentina", "Amapola"},
	})
	if g.Phase != PhaseRoundSummary || g.Round.Letter != "A" {
		t.Fatalf("phase %s letter %s", g.Phase, g.Round.Letter)
	}
	wantTotals := map[string]int{"p1": 15, "p2": 25, "p3": 20}
	for id, want := range wantTotals {
		if got := playerByID(t, g, id).TotalScore; got != want {
			t.Errorf("after round 1 %s = %d, want %d", id, got, want)
		}
	}

	expectErr(t, g, now, "p2", NextRound{}, ErrNotHost)
	events := apply(t, g, now, "p1", NextRound{})
	if e, ok := hasEvent(events, EventRoundStarted); !ok || e.Round != 2 || e.PlayerID != "p2" {
		t.Errorf("events = %+v, want round_started for round 2 with chooser p2", events)
	}
	if g.Phase != PhaseLetter || g.RoundNum != 2 || g.ChooserID != "p2" {
		t.Fatalf("phase %s round %d chooser %s", g.Phase, g.RoundNum, g.ChooserID)
	}
	if g.Round.Letter != "" || len(g.Round.RejectVotes) != 0 || len(g.Round.Proposals) != 0 {
		t.Errorf("round state was not reset: %+v", g.Round)
	}
	expectErr(t, g, now, "p1", StopLetter{}, ErrNotYourTurn)

	// Round 2, letter B (A is used). p3 leaves one category empty, p1 has a
	// wrong-letter answer.
	now = finishRound(t, g, now, map[string][]string{
		"p1": {"Beto", "Brasil", "Rojo"},
		"p2": {"Beto", "Bolivia", "Bandera"},
		"p3": {"Beto", "", "Bandera"},
	})
	if g.Round.Letter != "B" {
		t.Fatalf("round 2 letter = %q, want B", g.Round.Letter)
	}
	// p1: nombre 5 (3 Beto) + pais 10 + "Rojo" wrong letter 0 = 15
	// p2: 5 + 10 + 5 (Bandera x2) = 20     p3: 5 + 0 + 5 = 10
	wantTotals = map[string]int{"p1": 30, "p2": 45, "p3": 30}
	for id, want := range wantTotals {
		if got := playerByID(t, g, id).TotalScore; got != want {
			t.Errorf("after round 2 %s = %d, want %d", id, got, want)
		}
	}
	assertStrings(t, "UsedLetters", g.UsedLetters, []string{"A", "B"})
	if len(g.History) != 2 || g.History[1].Round != 2 {
		t.Errorf("history = %+v", g.History)
	}

	// The configured number of rounds has been played: next_round ends the game.
	events = apply(t, g, now, "p1", NextRound{})
	if g.Phase != PhaseFinal {
		t.Fatalf("phase = %s, want final", g.Phase)
	}
	assertStrings(t, "winners", g.Winners, []string{"p2"})
	if e, ok := hasEvent(events, EventGameOver); !ok || len(e.Winners) != 1 || e.Winners[0] != "p2" {
		t.Errorf("events = %+v", events)
	}
	expectErr(t, g, now, "p1", NextRound{}, ErrWrongPhase)
	expectErr(t, g, now, "p1", EndGame{}, ErrWrongPhase)
}

func TestChooserRotation(t *testing.T) {
	g := newLobby(t, 3)
	configure(t, g, func(s *Settings) { s.Rounds = 0; threeCategories(s) })
	apply(t, g, tStart, "p1", StartGame{})

	var chosen []string
	now := tStart
	for round := 1; round <= 4; round++ {
		chosen = append(chosen, g.ChooserID)
		apply(t, g, now, g.ChooserID, StopLetter{})
		for _, id := range g.Round.Participants {
			fill(t, g, now, id, g.Round.Letter+"a", g.Round.Letter+"b", g.Round.Letter+"c")
		}
		now = lockRound(t, g, now.Add(time.Second), "p1")
		apply(t, g, now, "p1", FinishReview{})
		apply(t, g, now, "p1", NextRound{})
	}
	assertStrings(t, "choosers", chosen, []string{"p1", "p2", "p3", "p1"})
}

func TestChooserRotationSkipsPlayersWhoLeft(t *testing.T) {
	g := newLobby(t, 4)
	configure(t, g, func(s *Settings) { s.Rounds = 0; threeCategories(s) })
	apply(t, g, tStart, "p1", StartGame{})

	next := func(now time.Time) time.Time {
		apply(t, g, now, g.ChooserID, StopLetter{})
		for _, id := range g.Round.Participants {
			fill(t, g, now, id, g.Round.Letter+"a", g.Round.Letter+"b", g.Round.Letter+"c")
		}
		now = lockRound(t, g, now.Add(time.Second), g.Round.Participants[0])
		apply(t, g, now, g.HostID, FinishReview{})
		return now
	}

	now := next(tStart) // round 1, chooser p1
	apply(t, g, now, "p1", Kick{PlayerID: "p2"})
	apply(t, g, now, "p3", Leave{})
	apply(t, g, now, "p1", NextRound{})
	if g.ChooserID != "p4" {
		t.Errorf("round 2 chooser = %s, want p4 (p2 was kicked, p3 left)", g.ChooserID)
	}

	// p4 (the last) is followed by the first remaining player.
	now = next(now)
	apply(t, g, now, "p1", NextRound{})
	if g.ChooserID != "p1" {
		t.Errorf("round 3 chooser = %s, want p1", g.ChooserID)
	}

	// The previous chooser leaving does not break the rotation either.
	now = next(now)
	apply(t, g, now, "p1", Leave{})
	if g.HostID != "p4" {
		t.Fatalf("host = %s, want p4", g.HostID)
	}
	apply(t, g, now, "p4", NextRound{})
	if g.ChooserID != "p4" {
		t.Errorf("round 4 chooser = %s, want p4", g.ChooserID)
	}
	assertStrings(t, "participants", g.Round.Participants, []string{"p4"})
}

func TestLeftParticipantStillScoresInTheRound(t *testing.T) {
	g := newPlaying(t, 3, threeCategories)
	now := tStart.Add(time.Second)
	fill(t, g, now, "p1", "Ana", "Argentina", "Alce")
	fill(t, g, now, "p2", "Ana", "Alemania", "Ardilla")
	fill(t, g, now, "p3", "Alba", "Angola", "Alacran")
	apply(t, g, now, "p3", Leave{})
	now = lockRound(t, g, now, "p1")

	// p3 left, but its answers are part of the round (and cannot vote).
	if got := cell(t, liveResult(t, g), "p3", 0); got.Points != 10 {
		t.Errorf("p3 = %+v", got)
	}
	expectErr(t, g, now, "p3", VoteReject{Category: 0, Target: "p1"}, ErrUnknownPlayer)
	apply(t, g, now, "p1", FinishReview{})
	if got := playerByID(t, g, "p3").TotalScore; got != 30 {
		t.Errorf("p3 total = %d, want 30", got)
	}
}

func TestEndGameFromSummary(t *testing.T) {
	g := newLobby(t, 2)
	configure(t, g, threeCategories)
	apply(t, g, tStart, "p1", StartGame{})
	now := finishRound(t, g, tStart, map[string][]string{
		"p1": {"Ana", "Argentina", "Alce"},
		"p2": {"Alba", "Angola", ""},
	})

	expectErr(t, g, now, "p2", EndGame{}, ErrNotHost)
	events := apply(t, g, now, "p1", EndGame{})
	if g.Phase != PhaseFinal {
		t.Fatalf("phase = %s", g.Phase)
	}
	assertStrings(t, "winners", g.Winners, []string{"p1"})
	if _, ok := hasEvent(events, EventGameOver); !ok {
		t.Errorf("events = %+v", events)
	}
	// Only the summary may end the game.
	h := newPlaying(t, 2, nil)
	expectErr(t, h, tStart.Add(time.Second), "p1", EndGame{}, ErrWrongPhase)
	expectErr(t, h, tStart.Add(time.Second), "p1", NextRound{}, ErrWrongPhase)
}

func TestTiesGiveSeveralWinnersAndLeaversCannotWin(t *testing.T) {
	g := newLobby(t, 3)
	configure(t, g, threeCategories)
	apply(t, g, tStart, "p1", StartGame{})
	now := finishRound(t, g, tStart, map[string][]string{
		"p1": {"Ana", "Argentina", "Alce"},
		"p2": {"Ana", "Argentina", "Alce"},
		"p3": {"Alba", "Angola", "Ardilla"}, // 30, the highest
	})
	apply(t, g, now, "p3", Leave{})
	apply(t, g, now, "p1", EndGame{})
	assertStrings(t, "winners", g.Winners, []string{"p1", "p2"})

	// Everybody on zero points ties too.
	g = newLobby(t, 2)
	apply(t, g, tStart, "p1", StartGame{})
	g.Phase = PhaseRoundSummary // jump straight to the summary for brevity
	apply(t, g, tStart, "p1", EndGame{})
	assertStrings(t, "winners", g.Winners, []string{"p1", "p2"})
}

func TestGameEndsWhenLettersRunOut(t *testing.T) {
	g := newLobby(t, 2)
	configure(t, g, func(s *Settings) { s.Rounds = 0; threeCategories(s) })
	apply(t, g, tStart, "p1", StartGame{})

	// Leave only one playable letter: V is the last of the 20.
	g.UsedLetters = AvailableLetters(LangES, true, []string{"V"})
	apply(t, g, tStart, "p1", StopLetter{}) // any elapsed time maps to V
	if g.Round.Letter != "V" {
		t.Fatalf("letter = %q, want V", g.Round.Letter)
	}
	for _, id := range g.Round.Participants {
		fill(t, g, tStart, id, "Vaca", "Venezuela", "Vicuña")
	}
	now := lockRound(t, g, tStart.Add(time.Second), "p1")
	apply(t, g, now, "p1", FinishReview{})

	// Unlimited rounds, but no letters remain: next_round goes to the final.
	events := apply(t, g, now, "p1", NextRound{})
	if g.Phase != PhaseFinal {
		t.Fatalf("phase = %s, want final", g.Phase)
	}
	if _, ok := hasEvent(events, EventGameOver); !ok {
		t.Errorf("events = %+v", events)
	}
	if len(g.UsedLetters) != 20 {
		t.Errorf("used letters = %d", len(g.UsedLetters))
	}
}

func TestRoundLimitMatchesAvailableLetters(t *testing.T) {
	// Rounds=20 is accepted with the 20 playable letters; the 20th round
	// is the last and the game then ends.
	g := newLobby(t, 2)
	configure(t, g, func(s *Settings) { s.Rounds = 20; threeCategories(s) })
	apply(t, g, tStart, "p1", StartGame{})
	now := tStart
	for round := 1; round <= 20; round++ {
		if g.Phase != PhaseLetter || g.RoundNum != round {
			t.Fatalf("round %d: phase %s round %d", round, g.Phase, g.RoundNum)
		}
		apply(t, g, now, g.ChooserID, StopLetter{})
		for _, id := range g.Round.Participants {
			fill(t, g, now, id, g.Round.Letter+"a", g.Round.Letter+"b", g.Round.Letter+"c")
		}
		now = lockRound(t, g, now.Add(time.Second), "p1")
		apply(t, g, now, "p1", FinishReview{})
		apply(t, g, now, "p1", NextRound{})
	}
	if g.Phase != PhaseFinal || len(g.History) != 20 {
		t.Errorf("phase %s history %d", g.Phase, len(g.History))
	}
	seen := map[string]bool{}
	for _, l := range g.UsedLetters {
		if seen[l] {
			t.Errorf("letter %s was played twice", l)
		}
		seen[l] = true
	}
}

func TestRematch(t *testing.T) {
	for _, lang := range Langs() {
		t.Run(string(lang), func(t *testing.T) { testRematch(t, lang) })
	}
}

func testRematch(t *testing.T, lang Lang) {
	g := newLobby(t, 4)
	configure(t, g, func(s *Settings) {
		s.Lang = lang
		s.Rounds = 2
		s.TimeLimitSeconds = 90
		s.Categories = []CategoryRef{{ID: "pais"}, {Custom: "Cosas rojas"}, {ID: "animal"}}
	})
	apply(t, g, tStart, "p1", StartGame{})
	now := finishRound(t, g, tStart, map[string][]string{
		"p1": {"Argentina", "Apple", "Ant"},
		"p2": {"Argentina", "Ambulance", "Ape"},
		"p3": {"Armenia", "Ant", "Ape"},
		"p4": {"Austria", "Ash", "Ant"},
	})
	apply(t, g, now, "p4", Leave{})
	apply(t, g, now, "p1", Kick{PlayerID: "p3"})
	apply(t, g, now, "p1", EndGame{})
	if g.Phase != PhaseFinal {
		t.Fatalf("phase = %s", g.Phase)
	}

	expectErr(t, g, now, "p2", Rematch{}, ErrNotHost)
	lobby := newLobby(t, 2)
	expectErr(t, lobby, now, "p1", Rematch{}, ErrWrongPhase)

	events := apply(t, g, now, "p1", Rematch{})
	if _, ok := hasEvent(events, EventRematch); !ok {
		t.Errorf("events = %+v", events)
	}
	if g.Phase != PhaseLobby || g.RoundNum != 0 || g.Round != nil || g.ChooserID != "" {
		t.Errorf("phase %s round %d round-state %v chooser %q", g.Phase, g.RoundNum, g.Round, g.ChooserID)
	}
	if len(g.UsedLetters) != 0 || len(g.History) != 0 || len(g.Winners) != 0 {
		t.Errorf("letters %v history %v winners %v", g.UsedLetters, g.History, g.Winners)
	}
	assertStrings(t, "players", playerIDs(g), []string{"p1", "p2"}) // p3 was kicked, p4 left
	for _, p := range g.Players {
		if p.TotalScore != 0 || p.JoinedRound != 1 || p.Left {
			t.Errorf("player not reset: %+v", p)
		}
	}
	if g.Settings.Lang != lang || g.Settings.Rounds != 2 || g.Settings.TimeLimitSeconds != 90 ||
		len(g.Settings.Categories) != 3 || g.Settings.Categories[1].Custom != "Cosas rojas" {
		t.Errorf("settings were not kept: %+v", g.Settings)
	}
	if _, ok := g.NextDeadline(); ok {
		t.Error("the lobby has no deadline")
	}

	// Kicked players stay banned, players who left may join again, and the
	// settings are editable again.
	expectErr(t, g, now, "p3", Join{Name: "x"}, ErrKicked)
	apply(t, g, now, "p4", Join{Name: "Back"})
	apply(t, g, now, "p1", UpdateSettings{Settings: DefaultSettings()})

	// A second game starts cleanly with the host choosing first.
	apply(t, g, now, "p1", StartGame{})
	if g.RoundNum != 1 || g.ChooserID != "p1" || g.Phase != PhaseLetter {
		t.Errorf("phase %s round %d chooser %s", g.Phase, g.RoundNum, g.ChooserID)
	}
	apply(t, g, now, "p1", StopLetter{})
	if g.Round.Letter != "A" {
		t.Errorf("letters must be fresh after a rematch, got %q", g.Round.Letter)
	}
}

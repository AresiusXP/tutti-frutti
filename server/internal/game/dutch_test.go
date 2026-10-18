package game

import (
	"strings"
	"testing"
	"time"
)

// dutchSettings is the lobby the Dutch tests play in: the three first
// categories of the catalog classics, labelled in Dutch.
func dutchSettings(rounds int) func(*Settings) {
	return func(s *Settings) {
		s.Lang = LangNL
		s.Rounds = rounds
		s.Categories = ids("nombre", "pais", "animal")
	}
}

// A whole game in a Dutch lobby: the alphabet and hard letters of nl, Dutch
// labels in every view, "IJsland" and "Ĳsland" counting as the same answer
// for the letter I, "Ysland" not being an I, and the language surviving
// snapshots and the rematch.
func TestDutchGame(t *testing.T) {
	g := newLobby(t, 3)
	configure(t, g, dutchSettings(2))
	if v := g.View("p2"); v.Settings.Lang != LangNL {
		t.Fatalf("lobby language = %q", v.Settings.Lang)
	} else {
		assertStrings(t, "labels", v.CategoryLabels, []string{"Naam", "Land", "Dier"})
	}
	apply(t, g, tStart, "p1", StartGame{})

	// The 9th of the 23 playable letters (Q, X and Y are excluded) is the I.
	letters := AvailableLetters(LangNL, true, nil)
	if len(letters) != 23 || letters[8] != "I" {
		t.Fatalf("letters = %v", letters)
	}
	apply(t, g, tStart.Add(8*LetterTickDuration), "p1", StopLetter{})
	if g.Phase != PhasePlaying || g.Round.Letter != "I" {
		t.Fatalf("phase %s letter %q, want playing with I", g.Phase, g.Round.Letter)
	}
	assertStrings(t, "used letters", g.UsedLetters, []string{"I"})

	now := tStart.Add(10 * time.Second)
	fill(t, g, now, "p1", "Ida", "IJsland", "Ijsvogel")
	fill(t, g, now, "p2", "Ilse", "\u0132sland", "Impala")
	fill(t, g, now, "p3", "Ivo", "Ysland", "\u0133svogel")
	if got := g.Round.Answers["p2"][1]; got != "\u0132sland" {
		t.Errorf("the answer is stored as typed, got %q", got)
	}
	// Ysland is no I: p3 cannot call basta, p2 (Ĳsland) can.
	expectErr(t, g, now, "p3", Basta{}, ErrBastaNotAllowed)
	now = lockRound(t, g, now.Add(time.Second), "p2")

	res := liveResult(t, g)
	wantCells := map[string][]cellWant{
		"p1": {unique, shared, shared},
		"p2": {unique, shared, unique},
		"p3": {unique, wrong, shared},
	}
	for p, wants := range wantCells {
		for i, w := range wants {
			if got := cell(t, res, p, i); got.Status != w.status || got.Points != w.points || got.Shared != w.shared {
				t.Errorf("%s category %d = %+v, want %+v", p, i, got, w)
			}
		}
	}
	if c := cell(t, res, "p2", 1); c.Raw != "\u0132sland" || c.Answer != "ijsland" {
		t.Errorf("p2 land = %+v", c)
	}
	if c := cell(t, res, "p3", 2); c.Raw != "\u0133svogel" || c.Answer != "ijsvogel" {
		t.Errorf("p3 dier = %+v", c)
	}

	// The game keeps going after a snapshot taken in the middle of the review.
	g = restore(t, g)
	apply(t, g, now, "p1", FinishReview{})
	for id, want := range map[string]int{"p1": 20, "p2": 25, "p3": 15} {
		if got := playerByID(t, g, id).TotalScore; got != want {
			t.Errorf("after round 1 %s = %d, want %d", id, got, want)
		}
	}
	apply(t, g, now, "p1", NextRound{})

	// Round 2: the I is used and Q, X, Y never come up, so the alphabet from
	// A on is the first letter again.
	g = restore(t, g)
	if g.Settings.Lang != LangNL || g.ChooserID != "p2" {
		t.Fatalf("restored lobby language %q chooser %s", g.Settings.Lang, g.ChooserID)
	}
	now = finishRound(t, g, now, map[string][]string{
		"p1": {"Anna", "Argentini\u00eb", "Aap"},
		"p2": {"Aad", "Angola", "Alpaca"},
		"p3": {"Anna", "Azerbeidzjan", "Aap"},
	})
	if g.Round.Letter != "A" {
		t.Fatalf("round 2 letter = %q, want A", g.Round.Letter)
	}
	assertStrings(t, "used letters", g.UsedLetters, []string{"I", "A"})
	// p1: 5 + 10 + 5   p2: 10 + 10 + 10   p3: 5 + 10 + 5
	for id, want := range map[string]int{"p1": 40, "p2": 55, "p3": 35} {
		if got := playerByID(t, g, id).TotalScore; got != want {
			t.Errorf("after round 2 %s = %d, want %d", id, got, want)
		}
	}
	apply(t, g, now, "p1", NextRound{})
	if g.Phase != PhaseFinal {
		t.Fatalf("phase = %s, want final", g.Phase)
	}
	assertStrings(t, "winners", g.Winners, []string{"p2"})

	// The rematch keeps the Dutch lobby.
	g = restore(t, g)
	apply(t, g, now, "p1", Rematch{})
	if g.Phase != PhaseLobby || g.Settings.Lang != LangNL || g.Settings.Rounds != 2 {
		t.Fatalf("after the rematch: phase %s settings %+v", g.Phase, g.Settings)
	}
	assertStrings(t, "labels", g.View("p3").CategoryLabels, []string{"Naam", "Land", "Dier"})
	apply(t, g, now, "p1", StartGame{})
	apply(t, g, now, "p1", StopLetter{})
	if g.Round.Letter != "A" {
		t.Errorf("letters must start again from A, got %q", g.Round.Letter)
	}
}

// With every letter allowed the Dutch game may land on Q, X and Y; the last
// letter of the alphabet is Z and nothing else (no Ñ).
func TestDutchLettersWithHardLettersAllowed(t *testing.T) {
	g := newLobby(t, 2)
	configure(t, g, func(s *Settings) {
		dutchSettings(26)(s)
		s.ExcludeHardLetters = false
	})
	apply(t, g, tStart, "p1", StartGame{})
	// Q is the 17th letter of the plain alphabet.
	apply(t, g, tStart.Add(16*LetterTickDuration), "p1", StopLetter{})
	if g.Round.Letter != "Q" {
		t.Fatalf("letter = %q, want Q", g.Round.Letter)
	}
	// Rounds = 26 is the limit; one more is refused.
	over := DefaultSettings()
	over.Lang, over.ExcludeHardLetters, over.Rounds = LangNL, false, 27
	if err := over.Validate(); err == nil {
		t.Error("27 rounds must not fit the 26-letter Dutch alphabet")
	}
}

// "Ĳsland" and "IJsland" match on their own, no merge vote needed, and a
// rejection of one of them leaves the other unique.
func TestDutchLigatureAnswersMatchWithoutAMerge(t *testing.T) {
	g := newLobby(t, 2)
	configure(t, g, dutchSettings(1))
	apply(t, g, tStart, "p1", StartGame{})
	apply(t, g, tStart.Add(8*LetterTickDuration), "p1", StopLetter{})
	if g.Round.Letter != "I" {
		t.Fatalf("letter = %q, want I", g.Round.Letter)
	}
	fill(t, g, tStart.Add(time.Second), "p1", "Ida", "IJsland", "Ierland")
	fill(t, g, tStart.Add(time.Second), "p2", "Ida", "\u0132sland", "Ijsland")
	now := lockRound(t, g, tStart.Add(2*time.Second), "p1")
	res := liveResult(t, g)
	for _, p := range []string{"p1", "p2"} {
		if c := cell(t, res, p, 1); c.Status != StatusValid || !c.Shared || c.Points != PointsShared {
			t.Errorf("%s land = %+v, want a shared valid answer", p, c)
		}
	}
	if c := cell(t, res, "p2", 2); c.Answer != "ijsland" || c.Points != PointsUnique {
		t.Errorf("p2 dier = %+v", c)
	}
	apply(t, g, now, "p1", VoteReject{Category: 1, Target: "p2"})
	res = liveResult(t, g)
	if c := cell(t, res, "p1", 1); c.Points != PointsUnique || c.Shared {
		t.Errorf("p1 land after the rejection = %+v", c)
	}
	if c := cell(t, res, "p2", 1); c.Status != StatusRejected {
		t.Errorf("p2 land after the rejection = %+v", c)
	}
}

func TestDutchViewJSON(t *testing.T) {
	g := newLobby(t, 2)
	configure(t, g, dutchSettings(5))
	for _, viewer := range []string{"p1", "p2"} {
		b := viewJSON(t, g, viewer)
		for _, want := range []string{`"lang":"nl"`, `"categoryLabels":["Naam","Land","Dier"]`} {
			if !strings.Contains(b, want) {
				t.Errorf("view of %s is missing %s: %s", viewer, want, b)
			}
		}
	}
}

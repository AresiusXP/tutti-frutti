package game

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestViewHidesOthersAnswersWhilePlaying(t *testing.T) {
	g := newPlaying(t, 3, threeCategories)
	now := tStart.Add(time.Second)
	fill(t, g, now, "p1", "Ana", "Argentina", "Alce")
	fill(t, g, now, "p2", "Alba", "", "")
	apply(t, g, now, "p4", Join{Name: "Spectator"})

	for _, viewer := range []string{"p1", "p2", "p3", "p4", "stranger"} {
		v := g.View(viewer)
		if v.Phase != PhasePlaying || v.YouID != viewer || v.Round == nil {
			t.Fatalf("%s: phase %s you %s", viewer, v.Phase, v.YouID)
		}
		if v.Round.Letter != "A" {
			t.Errorf("%s: letter = %q", viewer, v.Round.Letter)
		}
		for owner, answers := range v.Round.Answers {
			if owner != viewer {
				t.Errorf("%s can see %s's answers %v", viewer, owner, answers)
			}
		}
		if v.Round.Result != nil {
			t.Errorf("%s: no result is visible while playing", viewer)
		}
		// Progress is public.
		if got := v.Round.Filled; got["p1"] != 3 || got["p2"] != 1 || got["p3"] != 0 {
			t.Errorf("%s: filled = %v", viewer, got)
		}
	}
	if got := g.View("p1").Round.Answers["p1"]; !reflect.DeepEqual(got, []string{"Ana", "Argentina", "Alce"}) {
		t.Errorf("p1 own answers = %v", got)
	}
	if v := g.View("p4"); len(v.Round.Answers) != 0 {
		t.Errorf("a spectator sees answers: %v", v.Round.Answers)
	}

	// The raw answers of other players must not appear anywhere in the JSON.
	b, err := json.Marshal(g.View("p3"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"Argentina", "Alba", "Alce"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("p3's view leaks %q: %s", secret, b)
		}
	}
}

func TestViewHidesOthersAnswersInGrace(t *testing.T) {
	g := newPlaying(t, 2, threeCategories)
	now := tStart.Add(time.Second)
	fill(t, g, now, "p1", "Ana", "Argentina", "Alce")
	fill(t, g, now, "p2", "Alba", "Angola", "")
	apply(t, g, now, "p1", Basta{})

	v := g.View("p2")
	if v.Phase != PhaseGrace || v.Round.CallerID != "p1" || !v.Round.GraceEndsAt.Equal(now.Add(GraceDuration)) {
		t.Fatalf("view = %+v", v.Round)
	}
	if _, ok := v.Round.Answers["p1"]; ok {
		t.Error("the caller's answers are still hidden during the grace period")
	}
	if got := v.Round.Answers["p2"]; !reflect.DeepEqual(got, []string{"Alba", "Angola", ""}) {
		t.Errorf("own answers = %v", got)
	}
	if v.Round.Filled["p1"] != 3 || v.Round.Filled["p2"] != 2 {
		t.Errorf("filled = %v", v.Round.Filled)
	}
}

func TestViewShowsEverythingInReview(t *testing.T) {
	g, now := newReview(t, ansP1, []string{"Anna", "Alemania", "Ardilla"})
	apply(t, g, now, "p2", VoteReject{Category: 1, Target: "p1"})
	apply(t, g, now, "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p2"})

	for _, viewer := range []string{"p1", "p2", "outsider"} {
		v := g.View(viewer)
		if len(v.Round.Answers) != 2 {
			t.Fatalf("%s sees answers of %d players, want 2", viewer, len(v.Round.Answers))
		}
		if v.Round.Answers["p2"][1] != "Alemania" || v.Round.Answers["p1"][1] != "Argentina" {
			t.Errorf("%s: answers = %v", viewer, v.Round.Answers)
		}
		if v.Round.Result == nil || len(v.Round.Result.Players) != 2 {
			t.Fatalf("%s: result = %+v", viewer, v.Round.Result)
		}
		if len(v.Round.RejectVotes) != 1 || len(v.Round.Proposals) != 1 {
			t.Errorf("%s: votes %v proposals %v", viewer, v.Round.RejectVotes, v.Round.Proposals)
		}
	}

	// Points are live: Ana and Anna are two tens until the table merges them.
	if c := cell(t, liveResult(t, g), "p1", 0); c.Points != 10 || c.Shared {
		t.Errorf("before the merge: %+v", c)
	}
	apply(t, g, now, "p2", VoteMerge{ProposalID: 1, Yes: true})
	if c := cell(t, liveResult(t, g), "p1", 0); c.Points != 5 || !c.Shared {
		t.Errorf("after the merge: %+v", c)
	}
	// p2 rejected p1's país: the only other player is a majority of one.
	if c := cell(t, liveResult(t, g), "p1", 1); c.Status != StatusRejected || c.Points != 0 {
		t.Errorf("rejected answer: %+v", c)
	}
	if c := cell(t, liveResult(t, g), "p2", 1); c.Points != 10 {
		t.Errorf("p2 país: %+v", c)
	}
}

func TestViewAfterReview(t *testing.T) {
	g, now := newReview(t, ansP1, ansP1)
	apply(t, g, now, "p1", FinishReview{})
	v := g.View("p2")
	if v.Phase != PhaseRoundSummary || v.Round.Result == nil || v.Round.Result.Players["p1"].Total != 15 {
		t.Fatalf("summary view = %+v", v.Round)
	}
	if len(v.Round.Answers) != 2 {
		t.Errorf("answers are visible in the summary, got %v", v.Round.Answers)
	}
	if len(v.Round.RejectVotes) != 0 || len(v.Round.Proposals) != 0 {
		t.Error("votes are only sent during the review")
	}
	if len(v.History) != 1 || v.History[0].Players["p2"].Total != 15 {
		t.Errorf("history = %+v", v.History)
	}

	apply(t, g, now, "p1", EndGame{})
	v = g.View("p2")
	if v.Phase != PhaseFinal || v.Round.Result == nil || !reflect.DeepEqual(v.Winners, []string{"p1", "p2"}) {
		t.Errorf("final view: phase %s winners %v", v.Phase, v.Winners)
	}
}

func TestViewLetterPhaseRevealsNothing(t *testing.T) {
	g := newLobby(t, 3)
	apply(t, g, tStart, "p1", StartGame{})

	for _, viewer := range []string{"p1", "p2"} {
		v := g.View(viewer)
		if v.Phase != PhaseLetter || v.ChooserID != "p1" || v.Round == nil || v.Round.Letter != "" {
			t.Fatalf("%s: view = %+v", viewer, v)
		}
		if !v.Round.GraceEndsAt.IsZero() || !v.Round.TimeLimitEndsAt.IsZero() {
			t.Errorf("%s: no deadline may be sent during the letter phase", viewer)
		}
		b, _ := json.Marshal(v)
		// Neither the start time of the alphabet nor the deadline (which would
		// give it away) may be sent: the chooser could compute the letter.
		for _, leak := range []string{"letterStartedAt", tStart.Format(time.RFC3339), tStart.Add(ChooserTimeout).Format(time.RFC3339)} {
			if strings.Contains(string(b), leak) {
				t.Errorf("%s: view leaks %q: %s", viewer, leak, b)
			}
		}
	}

	// The letter is revealed by the transition to playing, together with the
	// updated used letters.
	apply(t, g, tStart.Add(ms(240)), "p1", StopLetter{})
	v := g.View("p2")
	if v.Round.Letter != "C" || len(v.UsedLetters) != 1 || v.UsedLetters[0] != "C" {
		t.Errorf("letter %q used %v", v.Round.Letter, v.UsedLetters)
	}
}

func TestViewLobbyAndMetadata(t *testing.T) {
	g := newLobby(t, 2)
	configure(t, g, func(s *Settings) {
		s.Lang = LangEN
		s.Categories = []CategoryRef{{ID: "pais"}, {Custom: "Cosas rojas"}, {ID: "animal"}}
	})
	v := g.View("p2")
	if v.Phase != PhaseLobby || v.Round != nil || v.HostID != "p1" || v.MaxPlayers != MaxPlayers {
		t.Errorf("lobby view = %+v", v)
	}
	assertStrings(t, "labels", v.CategoryLabels, []string{"Country", "Cosas rojas", "Animal"})
	if len(v.Players) != 2 || v.UsedLetters == nil || v.History == nil {
		t.Errorf("players %v used %v history %v", v.Players, v.UsedLetters, v.History)
	}

	// Empty collections serialise as [] rather than null.
	b, _ := json.Marshal(v)
	for _, field := range []string{`"usedLetters":[]`, `"history":[]`} {
		if !strings.Contains(string(b), field) {
			t.Errorf("JSON is missing %s: %s", field, b)
		}
	}
	empty := NewGame(Player{ID: "x", Name: "X"}, t0)
	empty.Players = nil
	empty.UsedLetters = nil
	if v := empty.View("x"); v.Players == nil || v.UsedLetters == nil {
		t.Error("nil slices must become empty slices in the view")
	}
}

func TestViewSharesNoMemoryWithTheGame(t *testing.T) {
	g, now := newReview(t, ansP1, []string{"Anna", "Alemania", "Ardilla"})
	apply(t, g, now, "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p2"})
	apply(t, g, now, "p2", VoteReject{Category: 1, Target: "p1"})
	before := snapshot(t, g)

	v := g.View("p1")
	v.Players[0].Name = "mutated"
	v.Settings.Categories[0].ID = "mutated"
	v.UsedLetters[0] = "Z"
	v.Round.Participants[0] = "mutated"
	v.Round.Answers["p1"][0] = "mutated"
	v.Round.RejectVotes[0].Voter = "mutated"
	v.Round.Proposals[0].Votes[0].Voter = "mutated"
	v.Round.Result.Players["p1"].Cells[0].Raw = "mutated"
	if after := snapshot(t, g); after != before {
		t.Errorf("mutating a review view changed the game:\nbefore %s\n after %s", before, after)
	}

	apply(t, g, now, "p1", FinishReview{})
	before = snapshot(t, g)
	v = g.View("p1")
	v.Round.Result.Players["p1"].Cells[0].Raw = "mutated"
	v.History[0].Players["p1"].Cells[0].Raw = "mutated"
	v.Winners = append(v.Winners, "mutated")
	if after := snapshot(t, g); after != before {
		t.Errorf("mutating a summary view changed the game:\nbefore %s\n after %s", before, after)
	}
}

func TestViewOfARoundWithoutParticipants(t *testing.T) {
	g := newLobby(t, 2)
	apply(t, g, tStart, "p1", StartGame{})
	g.Round.Participants = nil
	b, _ := json.Marshal(g.View("p1"))
	if !strings.Contains(string(b), `"participants":[]`) {
		t.Errorf("participants must serialise as []: %s", b)
	}
}

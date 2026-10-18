package room

import (
	"slices"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

// roundAnswers are the answers of p1, p2 and p3 for letter "A".
var roundAnswers = [3][3]string{
	{"Ana", "Argentina", "Aguila"},
	{"Alberto", "Austria", "Alce"},
	{"Ana", "Alemania", "Avestruz"},
}

func (l *lobby3) fillAnswers() {
	l.e.t.Helper()
	for i, answers := range roundAnswers {
		for cat, a := range answers {
			l.send(i, game.CmdSetAnswer, game.SetAnswer{Category: cat, Text: a})
		}
	}
}

// finishGame plays one round and ends the game: l ends in the final phase.
func finishGame(t *testing.T, l *lobby3) {
	t.Helper()
	l.startRound(nil)
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	l.send(0, game.CmdFinishReview, nil)
	l.send(0, game.CmdNextRound, nil)
	phase := query(l.e, l.code, func(r *room) game.Phase { return r.g.Phase })
	if phase != game.PhaseFinal {
		t.Fatalf("setup: phase = %s, want final", phase)
	}
	l.drain()
}

// expectFrames checks that each listed client received exactly the frame kinds
// want, and returns the frames.
func (l *lobby3) expectFrames(step string, want []string, clients ...int) [3][]frameRec {
	l.e.t.Helper()
	var out [3][]frameRec
	if len(clients) == 0 {
		clients = []int{0, 1, 2}
	}
	for _, i := range clients {
		out[i] = l.c[i].drain()
		if got := kinds(out[i]); !slices.Equal(got, want) {
			l.e.t.Fatalf("%s: p%d frames = %v, want %v", step, i+1, got, want)
		}
	}
	return out
}

// The whole game over the wire with three clients: lobby, letter, playing,
// basta, grace, review (votes and a merge), summary, final and rematch.
func TestFullGameFrames(t *testing.T) {
	l := newLobby3(t)

	// Lobby: the host sets the game up.
	l.send(0, game.CmdUpdateSettings, threeCats(nil))
	frames := l.expectFrames("update_settings", []string{"state"})
	if got := frames[1][0].state(t).View.Settings; got.Rounds != 1 || len(got.Categories) != 3 {
		t.Errorf("settings = %+v", got)
	}
	l.send(1, game.CmdUpdateSettings, threeCats(nil)) // not the host
	l.expectFrames("update_settings by a guest", []string{"error:not_host"}, 1)
	l.expectFrames("update_settings by a guest (others)", nil, 0, 2)

	// Start: events first, then each player's state.
	l.send(0, game.CmdStartGame, nil)
	frames = l.expectFrames("start_game", []string{"event:game_started", "event:round_started", "state"})
	for i := range l.c {
		if ev := frames[i][1].event(t); ev.Round != 1 || ev.PlayerID != "p1" {
			t.Errorf("round_started = %+v, want round 1 chosen by p1", ev)
		}
		v := frames[i][2].state(t).View
		if v.Phase != game.PhaseLetter || v.ChooserID != "p1" || v.Round.Letter != "" {
			t.Errorf("p%d letter view: phase %s chooser %q letter %q", i+1, v.Phase, v.ChooserID, v.Round.Letter)
		}
	}

	// Only the chooser can stop the alphabet; at +0 ms the letter is A.
	l.send(1, game.CmdStopLetter, nil)
	l.expectFrames("stop_letter by a non-chooser", []string{"error:not_your_turn"}, 1)
	l.send(0, game.CmdStopLetter, nil)
	frames = l.expectFrames("stop_letter", []string{"event:letter_revealed", "state"})
	if ev := frames[2][0].event(t); ev.Letter != "A" {
		t.Errorf("letter_revealed = %+v", ev)
	}
	if v := frames[2][1].state(t).View; v.Phase != game.PhasePlaying || v.Round.Letter != "A" {
		t.Errorf("playing view: phase %s letter %q", v.Phase, v.Round.Letter)
	}

	// Playing: every answer broadcasts a state.
	l.fillAnswers()
	for i, c := range l.c {
		got := c.drain()
		if len(got) != 9 || slices.ContainsFunc(got, func(f frameRec) bool { return f.T != protocol.TypeState }) {
			t.Fatalf("p%d: 9 answers must produce 9 state frames, got %v", i+1, kinds(got))
		}
	}
	l.send(0, game.CmdBasta, nil)
	frames = l.expectFrames("basta", []string{"event:basta", "state"})
	if ev := frames[1][0].event(t); ev.PlayerID != "p1" || ev.Name != "Ana" {
		t.Errorf("basta event = %+v", ev)
	}
	if v := frames[1][1].state(t).View; v.Phase != game.PhaseGrace || v.Round.CallerID != "p1" || v.Round.GraceEndsAt.IsZero() {
		t.Errorf("grace view: phase %s caller %q graceEndsAt %v", v.Phase, v.Round.CallerID, v.Round.GraceEndsAt)
	}

	// A late edit inside the grace period is still accepted.
	l.send(1, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Alberto"})
	l.expectFrames("answer during grace", []string{"state"})

	// The timer locks the round and opens the review.
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	frames = l.expectFrames("round_locked", []string{"event:round_locked", "state"})
	rv := frames[1][1].state(t).View
	if rv.Phase != game.PhaseReview || rv.Round.Answers["p3"][2] != "Avestruz" {
		t.Errorf("review view: phase %s answers %v", rv.Phase, rv.Round.Answers)
	}

	// Review: p2 and p3 reject p1's "Ana" (the author is not an elector), and
	// p3 proposes that "Alce" and "Avestruz" are the same animal.
	l.send(1, game.CmdVoteReject, game.VoteReject{Category: 0, Target: "p1"})
	l.expectFrames("first reject vote", []string{"state"})
	l.send(2, game.CmdVoteReject, game.VoteReject{Category: 0, Target: "p1"})
	frames = l.expectFrames("second reject vote", []string{"state"})
	if cell := frames[0][0].state(t).View.Round.Result.Players["p1"].Cells[0]; cell.Status != game.StatusRejected || cell.Points != 0 {
		t.Errorf("p1's rejected answer = %+v", cell)
	}
	l.send(2, game.CmdProposeMerge, game.ProposeMerge{Category: 2, PlayerA: "p2", PlayerB: "p3"})
	frames = l.expectFrames("propose_merge", []string{"state"})
	props := frames[0][0].state(t).View.Round.Proposals
	if len(props) != 1 || props[0].Yes != 1 || props[0].Passed {
		t.Fatalf("proposals = %+v", props)
	}
	l.send(0, game.CmdVoteMerge, game.VoteMerge{ProposalID: props[0].ID, Yes: true})
	frames = l.expectFrames("vote_merge", []string{"state"})
	if p := frames[1][0].state(t).View.Round.Proposals; len(p) != 1 || !p[0].Passed {
		t.Errorf("proposal after the majority vote: %+v", p)
	}
	l.send(0, game.CmdReviewNav, game.ReviewNav{Category: 2})
	frames = l.expectFrames("review_nav", []string{"state"})
	if got := frames[2][0].state(t).View.Round.ReviewCategory; got != 2 {
		t.Errorf("review category = %d", got)
	}
	l.send(1, game.CmdFinishReview, nil)
	l.expectFrames("finish_review by a guest", []string{"error:not_host"}, 1)

	// Summary: scores are final.
	l.send(0, game.CmdFinishReview, nil)
	frames = l.expectFrames("finish_review", []string{"event:round_scored", "state"})
	sv := frames[0][1].state(t).View
	totals := map[string]int{}
	for _, p := range sv.Players {
		totals[p.ID] = p.TotalScore
	}
	if want := map[string]int{"p1": 20, "p2": 25, "p3": 25}; !mapsEqual(totals, want) {
		t.Errorf("totals = %v, want %v", totals, want)
	}
	if sv.Phase != game.PhaseRoundSummary {
		t.Errorf("phase = %s", sv.Phase)
	}

	// The only round is over: next_round ends the game.
	l.send(0, game.CmdNextRound, nil)
	frames = l.expectFrames("next_round", []string{"event:game_over", "state"})
	if ev := frames[0][0].event(t); !slices.Equal(ev.Winners, []string{"p2", "p3"}) {
		t.Errorf("game_over winners = %v, want the tie p2, p3", ev.Winners)
	}
	if v := frames[0][1].state(t).View; v.Phase != game.PhaseFinal || !slices.Equal(v.Winners, []string{"p2", "p3"}) {
		t.Errorf("final view: phase %s winners %v", v.Phase, v.Winners)
	}

	// Newcomers wait for the rematch.
	late, _ := l.e.join(l.code, "p4", "Dani")
	if got := kinds(late.drain()); !slices.Equal(got, []string{"error:wrong_phase"}) {
		t.Errorf("a newcomer joining in the final phase got %v", got)
	}

	// Rematch: back to the lobby, scores reset, settings kept.
	l.send(0, game.CmdRematch, nil)
	frames = l.expectFrames("rematch", []string{"event:rematch", "state"})
	lv := frames[1][1].state(t).View
	if lv.Phase != game.PhaseLobby || lv.Round != nil || len(lv.Settings.Categories) != 3 {
		t.Errorf("lobby view: phase %s round %v categories %d", lv.Phase, lv.Round, len(lv.Settings.Categories))
	}
	for _, p := range lv.Players {
		if p.TotalScore != 0 {
			t.Errorf("%s kept a score of %d after the rematch", p.ID, p.TotalScore)
		}
	}
	// ... and the newcomer can join now.
	late2, _ := l.e.join(l.code, "p4", "Dani")
	if v := lastState(t, late2.drain()).View; len(v.Players) != 4 {
		t.Errorf("players after the newcomer joined = %d", len(v.Players))
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func TestMultipleRoundsRotateTheChooser(t *testing.T) {
	l := newLobby3(t)
	l.send(0, game.CmdUpdateSettings, threeCats(func(s *game.Settings) { s.Rounds = 2 }))
	l.send(0, game.CmdStartGame, nil)
	l.send(0, game.CmdStopLetter, nil)
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	l.send(0, game.CmdFinishReview, nil)
	l.drain()

	l.send(0, game.CmdNextRound, nil)
	frames := l.expectFrames("next_round", []string{"event:round_started", "state"})
	if ev := frames[0][0].event(t); ev.Round != 2 || ev.PlayerID != "p2" {
		t.Errorf("round_started = %+v, want round 2 chosen by p2", ev)
	}
	// The new letter phase has its own deadline timer.
	want := l.e.clock.Now().Add(game.ChooserTimeout)
	if got := query(l.e, l.code, func(r *room) time.Time { return r.wakeAt }); !got.Equal(want) {
		t.Errorf("wake at %v, want %v", got.Sub(t0), want.Sub(t0))
	}
}

func TestEventsReachEveryoneIncludingTheActor(t *testing.T) {
	l := newLobby3(t)
	l.send(0, game.CmdUpdateSettings, threeCats(nil))
	l.send(0, game.CmdStartGame, nil)
	l.drain()
	l.send(0, game.CmdStopLetter, nil)
	for i, c := range l.c {
		if got := kinds(c.drain()); !slices.Equal(got, []string{"event:letter_revealed", "state"}) {
			t.Errorf("p%d frames = %v", i+1, got)
		}
	}
}

func TestDisconnectDuringReviewChangesTheElectorate(t *testing.T) {
	l := newLobby3(t)
	l.startRound(nil)
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	l.drain()

	// With p3 gone, p2 alone is a majority of the two connected voters
	// excluding the author.
	l.s[2].Disconnect()
	l.e.sync(l.code)
	l.drain()
	l.send(1, game.CmdVoteReject, game.VoteReject{Category: 0, Target: "p1"})
	v := lastState(t, l.c[0].drain()).View
	if cell := v.Round.Result.Players["p1"].Cells[0]; cell.Status != game.StatusRejected {
		t.Errorf("p1's answer = %+v, want rejected by the only other connected voter", cell)
	}
}

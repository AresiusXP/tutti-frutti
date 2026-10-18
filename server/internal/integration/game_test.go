package integration

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

// eventTypes lists the event types of events.
func eventTypes(events []game.Event) []game.EventType {
	out := make([]game.EventType, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

func findEvent(events []game.Event, typ game.EventType) (game.Event, bool) {
	for _, e := range events {
		if e.Type == typ {
			return e, true
		}
	}
	return game.Event{}, false
}

// TestFullGameThreePlayersTwoRounds plays a whole game over real sockets:
// lobby, settings, round 1 (chooser taps STOP, basta, grace, review with votes
// and a merge), round 2 (the chooser times out, 10 s auto-stop), final with
// winners and a rematch. Scores are hand computed (see r1Answers).
func TestFullGameThreePlayersTwoRounds(t *testing.T) {
	s := newStack(t, stackOpts{})
	tb := newTable(t, s, "Ana", "Beto", "Cami")
	ana, beto, cami := tb.seats[0].p, tb.seats[1].p, tb.seats[2].p
	ids := tb.ids()

	// ---- lobby ----
	info, ok := s.roomInfo(tb.code)
	if !ok || info.Phase != game.PhaseLobby || info.Players != 3 || info.MaxPlayers != 10 || info.Lang != game.LangES || !info.Joinable {
		t.Fatalf("lobby info = %+v (found %v)", info, ok)
	}
	v := tb.view(1)
	if v.HostID != ana.id || v.YouID != beto.id || len(v.Players) != 3 {
		t.Fatalf("lobby view = host %q you %q players %d", v.HostID, v.YouID, len(v.Players))
	}
	for i, p := range v.Players {
		if p.ID != ids[i] || !p.Connected || p.JoinedSeq != i+1 {
			t.Fatalf("lobby player %d = %+v", i, p)
		}
	}

	tb.wantCmdError(1, game.CodeNotHost, game.CmdUpdateSettings, game.UpdateSettings{Settings: roundSettings()})
	tb.wantCmdError(1, game.CodeNotHost, game.CmdStartGame, nil)
	tooFew := roundSettings()
	tooFew.Categories = tooFew.Categories[:2]
	tb.wantCmdError(0, game.CodeInvalidSettings, game.CmdUpdateSettings, game.UpdateSettings{Settings: tooFew})
	clash := roundSettings()
	clash.Categories[2] = game.CategoryRef{Custom: "país"} // the built-in "País" in another spelling
	tb.wantCmdError(0, game.CodeInvalidSettings, game.CmdUpdateSettings, game.UpdateSettings{Settings: clash})
	tb.wantCmdError(2, game.CodeWrongPhase, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "x"})

	tb.configure(roundSettings())
	v = tb.view(2)
	if v.Settings.Rounds != 2 || !v.Settings.ExcludeHardLetters || len(v.Settings.Categories) != 3 ||
		v.Settings.Categories[2].Custom != "Cosa de cocina" ||
		!slices.Equal(v.CategoryLabels, []string{"Nombre", "Animal", "Cosa de cocina"}) {
		t.Fatalf("settings not applied: %+v labels %v", v.Settings, v.CategoryLabels)
	}

	// ---- letter phase of round 1: the host is the chooser ----
	m := tb.marks()
	tb.start()
	for i, st := range tb.seats {
		evs := eventTypes(st.c.events(m[i]))
		if !slices.Equal(evs, []game.EventType{game.EventGameStarted, game.EventRoundStarted}) {
			t.Fatalf("%s saw events %v after start_game", st.p.name, evs)
		}
		if e, _ := findEvent(st.c.events(m[i]), game.EventRoundStarted); e.PlayerID != ana.id || e.Round != 1 {
			t.Fatalf("round_started = %+v, want chooser Ana in round 1", e)
		}
		v := st.c.view()
		if v.ChooserID != ana.id || v.RoundNum != 1 || v.Round == nil || v.Round.Letter != "" {
			t.Fatalf("%s letter-phase view: chooser %q round %d round %+v", st.p.name, v.ChooserID, v.RoundNum, v.Round)
		}
	}
	for _, st := range tb.seats {
		raw := st.c.waitFor("letter-phase state", func(f frame) bool {
			return f.State != nil && f.State.View.Phase == game.PhaseLetter
		}).Raw
		if bytes.Contains(raw, []byte("letterStartedAt")) || bytes.Contains(raw, []byte("startedAt")) {
			t.Fatalf("letter-phase state leaks the alphabet clock: %s", raw)
		}
	}
	tb.wantCmdError(1, game.CodeNotYourTurn, game.CmdStopLetter, nil)
	if got := tb.anyView().Phase; got != game.PhaseLetter {
		t.Fatalf("a rejected stop_letter changed the phase to %s", got)
	}

	m = tb.marks()
	tb.stopLetter("M")
	for i, st := range tb.seats {
		e, ok := findEvent(st.c.events(m[i]), game.EventLetterRevealed)
		if !ok || e.Letter != "M" || e.Round != 1 {
			t.Fatalf("%s: letter_revealed = %+v (found %v)", st.p.name, e, ok)
		}
		// The event precedes the state that moves the room to "playing".
		evFrame := st.c.waitSince(m[i], "letter_revealed", isEvent(game.EventLetterRevealed))
		stFrame := st.c.waitSince(m[i], "playing state", func(f frame) bool {
			return f.State != nil && f.State.View.Phase == game.PhasePlaying
		})
		if evFrame.Seq > stFrame.Seq {
			t.Fatalf("%s: letter_revealed (#%d) arrived after the playing state (#%d)", st.p.name, evFrame.Seq, stFrame.Seq)
		}
	}
	if v := tb.view(0); !slices.Equal(v.UsedLetters, []string{"M"}) {
		t.Fatalf("usedLetters = %v", v.UsedLetters)
	}

	// ---- playing: everybody types, the others' answers stay hidden ----
	tb.wantCmdError(0, game.CodeInvalidCategory, game.CmdSetAnswer, game.SetAnswer{Category: 3, Text: "Mono"})
	tb.wantCmdError(0, game.CodeAnswerTooLong, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "M" + strings.Repeat("a", 40)})
	tb.wantCmdError(0, protocol.ErrBadMessage, game.CmdSetAnswer, map[string]any{"category": "zero"})
	typed := r1Answers
	typed[2][2] = "Mandioca" // Cami corrects it during the grace allowance
	for i := range typed {
		tb.answers(i, typed[i][:]...)
	}
	tb.answer(2, 1, "Mapache") // typed and erased again: Cami ends with no animal
	tb.answer(2, 1, "")
	for i, st := range tb.seats {
		v := st.c.view()
		wantFilled := map[string]int{ana.id: 3, beto.id: 3, cami.id: 2}
		for id, n := range wantFilled {
			if v.Round.Filled[id] != n {
				t.Fatalf("%s: filled[%s] = %d, want %d (progress must be visible)", st.p.name, id, v.Round.Filled[id], n)
			}
		}
		if len(v.Round.Answers) != 1 || !slices.Equal(v.Round.Answers[ids[i]], typed[i][:]) {
			t.Fatalf("%s sees answers %v, want only its own %v", st.p.name, v.Round.Answers, typed[i])
		}
	}
	// Beto's Perro does not start with M: he cannot call basta.
	tb.wantCmdError(1, game.CodeBastaNotAllowed, game.CmdBasta, nil)
	tb.wantCmdError(2, game.CodeBastaNotAllowed, game.CmdBasta, nil)

	// ---- basta -> grace -> review ----
	callTime := s.clock.Now()
	m = tb.marks()
	tb.mustCmd(0, game.CmdBasta, nil)
	tb.waitPhase(game.PhaseGrace)
	for i, st := range tb.seats {
		e, ok := findEvent(st.c.events(m[i]), game.EventBasta)
		if !ok || e.PlayerID != ana.id || e.Name != "Ana" {
			t.Fatalf("%s: basta event = %+v (found %v)", st.p.name, e, ok)
		}
		r := st.c.view().Round
		if r.CallerID != ana.id || !r.GraceEndsAt.Equal(callTime.Add(graceFor)) {
			t.Fatalf("%s: grace view caller %q ends %v, want Ana at %v", st.p.name, r.CallerID, r.GraceEndsAt, callTime.Add(graceFor))
		}
	}
	tb.wantCmdError(1, game.CodeWrongPhase, game.CmdBasta, nil)

	// The 3 s grace passes; Cami's update still lands in the 500 ms allowance.
	s.advance(graceFor)
	tb.answer(2, 2, "Manteca")
	if got := tb.view(2).Phase; got != game.PhaseGrace {
		t.Fatalf("phase right at the lock = %s, want grace (late updates are accepted)", got)
	}
	m = tb.marks()
	s.advance(lateFor)
	tb.waitPhase(game.PhaseReview)
	for i, st := range tb.seats {
		if _, ok := findEvent(st.c.events(m[i]), game.EventRoundLocked); !ok {
			t.Fatalf("%s: no round_locked event: %v", st.p.name, eventTypes(st.c.events(m[i])))
		}
	}
	tb.wantCmdError(2, game.CodeWrongPhase, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Mauro"})

	// The answers are no longer hidden, with the live points of nobody's votes yet.
	for _, st := range tb.seats {
		v := st.c.view()
		for j := range r1Answers {
			if !slices.Equal(v.Round.Answers[ids[j]], r1Answers[j][:]) {
				t.Fatalf("%s: review answers of %s = %v, want %v", st.p.name, tb.seats[j].p.name, v.Round.Answers[ids[j]], r1Answers[j])
			}
		}
		wantInts(t, st.p.name+" live totals", reviewTotals(v, ids...), r1Unvoted)
	}
	res := tb.view(0).Round.Result
	if res.Letter != "M" || res.Players[beto.id].Cells[1].Status != game.StatusWrongLetter ||
		res.Players[cami.id].Cells[1].Status != game.StatusEmpty || res.Players[ana.id].Cells[2].Points != game.PointsShared {
		t.Fatalf("live result = %+v", res)
	}

	// ---- review: votes, merge, navigation ----
	tb.wantCmdError(0, game.CodeSelfVote, game.CmdVoteReject, game.VoteReject{Category: 2, Target: ana.id})
	tb.wantCmdError(0, game.CodeEmptyAnswer, game.CmdVoteReject, game.VoteReject{Category: 1, Target: cami.id})
	tb.wantCmdError(0, game.CodeInvalidCategory, game.CmdVoteReject, game.VoteReject{Category: 7, Target: beto.id})
	tb.wantCmdError(0, game.CodeUnknownPlayer, game.CmdVoteReject, game.VoteReject{Category: 2, Target: "nobody"})

	tb.mustCmd(0, game.CmdVoteReject, game.VoteReject{Category: 2, Target: beto.id})
	if c := tb.view(1).Round.Result.Players[beto.id].Cells[2]; c.Status != game.StatusValid {
		t.Fatalf("one reject vote of two eligible must not reject: %+v", c)
	}
	tb.mustCmd(2, game.CmdVoteReject, game.VoteReject{Category: 2, Target: beto.id})
	for _, st := range tb.seats {
		st.c.waitState("Beto's Mate rejected", func(s *protocol.State) bool {
			c := s.View.Round.Result.Players[beto.id].Cells[2]
			return c.Status == game.StatusRejected && c.Points == 0
		})
	}
	v = tb.view(0)
	if c := v.Round.Result.Players[ana.id].Cells[2]; c.Points != game.PointsUnique || c.Shared {
		t.Fatalf("Ana's Mate must be unique once Beto's is rejected: %+v", c)
	}
	if len(v.Round.RejectVotes) != 2 {
		t.Fatalf("rejectVotes = %+v", v.Round.RejectVotes)
	}
	// A vote can be withdrawn.
	tb.mustCmd(2, game.CmdVoteReject, game.VoteReject{Category: 2, Target: beto.id, Withdraw: true})
	if c := tb.view(2).Round.Result.Players[beto.id].Cells[2]; c.Status != game.StatusValid {
		t.Fatalf("withdrawing a vote must bring the answer back: %+v", c)
	}
	tb.mustCmd(2, game.CmdVoteReject, game.VoteReject{Category: 2, Target: beto.id})

	// Merge Marta and Martita: invalid for a wrong-letter answer, then proposal + votes.
	tb.wantCmdError(1, game.CodeInvalidMerge, game.CmdProposeMerge, game.ProposeMerge{Category: 1, PlayerA: ana.id, PlayerB: beto.id})
	tb.wantCmdError(1, game.CodeInvalidMerge, game.CmdProposeMerge, game.ProposeMerge{Category: 0, PlayerA: beto.id, PlayerB: beto.id})
	tb.mustCmd(0, game.CmdProposeMerge, game.ProposeMerge{Category: 0, PlayerA: ana.id, PlayerB: beto.id})
	pr := tb.view(1).Round.Proposals
	if len(pr) != 1 || pr[0].ID != 1 || pr[0].Yes != 1 || pr[0].Passed || pr[0].ProposedBy != ana.id {
		t.Fatalf("proposals after the proposal = %+v", pr)
	}
	tb.wantCmdError(1, game.CodeProposalExists, game.CmdProposeMerge, game.ProposeMerge{Category: 0, PlayerA: beto.id, PlayerB: ana.id})
	tb.wantCmdError(1, game.CodeUnknownProposal, game.CmdVoteMerge, game.VoteMerge{ProposalID: 99, Yes: true})
	tb.mustCmd(2, game.CmdVoteMerge, game.VoteMerge{ProposalID: 1, Yes: false})
	if pr := tb.view(0).Round.Proposals; pr[0].Passed || pr[0].Yes != 1 {
		t.Fatalf("a no vote must not pass the proposal: %+v", pr)
	}
	tb.mustCmd(1, game.CmdVoteMerge, game.VoteMerge{ProposalID: 1, Yes: true})
	for _, st := range tb.seats {
		wantInts(t, st.p.name+" voted totals", reviewTotals(st.c.waitState("merge passed", func(s *protocol.State) bool {
			return len(s.View.Round.Proposals) == 1 && s.View.Round.Proposals[0].Passed
		}).View, ids...), r1Voted)
	}
	tb.wantCmdError(0, game.CodeAlreadySame, game.CmdProposeMerge, game.ProposeMerge{Category: 0, PlayerA: ana.id, PlayerB: beto.id})
	if c := tb.view(2).Round.Result.Players[cami.id].Cells[0]; c.Points != game.PointsUnique {
		t.Fatalf("Cami's Mauro stays unique: %+v", c)
	}

	// The host moves the shared card; everybody follows.
	tb.wantCmdError(1, game.CodeNotHost, game.CmdReviewNav, game.ReviewNav{Category: 1})
	tb.wantCmdError(0, game.CodeInvalidCategory, game.CmdReviewNav, game.ReviewNav{Category: 9})
	for _, cat := range []int{1, 2, 0, 2} {
		tb.mustCmd(0, game.CmdReviewNav, game.ReviewNav{Category: cat})
		tb.waitAll("review card", func(v game.View) bool { return v.Round.ReviewCategory == cat })
	}

	// ---- finish the review: round summary ----
	tb.wantCmdError(1, game.CodeNotHost, game.CmdFinishReview, nil)
	m = tb.marks()
	tb.mustCmd(0, game.CmdFinishReview, nil)
	tb.waitPhase(game.PhaseRoundSummary)
	for i, st := range tb.seats {
		if _, ok := findEvent(st.c.events(m[i]), game.EventRoundScored); !ok {
			t.Fatalf("%s: no round_scored event", st.p.name)
		}
		v := st.c.view()
		wantInts(t, st.p.name+" running totals after round 1", totalScores(v, ids...), r1Voted)
		if len(v.History) != 1 || v.History[0].Round != 1 || v.History[0].Letter != "M" {
			t.Fatalf("%s: history = %+v", st.p.name, v.History)
		}
		got := []int{v.History[0].Players[ana.id].Total, v.History[0].Players[beto.id].Total, v.History[0].Players[cami.id].Total}
		wantInts(t, st.p.name+" history totals", got, r1Voted)
		wantInts(t, st.p.name+" summary result", reviewTotals(v, ids...), r1Voted)
	}
	tb.wantCmdError(1, game.CodeNotHost, game.CmdNextRound, nil)
	tb.wantCmdError(1, game.CodeNotHost, game.CmdEndGame, nil)

	// ---- round 2: the chooser is Beto (next in join order) and times out ----
	m = tb.marks()
	tb.mustCmd(0, game.CmdNextRound, nil)
	tb.waitPhase(game.PhaseLetter)
	for i, st := range tb.seats {
		e, ok := findEvent(st.c.events(m[i]), game.EventRoundStarted)
		if !ok || e.PlayerID != beto.id || e.Round != 2 {
			t.Fatalf("%s: round_started = %+v (found %v), want chooser Beto in round 2", st.p.name, e, ok)
		}
	}
	tb.wantCmdError(0, game.CodeNotYourTurn, game.CmdStopLetter, nil) // the host is not the chooser now
	v = tb.view(0)
	avail := game.AvailableLetters(v.Settings.Lang, v.Settings.ExcludeHardLetters, v.UsedLetters)
	if slices.Contains(avail, "M") {
		t.Fatalf("M is not skipped in round 2: %v", avail)
	}
	letter := game.LetterAt(avail, chooserFor)
	m = tb.marks()
	s.advance(chooserFor)
	tb.waitAll("auto-stopped letter", func(v game.View) bool {
		return v.Phase == game.PhasePlaying && v.Round.Letter == letter
	})
	for i, st := range tb.seats {
		if e, ok := findEvent(st.c.events(m[i]), game.EventLetterRevealed); !ok || e.Letter != letter || e.Round != 2 {
			t.Fatalf("%s: auto-stop event = %+v (found %v), want letter %s", st.p.name, e, ok, letter)
		}
	}
	if got := tb.view(1).UsedLetters; !slices.Equal(got, []string{"M", letter}) {
		t.Fatalf("usedLetters = %v", got)
	}

	// Round 2: Ana and Beto share a name (case does not matter), Cami skips the animal.
	r2 := [3][3]string{
		{letter + "aa", letter + "cc", letter + "ee"},
		{letter + "AA", letter + "dd", letter + "ff"},
		{letter + "bb", "", letter + "gg"},
	}
	for i := range r2 {
		tb.answers(i, r2[i][:]...)
	}
	for i, st := range tb.seats {
		if got := st.c.view().Round.Answers[ids[i]]; !slices.Equal(got, r2[i][:]) {
			t.Fatalf("%s: round 2 own answers = %v, want %v", st.p.name, got, r2[i])
		}
	}
	tb.mustCmd(1, game.CmdBasta, nil) // Beto, the chooser, calls it
	tb.waitPhase(game.PhaseGrace)
	if got := tb.view(0).Round.CallerID; got != beto.id {
		t.Fatalf("callerId = %q, want Beto", got)
	}
	s.advance(graceFor + lateFor)
	tb.waitPhase(game.PhaseReview)
	wantInts(t, "round 2 live totals", reviewTotals(tb.view(2), ids...), []int{25, 25, 20})
	tb.mustCmd(0, game.CmdFinishReview, nil)
	tb.waitPhase(game.PhaseRoundSummary)
	for _, st := range tb.seats {
		wantInts(t, st.p.name+" running totals after round 2", totalScores(st.c.view(), ids...), []int{50, 30, 40})
	}

	// ---- final ----
	m = tb.marks()
	tb.mustCmd(0, game.CmdNextRound, nil) // 2 rounds were configured: the game ends
	tb.waitPhase(game.PhaseFinal)
	for i, st := range tb.seats {
		e, ok := findEvent(st.c.events(m[i]), game.EventGameOver)
		if !ok || !slices.Equal(e.Winners, []string{ana.id}) {
			t.Fatalf("%s: game_over = %+v (found %v)", st.p.name, e, ok)
		}
		if v := st.c.view(); !slices.Equal(v.Winners, []string{ana.id}) || len(v.History) != 2 {
			t.Fatalf("%s: final view winners %v history %d", st.p.name, v.Winners, len(v.History))
		}
	}
	if info, _ := s.roomInfo(tb.code); info.Phase != game.PhaseFinal || info.Joinable {
		t.Fatalf("final info = %+v, want phase final and not joinable", info)
	}
	// A brand-new player cannot join a finished game...
	dani := s.newPlayer("Dani")
	dc := s.connect(dani)
	dc.send(game.CmdJoin, dc.joinRequest(tb.code))
	dc.waitFor("wrong_phase for a newcomer in the final", isError(game.CodeWrongPhase))

	// ---- rematch: scores reset, custom category kept ----
	tb.wantCmdError(1, game.CodeNotHost, game.CmdRematch, nil)
	m = tb.marks()
	tb.mustCmd(0, game.CmdRematch, nil)
	tb.waitPhase(game.PhaseLobby)
	for i, st := range tb.seats {
		if _, ok := findEvent(st.c.events(m[i]), game.EventRematch); !ok {
			t.Fatalf("%s: no rematch event", st.p.name)
		}
		v := st.c.view()
		wantInts(t, st.p.name+" totals after rematch", totalScores(v, ids...), []int{0, 0, 0})
		if len(v.UsedLetters) != 0 || len(v.History) != 0 || v.Round != nil || v.RoundNum != 0 || len(v.Winners) != 0 {
			t.Fatalf("%s: rematch left state behind: used %v history %d round %v roundNum %d winners %v",
				st.p.name, v.UsedLetters, len(v.History), v.Round, v.RoundNum, v.Winners)
		}
		if v.Settings.Rounds != 2 || len(v.Settings.Categories) != 3 || v.Settings.Categories[2].Custom != "Cosa de cocina" {
			t.Fatalf("%s: rematch lost the settings: %+v", st.p.name, v.Settings)
		}
	}
	// ...but can once the lobby is back.
	dc.send(game.CmdJoin, dc.joinRequest(tb.code))
	dc.waitState("Dani in the rematch lobby", func(s *protocol.State) bool { return len(s.View.Players) == 4 })
	if info, _ := s.roomInfo(tb.code); info.Phase != game.PhaseLobby || info.Players != 4 || !info.Joinable {
		t.Fatalf("rematch info = %+v", info)
	}

	// ---- privacy over the whole game ----
	if n := tb.assertNoAnswerLeaks(); n < 12 {
		t.Fatalf("only %d playing/grace state frames were checked", n)
	}
	// Positive control: the same strings do show up in the review frames.
	raw := tb.seats[0].c.waitFor("review state", func(f frame) bool {
		return f.State != nil && f.State.View.Phase == game.PhaseReview
	}).Raw
	if !bytes.Contains(raw, []byte(`"Martita"`)) {
		t.Fatalf("review frame does not carry the others' answers: %s", raw)
	}
	for _, c := range tb.clients {
		var last int64
		for _, f := range c.snapshotFrames() {
			if f.State != nil {
				if cur := f.State.ServerTime; cur < last {
					t.Fatalf("%s: serverTime went backwards: %v after %v", c.p.name, cur, last)
				} else {
					last = cur
				}
				if f.State.Code != tb.code {
					t.Fatalf("%s: state for room %q, want %q", c.p.name, f.State.Code, tb.code)
				}
			}
		}
	}
}

// TestLetterPhase covers the chooser's STOP, the 10 s auto-stop and what
// happens around them.
func TestLetterPhase(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, tb *table)
	}{
		{"chooser taps stop", func(t *testing.T, tb *table) {
			tb.stopLetter("C")
			if got := tb.view(1).Round.Letter; got != "C" {
				t.Fatalf("letter = %q", got)
			}
		}},
		{"letter depends on when the chooser taps", func(t *testing.T, tb *table) {
			// 1 s = 8 slots of 120 ms -> the 9th available letter (I).
			tb.s.advance(time.Second)
			tb.mustCmd(0, game.CmdStopLetter, nil)
			want := game.LetterAt(game.AvailableLetters(game.LangES, true, nil), time.Second)
			if got := tb.view(1).Round.Letter; got != want || want != "I" {
				t.Fatalf("letter = %q, want %q (I)", got, want)
			}
		}},
		{"auto-stop after 10 s", func(t *testing.T, tb *table) {
			tb.s.advance(chooserFor - time.Millisecond)
			tb.wantCmdError(1, game.CodeNotYourTurn, game.CmdStopLetter, nil) // still the letter phase
			m := tb.marks()
			tb.s.advance(time.Millisecond)
			tb.waitPhase(game.PhasePlaying)
			want := game.LetterAt(game.AvailableLetters(game.LangES, true, nil), chooserFor)
			if got := tb.view(1).Round.Letter; got != want {
				t.Fatalf("auto letter = %q, want %q", got, want)
			}
			if e, ok := findEvent(tb.seats[1].c.events(m[1]), game.EventLetterRevealed); !ok || e.Letter != want {
				t.Fatalf("letter_revealed = %+v (found %v)", e, ok)
			}
		}},
		{"a tap after the deadline is too late", func(t *testing.T, tb *table) {
			tb.s.advance(chooserFor)
			tb.waitPhase(game.PhasePlaying)
			tb.wantCmdError(0, game.CodeWrongPhase, game.CmdStopLetter, nil)
		}},
		{"only the chooser may stop", func(t *testing.T, tb *table) {
			tb.wantCmdError(1, game.CodeNotYourTurn, game.CmdStopLetter, nil)
			if got := tb.view(0).Phase; got != game.PhaseLetter {
				t.Fatalf("phase = %s", got)
			}
		}},
		{"an offline chooser is stopped by the timer", func(t *testing.T, tb *table) {
			tb.seats[0].c.drop()
			tb.seats[1].c.waitState("Ana offline", func(s *protocol.State) bool {
				p, _ := seatOf(s.View, tb.seats[0].p.id)
				return !p.Connected
			})
			tb.s.advance(chooserFor)
			st := tb.seats[1].c.waitState("playing without the chooser", func(s *protocol.State) bool {
				return s.View.Phase == game.PhasePlaying
			})
			if st.View.Round.Letter == "" {
				t.Fatal("no letter after the auto-stop")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStack(t, stackOpts{})
			tb := newTable(t, s, "Ana", "Beto")
			tb.configure(roundSettings())
			tb.start()
			tc.run(t, tb)
		})
	}
}

// TestTimeLimitEndsTheRound: with a time limit and nobody calling basta the
// round locks by itself (time_up, then round_locked after the 500 ms allowance).
func TestTimeLimitEndsTheRound(t *testing.T) {
	for _, limit := range []int{60, 90} {
		t.Run(time.Duration(limit*int(time.Second)).String(), func(t *testing.T) {
			s := newStack(t, stackOpts{})
			tb := newTable(t, s, "Ana", "Beto", "Cami")
			settings := roundSettings()
			settings.TimeLimitSeconds = limit
			tb.configure(settings)
			tb.start()

			tb.stopLetter("M")
			revealedAt := s.clock.Now()
			deadline := revealedAt.Add(time.Duration(limit) * time.Second)
			for _, st := range tb.seats {
				if r := st.c.view().Round; !r.TimeLimitEndsAt.Equal(deadline) {
					t.Fatalf("%s: timeLimitEndsAt = %v, want %v", st.p.name, r.TimeLimitEndsAt, deadline)
				}
			}
			// Partial answers: nobody can call basta.
			tb.answer(0, 0, "Marta")
			tb.answer(1, 1, "Mono")
			tb.wantCmdError(0, game.CodeBastaNotAllowed, game.CmdBasta, nil)

			// One second before the limit nothing has happened.
			s.advance(time.Duration(limit)*time.Second - time.Second)
			tb.answer(2, 0, "Mauro")
			if got := tb.view(2).Phase; got != game.PhasePlaying {
				t.Fatalf("phase = %s one second before the limit", got)
			}

			m := tb.marks()
			s.advance(time.Second)
			tb.waitPhase(game.PhaseGrace)
			for i, st := range tb.seats {
				evs := eventTypes(st.c.events(m[i]))
				if !slices.Equal(evs, []game.EventType{game.EventTimeUp}) {
					t.Fatalf("%s: events at the limit = %v, want [time_up] (and no basta)", st.p.name, evs)
				}
				r := st.c.view().Round
				if r.CallerID != "" || !r.GraceEndsAt.Equal(deadline) {
					t.Fatalf("%s: grace after time-up: caller %q graceEndsAt %v, want no caller and %v", st.p.name, r.CallerID, r.GraceEndsAt, deadline)
				}
			}
			// The 500 ms allowance still takes a late update.
			tb.answer(1, 2, "Mate")
			if got := tb.view(1).Phase; got != game.PhaseGrace {
				t.Fatalf("phase = %s", got)
			}
			tb.wantCmdError(0, game.CodeWrongPhase, game.CmdBasta, nil)

			m = tb.marks()
			s.advance(lateFor)
			tb.waitPhase(game.PhaseReview)
			for i, st := range tb.seats {
				evs := eventTypes(st.c.events(m[i]))
				if !slices.Equal(evs, []game.EventType{game.EventRoundLocked}) {
					t.Fatalf("%s: events after the allowance = %v, want [round_locked]", st.p.name, evs)
				}
			}
			v := tb.view(0)
			if v.Round.CallerID != "" {
				t.Fatalf("callerId = %q", v.Round.CallerID)
			}
			if got := v.Round.Answers[tb.seats[1].p.id]; !slices.Equal(got, []string{"", "Mono", "Mate"}) {
				t.Fatalf("Beto's answers = %v", got)
			}
			if got := s.metricValue("tuttifrutti_basta_total", nil); got != 0 {
				t.Fatalf("basta_total = %v after a round nobody called", got)
			}
			if got := s.metricValue("tuttifrutti_rounds_total", nil); got != 1 {
				t.Fatalf("rounds_total = %v", got)
			}
			tb.assertNoAnswerLeaks()
		})
	}
}

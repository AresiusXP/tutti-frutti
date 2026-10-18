package game

import (
	"fmt"
	"testing"
	"time"
)

// threeCats answers: p1 is always valid so it can call basta.
var (
	ansP1 = []string{"Ana", "Argentina", "Alce"}
)

func TestReviewNav(t *testing.T) {
	g, now := newReview(t, ansP1, ansP1, ansP1)
	if g.Round.ReviewCategory != 0 {
		t.Fatalf("review starts at category %d", g.Round.ReviewCategory)
	}
	apply(t, g, now, "p1", ReviewNav{Category: 2})
	if g.Round.ReviewCategory != 2 {
		t.Errorf("ReviewCategory = %d", g.Round.ReviewCategory)
	}
	apply(t, g, now, "p1", ReviewNav{Category: 0})
	expectErr(t, g, now, "p1", ReviewNav{Category: 3}, ErrInvalidCategory)
	expectErr(t, g, now, "p1", ReviewNav{Category: -1}, ErrInvalidCategory)
	expectErr(t, g, now, "p2", ReviewNav{Category: 1}, ErrNotHost)

	if v := g.View("p3"); v.Round.ReviewCategory != 0 {
		t.Errorf("everyone sees the host's card, got %d", v.Round.ReviewCategory)
	}

	h := newPlaying(t, 2, nil)
	expectErr(t, h, tStart.Add(time.Second), "p1", ReviewNav{Category: 1}, ErrWrongPhase)
}

func TestVoteRejectGuards(t *testing.T) {
	g, now := newReview(t, ansP1, []string{"Ana", "", "Brisa"}, ansP1)
	apply(t, g, now, "p4", Join{Name: "Spectator"})

	expectErr(t, g, now, "p1", VoteReject{Category: 0, Target: "p1"}, ErrSelfVote)
	expectErr(t, g, now, "p1", VoteReject{Category: 1, Target: "p2"}, ErrEmptyAnswer)
	expectErr(t, g, now, "p1", VoteReject{Category: 0, Target: "ghost"}, ErrUnknownPlayer)
	expectErr(t, g, now, "p1", VoteReject{Category: 0, Target: "p4"}, ErrUnknownPlayer) // spectators have no answers
	expectErr(t, g, now, "p1", VoteReject{Category: 3, Target: "p2"}, ErrInvalidCategory)
	expectErr(t, g, now, "p1", VoteReject{Category: -1, Target: "p2"}, ErrInvalidCategory)
	expectErr(t, g, now, "p4", VoteReject{Category: 0, Target: "p1"}, ErrNotParticipant)

	// A wrong-letter answer may be voted on (it is already worth 0).
	apply(t, g, now, "p1", VoteReject{Category: 2, Target: "p2"})

	h := newPlaying(t, 2, nil)
	expectErr(t, h, tStart.Add(time.Second), "p1", VoteReject{Category: 0, Target: "p2"}, ErrWrongPhase)
}

func TestVoteRejectIsIdempotentAndWithdrawable(t *testing.T) {
	g, now := newReview(t, ansP1, ansP1, ansP1)
	vote := VoteReject{Category: 0, Target: "p1"}

	apply(t, g, now, "p2", vote)
	apply(t, g, now, "p2", vote)
	if n := len(g.Round.RejectVotes); n != 1 {
		t.Fatalf("voting twice stored %d votes, want 1", n)
	}
	if got := cell(t, liveResult(t, g), "p1", 0); got.Status != StatusValid {
		t.Errorf("1 of 2 eligible is not a majority, got %+v", got)
	}

	apply(t, g, now, "p3", vote)
	if got := cell(t, liveResult(t, g), "p1", 0); got.Status != StatusRejected || got.Points != 0 {
		t.Errorf("2 of 2 eligible rejects, got %+v", got)
	}

	// Withdrawing brings the answer back; withdrawing a missing vote is harmless.
	apply(t, g, now, "p3", VoteReject{Category: 0, Target: "p1", Withdraw: true})
	apply(t, g, now, "p3", VoteReject{Category: 0, Target: "p1", Withdraw: true})
	if got := cell(t, liveResult(t, g), "p1", 0); got.Status != StatusValid {
		t.Errorf("after withdrawing, got %+v", got)
	}
	if n := len(g.Round.RejectVotes); n != 1 {
		t.Errorf("votes = %d, want 1", n)
	}
}

func TestRejectThresholds(t *testing.T) {
	// Every player answers "Ana" in category 0; p1 is the author being voted
	// on. voters cast a reject vote first, then the listed players disconnect.
	// Rejected iff yesVotes*2 > connected participants other than the author.
	tests := []struct {
		name         string
		players      int
		voters       []string
		disconnected []string
		want         bool
	}{
		{"2 players: 0 votes", 2, nil, nil, false},
		{"2 players: the only other player votes", 2, []string{"p2"}, nil, true},
		{"3 players: 1 of 2 is not more than half", 3, []string{"p2"}, nil, false},
		{"3 players: 2 of 2", 3, []string{"p2", "p3"}, nil, true},
		{"4 players: 1 of 3", 4, []string{"p2"}, nil, false},
		{"4 players: 2 of 3", 4, []string{"p2", "p3"}, nil, true},
		{"4 players: 3 of 3", 4, []string{"p2", "p3", "p4"}, nil, true},
		{"5 players: 2 of 4 is exactly half", 5, []string{"p2", "p3"}, nil, false},
		{"5 players: 3 of 4", 5, []string{"p2", "p3", "p4"}, nil, true},
		{"3 players, 1 disconnected: 1 of 1", 3, []string{"p2"}, []string{"p3"}, true},
		{"3 players, 1 disconnected: 0 of 1", 3, nil, []string{"p3"}, false},
		{"4 players, 1 disconnected: 1 of 2 is exactly half", 4, []string{"p2"}, []string{"p4"}, false},
		{"4 players, 1 disconnected: 2 of 2", 4, []string{"p2", "p3"}, []string{"p4"}, true},
		{"5 players, 2 disconnected: 1 of 2", 5, []string{"p2"}, []string{"p4", "p5"}, false},
		{"5 players, 2 disconnected: 2 of 2", 5, []string{"p2", "p3"}, []string{"p4", "p5"}, true},
		{"votes from players who disconnected are ignored", 3, []string{"p3"}, []string{"p3"}, false},
		{"a disconnected voter does not stop the others", 3, []string{"p2", "p3"}, []string{"p3"}, true},
		{"the author being disconnected does not shrink the electorate", 3, []string{"p2"}, []string{"p1"}, false},
		{"the author being disconnected, everyone else votes", 3, []string{"p2", "p3"}, []string{"p1"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			answers := make([][]string, tt.players)
			for i := range answers {
				answers[i] = ansP1
			}
			g, now := newReview(t, answers...)
			for _, v := range tt.voters {
				apply(t, g, now, v, VoteReject{Category: 0, Target: "p1"})
			}
			for _, d := range tt.disconnected {
				setConnected(t, g, now, d, false)
			}

			got := cell(t, liveResult(t, g), "p1", 0)
			if rejected := got.Status == StatusRejected; rejected != tt.want {
				t.Errorf("rejected = %v, want %v (cell %+v)", rejected, tt.want, got)
			}
			// Only the targeted category is affected.
			if other := cell(t, liveResult(t, g), "p1", 1); other.Status != StatusValid {
				t.Errorf("category 1 = %+v", other)
			}
		})
	}
}

func TestRejectedAnswerStopsCountingAsDuplicate(t *testing.T) {
	g, now := newReview(t, ansP1, []string{"Ana", "Alemania", "Alce"}, []string{"Alba", "Angola", "Alce"})
	res := liveResult(t, g)
	if c := cell(t, res, "p1", 0); c.Points != 5 || !c.Shared {
		t.Fatalf("before voting p1 = %+v, want shared", c)
	}

	apply(t, g, now, "p1", VoteReject{Category: 0, Target: "p2"})
	apply(t, g, now, "p3", VoteReject{Category: 0, Target: "p2"})
	res = liveResult(t, g)
	if c := cell(t, res, "p2", 0); c.Status != StatusRejected || c.Points != 0 {
		t.Errorf("p2 = %+v, want rejected", c)
	}
	if c := cell(t, res, "p1", 0); c.Status != StatusValid || c.Points != 10 || c.Shared {
		t.Errorf("p1 = %+v, want unique now that the twin is rejected", c)
	}
	if res.Players["p2"].Total != 0+10+5 { // rejected, unique país, "Alce" shared by three

		t.Errorf("p2 total = %d", res.Players["p2"].Total)
	}
}

func TestProposeMergeGuards(t *testing.T) {
	g, now := newReview(t,
		[]string{"Ana", "Argentina", "Alce"},
		[]string{"Anna", "", "Brisa"},
		[]string{"Ana", "Alemania", "Alce"},
	)
	apply(t, g, now, "p4", Join{Name: "Spectator"})
	res := liveResult(t, g)
	if c := cell(t, res, "p1", 0); !c.Shared {
		t.Fatalf("setup: %+v", c)
	}

	tests := []struct {
		name string
		by   string
		cmd  ProposeMerge
		want error
	}{
		{"unknown category", "p1", ProposeMerge{Category: 3, PlayerA: "p1", PlayerB: "p2"}, ErrInvalidCategory},
		{"negative category", "p1", ProposeMerge{Category: -1, PlayerA: "p1", PlayerB: "p2"}, ErrInvalidCategory},
		{"unknown player", "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "ghost"}, ErrUnknownPlayer},
		{"spectator target", "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p4"}, ErrUnknownPlayer},
		{"same player twice", "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p1"}, ErrInvalidMerge},
		{"empty answer", "p1", ProposeMerge{Category: 1, PlayerA: "p1", PlayerB: "p2"}, ErrInvalidMerge},
		{"wrong letter answer", "p1", ProposeMerge{Category: 2, PlayerA: "p1", PlayerB: "p2"}, ErrInvalidMerge},
		{"already the same text", "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p3"}, ErrAlreadySame},
		{"spectator proposer", "p4", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p2"}, ErrNotParticipant},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectErr(t, g, now, tt.by, tt.cmd, tt.want)
		})
	}

	h := newPlaying(t, 2, nil)
	expectErr(t, h, tStart.Add(time.Second), "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p2"}, ErrWrongPhase)
}

func TestRejectedAnswersCannotBeMerged(t *testing.T) {
	g, now := newReview(t, ansP1, []string{"Anna", "Alemania", "Alce"}, ansP1)
	apply(t, g, now, "p1", VoteReject{Category: 0, Target: "p2"})
	apply(t, g, now, "p3", VoteReject{Category: 0, Target: "p2"})
	expectErr(t, g, now, "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p2"}, ErrInvalidMerge)
}

func TestMergeProposalLifecycle(t *testing.T) {
	g, now := newReview(t,
		[]string{"Ana", "Argentina", "Alce"},
		[]string{"Anna", "Alemania", "Alce"},
		[]string{"Alba", "Angola", "Ardilla"},
	)
	if c := cell(t, liveResult(t, g), "p1", 0); c.Points != 10 {
		t.Fatalf("setup: %+v", c)
	}

	// p3 proposes that Ana and Anna are the same: one yes of three is not a majority.
	apply(t, g, now, "p3", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p2"})
	if len(g.Round.Proposals) != 1 {
		t.Fatalf("proposals = %+v", g.Round.Proposals)
	}
	pr := g.Round.Proposals[0]
	if pr.ID != 1 || pr.ProposedBy != "p3" || len(pr.Votes) != 1 || pr.Votes[0] != (MergeVote{Voter: "p3", Yes: true}) {
		t.Errorf("proposal = %+v", pr)
	}
	if c := cell(t, liveResult(t, g), "p1", 0); c.Points != 10 {
		t.Errorf("1 of 3 must not merge, got %+v", c)
	}
	pv := g.View("p1").Round.Proposals
	if len(pv) != 1 || pv[0].Yes != 1 || pv[0].Passed {
		t.Errorf("view proposals = %+v", pv)
	}

	// The same pair cannot be proposed again while it is pending.
	expectErr(t, g, now, "p1", ProposeMerge{Category: 0, PlayerA: "p2", PlayerB: "p1"}, ErrProposalExists)

	// A second yes makes 2 of 3: passed, both answers become shared.
	apply(t, g, now, "p1", VoteMerge{ProposalID: 1, Yes: true})
	res := liveResult(t, g)
	for _, p := range []string{"p1", "p2"} {
		if c := cell(t, res, p, 0); c.Points != 5 || !c.Shared {
			t.Errorf("%s = %+v, want shared 5", p, c)
		}
	}
	if c := cell(t, res, "p3", 0); c.Points != 10 {
		t.Errorf("p3 = %+v, want unchanged", c)
	}
	if pv := g.View("p2").Round.Proposals; !pv[0].Passed || pv[0].Yes != 2 {
		t.Errorf("view proposals = %+v", pv)
	}

	// Once the answers are the same, proposing again is refused.
	expectErr(t, g, now, "p3", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p2"}, ErrAlreadySame)

	// Votes can be changed: a yes turning into a no un-merges.
	apply(t, g, now, "p1", VoteMerge{ProposalID: 1, Yes: false})
	if c := cell(t, liveResult(t, g), "p1", 0); c.Points != 10 {
		t.Errorf("after the yes became a no, p1 = %+v", c)
	}
	apply(t, g, now, "p2", VoteMerge{ProposalID: 1, Yes: true})
	if c := cell(t, liveResult(t, g), "p1", 0); c.Points != 5 {
		t.Errorf("after p2 voted yes, p1 = %+v", c)
	}
	if n := len(g.Round.Proposals[0].Votes); n != 3 {
		t.Errorf("votes = %d, want one per voter (3)", n)
	}

	// The merge only counts in its own category.
	if c := cell(t, liveResult(t, g), "p1", 1); c.Points != 10 {
		t.Errorf("category 1 = %+v", c)
	}
}

func TestMergeIsTransitive(t *testing.T) {
	g, now := newReview(t,
		[]string{"Ana", "Argentina", "Alce"},
		[]string{"Anna", "Alemania", "Alce"},
		[]string{"Anita", "Angola", "Ardilla"},
	)
	// A~B and B~C pass, so A~C follows.
	apply(t, g, now, "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p2"})
	apply(t, g, now, "p2", VoteMerge{ProposalID: 1, Yes: true})
	apply(t, g, now, "p2", ProposeMerge{Category: 0, PlayerA: "p2", PlayerB: "p3"})
	apply(t, g, now, "p3", VoteMerge{ProposalID: 2, Yes: true})

	res := liveResult(t, g)
	for _, p := range []string{"p1", "p2", "p3"} {
		if c := cell(t, res, p, 0); c.Points != 5 || !c.Shared {
			t.Errorf("%s = %+v, want shared 5", p, c)
		}
	}
	// p1 and p3 are now the same through p2: no need for a third proposal.
	expectErr(t, g, now, "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p3"}, ErrAlreadySame)
}

func TestMergeThresholdsWithDisconnectedPlayers(t *testing.T) {
	g, now := newReview(t,
		[]string{"Ana", "Argentina", "Alce"},
		[]string{"Anna", "Alemania", "Alce"},
		[]string{"Alba", "Angola", "Ardilla"},
	)
	setConnected(t, g, now, "p3", false)

	// Two connected participants: the proposer's yes (1) is not a majority of 2.
	apply(t, g, now, "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p2"})
	if c := cell(t, liveResult(t, g), "p1", 0); c.Points != 10 {
		t.Errorf("1 of 2 connected must not merge, got %+v", c)
	}
	apply(t, g, now, "p2", VoteMerge{ProposalID: 1, Yes: true})
	if c := cell(t, liveResult(t, g), "p1", 0); c.Points != 5 {
		t.Errorf("2 of 2 connected merges, got %+v", c)
	}

	// A yes from someone who is disconnected does not count.
	apply(t, g, now, "p1", ProposeMerge{Category: 1, PlayerA: "p1", PlayerB: "p2"})
	apply(t, g, now, "p3", VoteMerge{ProposalID: 2, Yes: true})
	if c := cell(t, liveResult(t, g), "p1", 1); c.Points != 10 {
		t.Errorf("a disconnected player's yes must be ignored, got %+v", c)
	}
	if pv := g.View("p1").Round.Proposals; pv[1].Yes != 1 || pv[1].Passed {
		t.Errorf("view proposals = %+v", pv)
	}
}

func TestMergeThresholdsByPlayerCount(t *testing.T) {
	tests := []struct {
		players, yes int
		want         bool
	}{
		{2, 1, false}, {2, 2, true},
		{3, 1, false}, {3, 2, true},
		{4, 2, false}, {4, 3, true},
		{5, 2, false}, {5, 3, true},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d players %d yes", tt.players, tt.yes), func(t *testing.T) {
			answers := make([][]string, tt.players)
			for i := range answers {
				answers[i] = []string{fmt.Sprintf("A%d", i), "Argentina", "Alce"}
			}
			g, now := newReview(t, answers...)
			apply(t, g, now, "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p2"}) // proposer = 1 yes
			for i := 2; i <= tt.yes; i++ {
				apply(t, g, now, pid(i), VoteMerge{ProposalID: 1, Yes: true})
			}
			if merged := cell(t, liveResult(t, g), "p1", 0).Shared; merged != tt.want {
				t.Errorf("merged = %v, want %v", merged, tt.want)
			}
		})
	}
}

func TestVoteMergeGuards(t *testing.T) {
	g, now := newReview(t, ansP1, []string{"Anna", "Alemania", "Alce"})
	apply(t, g, now, "p3", Join{Name: "Spectator"})
	apply(t, g, now, "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p2"})

	expectErr(t, g, now, "p2", VoteMerge{ProposalID: 99, Yes: true}, ErrUnknownProposal)
	expectErr(t, g, now, "p3", VoteMerge{ProposalID: 1, Yes: true}, ErrNotParticipant)
	h := newPlaying(t, 2, nil)
	expectErr(t, h, tStart.Add(time.Second), "p1", VoteMerge{ProposalID: 1, Yes: true}, ErrWrongPhase)
}

func TestMergeProposalIDsAreUnique(t *testing.T) {
	g, now := newReview(t,
		[]string{"Ana", "Argentina", "Alce"},
		[]string{"Anna", "Alemania", "Ardilla"},
		[]string{"Alba", "Angola", "Alacran"},
	)
	apply(t, g, now, "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p2"})
	apply(t, g, now, "p1", ProposeMerge{Category: 1, PlayerA: "p1", PlayerB: "p2"})
	apply(t, g, now, "p1", ProposeMerge{Category: 0, PlayerA: "p1", PlayerB: "p3"})
	for i, pr := range g.Round.Proposals {
		if pr.ID != i+1 {
			t.Errorf("proposal %d has id %d", i, pr.ID)
		}
	}
}

func TestFinishReviewAddsScores(t *testing.T) {
	g, now := newReview(t,
		[]string{"Ana", "Argentina", "Alce"},
		[]string{"Ana", "Alemania", ""},
		[]string{"Alba", "Angola", "Brisa"},
	)
	expectErr(t, g, now, "p2", FinishReview{}, ErrNotHost)
	h := newPlaying(t, 2, nil)
	expectErr(t, h, tStart.Add(time.Second), "p1", FinishReview{}, ErrWrongPhase)

	events := apply(t, g, now, "p1", FinishReview{})
	if e, ok := hasEvent(events, EventRoundScored); !ok || e.Round != 1 {
		t.Errorf("events = %+v", events)
	}
	if g.Phase != PhaseRoundSummary {
		t.Fatalf("phase = %s", g.Phase)
	}
	// nombre: Ana/Ana shared (5,5), Alba unique (10).  pais: all unique (10 each).
	// animal: Alce 10, empty 0, wrong-letter 0.
	want := map[string]int{"p1": 5 + 10 + 10, "p2": 5 + 10 + 0, "p3": 10 + 10 + 0}
	for id, score := range want {
		if got := playerByID(t, g, id).TotalScore; got != score {
			t.Errorf("%s total = %d, want %d", id, got, score)
		}
	}
	if len(g.History) != 1 || g.History[0].Round != 1 || g.History[0].Letter != "A" {
		t.Fatalf("history = %+v", g.History)
	}
	for id, score := range want {
		if got := g.History[0].Players[id].Total; got != score {
			t.Errorf("history %s = %d, want %d", id, got, score)
		}
	}

	// Review commands are no longer accepted.
	expectErr(t, g, now, "p2", VoteReject{Category: 0, Target: "p1"}, ErrWrongPhase)
	expectErr(t, g, now, "p1", FinishReview{}, ErrWrongPhase)
}

func TestSummaryResultIsFrozen(t *testing.T) {
	// Disconnections after the review must not rescore a finished round.
	g, now := newReview(t, ansP1, ansP1, ansP1)
	apply(t, g, now, "p2", VoteReject{Category: 0, Target: "p1"})
	apply(t, g, now, "p3", VoteReject{Category: 0, Target: "p1"})
	apply(t, g, now, "p1", FinishReview{})
	before := g.View("p1").Round.Result.Players["p1"].Total

	setConnected(t, g, now, "p2", false)
	setConnected(t, g, now, "p3", false)
	// p1 lost nombre (rejected) and shares the other two categories: 0+5+5.
	if after := g.View("p1").Round.Result.Players["p1"].Total; after != before || before != 10 {
		t.Errorf("total changed from %d to %d (want 10 frozen)", before, after)
	}
}

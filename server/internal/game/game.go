package game

import (
	"slices"
	"time"
)

// Phase is the state-machine phase of a game.
type Phase string

// Phases, in the order a game goes through them. The engine has no separate
// "reveal" phase: the letter is revealed by the transition letter -> playing.
const (
	PhaseLobby        Phase = "lobby"
	PhaseLetter       Phase = "letter"
	PhasePlaying      Phase = "playing"
	PhaseGrace        Phase = "grace"
	PhaseReview       Phase = "review"
	PhaseRoundSummary Phase = "round_summary"
	PhaseFinal        Phase = "final"
)

// Avatar is a player's emoji and colour.
type Avatar struct {
	Emoji string `json:"emoji"`
	Color string `json:"color"`
}

// Player is a seat in the game.
type Player struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar Avatar `json:"avatar"`
	// Connected is false while the player has no open socket. Disconnected
	// players keep their seat all game.
	Connected bool `json:"connected"`
	// ConnectedAt is when the player last became connected; it decides who
	// becomes host when the host leaves.
	ConnectedAt time.Time `json:"connectedAt"`
	// JoinedSeq orders players by joining time (and drives the chooser
	// rotation); it is unique and increasing.
	JoinedSeq int `json:"joinedSeq"`
	// JoinedRound is the first round the player takes part in.
	JoinedRound int `json:"joinedRound"`
	// Left is set for players who left or were kicked after the game started.
	Left       bool `json:"left"`
	TotalScore int  `json:"totalScore"`
}

// RejectVote is one player's vote to reject another player's answer.
type RejectVote struct {
	Category int    `json:"category"`
	Target   string `json:"target"`
	Voter    string `json:"voter"`
}

// MergeVote is one player's vote on a merge proposal.
type MergeVote struct {
	Voter string `json:"voter"`
	Yes   bool   `json:"yes"`
}

// MergeProposal proposes that two answers in a category are the same.
type MergeProposal struct {
	ID         int         `json:"id"`
	Category   int         `json:"category"`
	PlayerA    string      `json:"playerA"`
	PlayerB    string      `json:"playerB"`
	ProposedBy string      `json:"proposedBy"`
	Votes      []MergeVote `json:"votes"`
}

// StopVote is a player's request that everybody stops although they have not
// filled every category. The voters are the connected participants other than
// the asker, evaluated live like the review votes.
type StopVote struct {
	CallerID string    `json:"callerId"`
	EndsAt   time.Time `json:"endsAt"`
	// Yes holds the ids that voted yes (a "no" ends the vote at once, so no
	// "no" is ever stored).
	Yes []string `json:"yes"`
}

// Round is the state of the round in progress (or the last finished one).
type Round struct {
	Number int `json:"number"`
	// Letter is empty until the chooser stops the alphabet.
	Letter string `json:"letter"`
	// LetterStartedAt is when the silent alphabet started; it is never sent
	// to clients (it would reveal the letter).
	LetterStartedAt time.Time `json:"letterStartedAt"`
	// Participants are the players that take part, in join order. Mid-game
	// joiners and spectators are not in it.
	Participants []string `json:"participants"`
	// Answers holds each participant's trimmed answer per category.
	Answers map[string][]string `json:"answers"`
	// CallerID is who called Basta; empty on a time-limit lock.
	CallerID string `json:"callerId,omitempty"`
	// PlayingEndsAt is the time-limit deadline (zero when there is no limit).
	PlayingEndsAt time.Time `json:"playingEndsAt,omitzero"`
	// GraceEndsAt is the instant inputs lock (zero before basta/time-up).
	GraceEndsAt time.Time `json:"graceEndsAt,omitzero"`
	// StopVote is the open vote to stop the round (nil when there is none).
	StopVote *StopVote `json:"stopVote,omitempty"`
	// ReviewCategory is the category index shown in the review.
	ReviewCategory int             `json:"reviewCategory"`
	RejectVotes    []RejectVote    `json:"rejectVotes"`
	Proposals      []MergeProposal `json:"proposals"`
}

// Game is the whole state of one room's game. It is a plain value that
// round-trips through encoding/json in every phase. It is not safe for
// concurrent use: the room goroutine owns it.
type Game struct {
	Phase    Phase    `json:"phase"`
	Players  []Player `json:"players"`
	HostID   string   `json:"hostId"`
	Settings Settings `json:"settings"`
	// UsedLetters are the letters already played, in order.
	UsedLetters []string `json:"usedLetters"`
	// RoundNum is the current round number (0 in the lobby).
	RoundNum int `json:"roundNum"`
	// ChooserID is the chooser of the current round.
	ChooserID  string `json:"chooserId"`
	ChooserSeq int    `json:"chooserSeq"`
	// Round is nil in the lobby.
	Round *Round `json:"round,omitempty"`
	// History holds the scored result of every finished round.
	History []RoundResult `json:"history"`
	// Winners is set in the final phase (several on a tie).
	Winners []string `json:"winners,omitempty"`
	// Banned lists kicked player ids; they cannot join again.
	Banned         []string `json:"banned,omitempty"`
	NextSeq        int      `json:"nextSeq"`
	NextProposalID int      `json:"nextProposalId"`
}

// NewGame creates a game in the lobby with host as its first player and host,
// default settings. The host is marked connected at now.
func NewGame(host Player, now time.Time) *Game {
	return &Game{
		Phase: PhaseLobby,
		Players: []Player{{
			ID:          host.ID,
			Name:        cleanText(host.Name),
			Avatar:      host.Avatar,
			Connected:   true,
			ConnectedAt: now,
			JoinedSeq:   1,
			JoinedRound: 1,
		}},
		HostID:         host.ID,
		Settings:       DefaultSettings(),
		UsedLetters:    []string{},
		History:        []RoundResult{},
		NextSeq:        2,
		NextProposalID: 1,
	}
}

// Apply runs one command by actor (a player id) at time now. It first
// advances any time-based transition that is due (see Tick), so a command
// arriving after a deadline sees the new phase. The returned events include
// those of the transitions. On error the command had no effect, but events
// from due transitions may still be returned.
func (g *Game) Apply(now time.Time, actor string, cmd Command) ([]Event, error) {
	events := g.Tick(now)
	more, err := g.apply(now, actor, cmd)
	return append(events, more...), err
}

func (g *Game) apply(now time.Time, actor string, cmd Command) ([]Event, error) {
	if cmd == nil {
		return nil, ErrUnknownCommand
	}
	if c, ok := cmd.(Join); ok {
		return nil, g.join(now, actor, c)
	}
	p := g.seat(actor)
	if p == nil {
		return nil, ErrUnknownPlayer
	}
	switch c := cmd.(type) {
	case Leave:
		return g.dropPlayer(now, p.ID, false), nil
	case Kick:
		return g.kick(now, p, c)
	case UpdateSettings:
		return nil, g.updateSettings(p, c)
	case StartGame:
		return g.startGame(now, p)
	case StopLetter:
		return g.stopLetter(now, p)
	case SetAnswer:
		return nil, g.setAnswer(p, c)
	case Basta:
		return g.basta(now, p)
	case AskStop:
		return g.askStop(now, p)
	case VoteStop:
		return g.voteStop(now, p, c)
	case VoteReject:
		return nil, g.voteReject(p, c)
	case ProposeMerge:
		return nil, g.proposeMerge(p, c)
	case VoteMerge:
		return nil, g.voteMerge(p, c)
	case ReviewNav:
		return nil, g.reviewNav(p, c)
	case FinishReview:
		return g.finishReview(p)
	case NextRound:
		return g.nextRound(now, p)
	case EndGame:
		return g.endGame(p)
	case Rematch:
		return g.rematch(p)
	}
	return nil, ErrUnknownCommand
}

// Tick applies every time-based transition that is due at now: the chooser
// auto-stop, the time-limit expiry, and the end of the grace period plus late
// update allowance. All deadlines are exclusive: a transition happens when
// now >= deadline. It is safe to call at any time.
//
// A late Tick catches up: each transition is dated at its own deadline, not
// at now, and the loop then applies any later deadline that has also already
// passed. For example the chooser auto-stop reveals the letter at
// LetterStartedAt+ChooserTimeout, so the round's time limit starts there even
// if the tick is processed much later (and may then expire in the same call).
func (g *Game) Tick(now time.Time) []Event {
	var events []Event
	for {
		deadline, ok := g.NextDeadline()
		if !ok || now.Before(deadline) {
			return events
		}
		switch g.Phase {
		case PhaseLetter:
			avail := AvailableLetters(g.Settings.Lang, g.Settings.ExcludeHardLetters, g.UsedLetters)
			events = append(events, g.revealLetter(deadline, LetterAt(avail, ChooserTimeout))...)
		case PhasePlaying:
			if v := g.Round.StopVote; v != nil && (g.Round.PlayingEndsAt.IsZero() || v.EndsAt.Before(g.Round.PlayingEndsAt)) {
				events = append(events, g.endStopVote(StopReasonTimeout)...)
				continue
			}
			// Time limit: enter grace with no caller; inputs are already
			// locked at the deadline, so the phase only lasts for the
			// late-update allowance. An open vote to stop ends with the round.
			events = append(events, g.endStopVote(StopReasonEnded)...)
			g.Phase = PhaseGrace
			g.Round.CallerID = ""
			g.Round.GraceEndsAt = g.Round.PlayingEndsAt
			events = append(events, Event{Type: EventTimeUp, Round: g.RoundNum})
		case PhaseGrace:
			g.Phase = PhaseReview
			g.Round.ReviewCategory = 0
			events = append(events, Event{Type: EventRoundLocked, Round: g.RoundNum})
		}
	}
}

// NextDeadline returns the next instant at which Tick will change the state,
// if any, so the room layer can arm a single timer:
//
//   - letter: LetterStartedAt + ChooserTimeout
//   - playing: the time-limit deadline, when a limit is set, or the end of
//     the open vote to stop, whichever comes first
//   - grace: GraceEndsAt + LateUpdateAllowance
func (g *Game) NextDeadline() (time.Time, bool) {
	if g.Round == nil {
		return time.Time{}, false
	}
	switch g.Phase {
	case PhaseLetter:
		return g.Round.LetterStartedAt.Add(ChooserTimeout), true
	case PhasePlaying:
		limit := g.Round.PlayingEndsAt
		if v := g.Round.StopVote; v != nil && (limit.IsZero() || v.EndsAt.Before(limit)) {
			return v.EndsAt, true
		}
		return limit, !limit.IsZero()
	case PhaseGrace:
		return g.Round.GraceEndsAt.Add(LateUpdateAllowance), true
	}
	return time.Time{}, false
}

// seat returns the seated (not left) player with the given id, or nil. The
// pointer is only valid until g.Players is next modified.
func (g *Game) seat(id string) *Player {
	for i := range g.Players {
		if g.Players[i].ID == id && !g.Players[i].Left {
			return &g.Players[i]
		}
	}
	return nil
}

func (g *Game) seatedCount() int {
	n := 0
	for _, p := range g.Players {
		if !p.Left {
			n++
		}
	}
	return n
}

func (g *Game) isParticipant(id string) bool {
	return g.Round != nil && slices.Contains(g.Round.Participants, id)
}

// requireHost checks that p is the host.
func (g *Game) requireHost(p *Player) error {
	if p.ID != g.HostID {
		return ErrNotHost
	}
	return nil
}

// CleanName removes invisible format characters from a nickname, trims and
// collapses its whitespace, and checks it is non-empty and at most MaxNameLen
// runes; otherwise it returns ErrInvalidName. A nickname made only of
// invisible characters is therefore refused.
// The room layer can use it to validate a nickname before creating a game.
func CleanName(name string) (string, error) {
	name = cleanText(name)
	if name == "" || len([]rune(name)) > MaxNameLen {
		return "", ErrInvalidName
	}
	return name, nil
}

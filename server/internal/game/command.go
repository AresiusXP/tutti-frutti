package game

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Command type names. They match the client-to-server WebSocket message names
// in DESIGN.md section 5. Connection changes are not commands: a client must
// not be able to flip its own connected flag, so the room layer reports them
// through Game.SetConnected instead.
const (
	CmdJoin           = "join"
	CmdLeave          = "leave"
	CmdUpdateSettings = "update_settings"
	CmdKick           = "kick"
	CmdStartGame      = "start_game"
	CmdStopLetter     = "stop_letter"
	CmdSetAnswer      = "set_answer"
	CmdBasta          = "basta"
	CmdAskStop        = "ask_stop"
	CmdVoteStop       = "vote_stop"
	CmdVoteReject     = "vote_reject"
	CmdProposeMerge   = "propose_merge"
	CmdVoteMerge      = "vote_merge"
	CmdReviewNav      = "review_nav"
	CmdAskJudge       = "ask_judge"
	CmdFinishReview   = "finish_review"
	CmdNextRound      = "next_round"
	CmdEndGame        = "end_game"
	CmdRematch        = "rematch"
)

// Command is an intent sent by a player. Commands are passed to Game.Apply by
// value.
type Command interface {
	// Type returns the wire name of the command.
	Type() string
}

// Join seats the actor in the game. Joining again while seated just marks
// the player connected.
type Join struct {
	Name   string `json:"name"`
	Avatar Avatar `json:"avatar"`
}

// Leave removes the actor from the game.
type Leave struct{}

// UpdateSettings replaces the lobby settings (host only, lobby only).
type UpdateSettings struct {
	Settings Settings `json:"settings"`
}

// Kick removes another player (host only).
type Kick struct {
	PlayerID string `json:"playerId"`
}

// StartGame starts the first round (host only, lobby only).
type StartGame struct{}

// StopLetter stops the silent alphabet (chooser only).
type StopLetter struct{}

// SetAnswer stores the actor's answer for one category. An empty Text
// clears it.
type SetAnswer struct {
	Category int    `json:"category"`
	Text     string `json:"text"`
}

// Basta calls Basta/Stop!; it needs every category filled with an answer that
// starts with the letter.
type Basta struct{}

// VoteReject votes to reject Target's answer in Category. Voting twice is the
// same as voting once; Withdraw removes the vote.
type VoteReject struct {
	Category int    `json:"category"`
	Target   string `json:"targetPlayer"`
	Withdraw bool   `json:"withdraw,omitempty"`
}

// ProposeMerge proposes that the answers of PlayerA and PlayerB in Category
// are the same. The proposer counts as a yes vote.
type ProposeMerge struct {
	Category int    `json:"category"`
	PlayerA  string `json:"playerA"`
	PlayerB  string `json:"playerB"`
}

// VoteMerge votes on a merge proposal; voting again overwrites the vote.
type VoteMerge struct {
	ProposalID int  `json:"proposalId"`
	Yes        bool `json:"yes"`
}

// ReviewNav moves the shared review card to a category (host only).
type ReviewNav struct {
	Category int `json:"category"`
}

// FinishReview ends the review and scores the round (host only).
type FinishReview struct{}

// NextRound starts the next round or ends the game (host only).
type NextRound struct{}

// EndGame ends the game from the round summary (host only).
type EndGame struct{}

// Rematch returns to the lobby with the same players and settings (host only).
type Rematch struct{}

// AskStop asks every other connected participant to stop the round although
// the actor has not filled every category (playing phase, Settings.StopVote).
// When every category is filled the actor calls Basta instead.
type AskStop struct{}

// VoteStop answers the open vote to stop. Any "no" rejects the vote at once;
// it passes when every other connected participant voted yes.
type VoteStop struct {
	Yes bool `json:"yes"`
}

// Type implementations.
func (Join) Type() string           { return CmdJoin }
func (Leave) Type() string          { return CmdLeave }
func (UpdateSettings) Type() string { return CmdUpdateSettings }
func (Kick) Type() string           { return CmdKick }
func (StartGame) Type() string      { return CmdStartGame }
func (StopLetter) Type() string     { return CmdStopLetter }
func (SetAnswer) Type() string      { return CmdSetAnswer }
func (Basta) Type() string          { return CmdBasta }
func (AskStop) Type() string        { return CmdAskStop }
func (VoteStop) Type() string       { return CmdVoteStop }
func (VoteReject) Type() string     { return CmdVoteReject }
func (ProposeMerge) Type() string   { return CmdProposeMerge }
func (VoteMerge) Type() string      { return CmdVoteMerge }
func (ReviewNav) Type() string      { return CmdReviewNav }
func (FinishReview) Type() string   { return CmdFinishReview }
func (NextRound) Type() string      { return CmdNextRound }
func (EndGame) Type() string        { return CmdEndGame }
func (Rematch) Type() string        { return CmdRematch }

// ParseCommand decodes the payload of a WebSocket message of type t into a
// Command. An empty or null payload is accepted for commands without fields.
// It returns ErrUnknownCommand for an unknown type (this includes
// "set_connected" and every other message that is not a player intent) and a
// wrapped ErrBadPayload for malformed JSON.
func ParseCommand(t string, raw json.RawMessage) (Command, error) {
	switch t {
	case CmdJoin:
		return decode[Join](raw)
	case CmdLeave:
		return decode[Leave](raw)
	case CmdUpdateSettings:
		return decode[UpdateSettings](raw)
	case CmdKick:
		return decode[Kick](raw)
	case CmdStartGame:
		return decode[StartGame](raw)
	case CmdStopLetter:
		return decode[StopLetter](raw)
	case CmdSetAnswer:
		return decode[SetAnswer](raw)
	case CmdBasta:
		return decode[Basta](raw)
	case CmdAskStop:
		return decode[AskStop](raw)
	case CmdVoteStop:
		return decode[VoteStop](raw)
	case CmdVoteReject:
		return decode[VoteReject](raw)
	case CmdProposeMerge:
		return decode[ProposeMerge](raw)
	case CmdVoteMerge:
		return decode[VoteMerge](raw)
	case CmdReviewNav:
		return decode[ReviewNav](raw)
	case CmdAskJudge:
		return decode[AskJudge](raw)
	case CmdFinishReview:
		return decode[FinishReview](raw)
	case CmdNextRound:
		return decode[NextRound](raw)
	case CmdEndGame:
		return decode[EndGame](raw)
	case CmdRematch:
		return decode[Rematch](raw)
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownCommand, t)
}

func decode[T Command](raw json.RawMessage) (Command, error) {
	var c T
	if b := bytes.TrimSpace(raw); len(b) > 0 && !bytes.Equal(b, []byte("null")) {
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBadPayload, err)
		}
	}
	return c, nil
}

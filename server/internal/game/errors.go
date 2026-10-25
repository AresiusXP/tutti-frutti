package game

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by the engine. Use errors.Is to test for them and
// ErrorCode to get a stable machine-readable code for the wire.
var (
	ErrNotHost          = errors.New("game: only the host can do that")
	ErrWrongPhase       = errors.New("game: not allowed in the current phase")
	ErrNotYourTurn      = errors.New("game: it is not your turn")
	ErrLobbyFull        = errors.New("game: the lobby is full")
	ErrInvalidSettings  = errors.New("game: invalid settings")
	ErrBastaNotAllowed  = errors.New("game: every category must be filled with a valid answer to call basta")
	ErrStopVoteDisabled = errors.New("game: asking everyone to stop is turned off in this game")
	ErrStopVotePending  = errors.New("game: there is already a vote to stop")
	ErrNoStopVote       = errors.New("game: there is no vote to stop")
	ErrStopNotNeeded    = errors.New("game: every category is filled, call basta instead")
	ErrUnknownPlayer    = errors.New("game: unknown player")
	ErrNotParticipant   = errors.New("game: you are not taking part in this round")
	ErrNotEnoughPlayers = errors.New("game: not enough players to start")
	ErrKicked           = errors.New("game: you were kicked from this game")
	ErrCannotKickSelf   = errors.New("game: the host cannot kick themselves")
	ErrInvalidName      = errors.New("game: invalid nickname")
	ErrInvalidAvatar    = errors.New("game: invalid avatar")
	ErrInvalidCategory  = errors.New("game: invalid category index")
	ErrAnswerTooLong    = errors.New("game: answer is too long")
	ErrSelfVote         = errors.New("game: you cannot vote on your own answer")
	ErrEmptyAnswer      = errors.New("game: that answer is empty")
	ErrAlreadySame      = errors.New("game: those answers are already considered the same")
	ErrProposalExists   = errors.New("game: there is already a proposal for those answers")
	ErrInvalidMerge     = errors.New("game: those answers cannot be merged")
	ErrUnknownProposal  = errors.New("game: unknown merge proposal")
	ErrJudgePending     = errors.New("game: the AI judge is already working on that category")
	ErrAlreadyJudged    = errors.New("game: that category was already judged")
	ErrNothingToJudge   = errors.New("game: there is no answer to judge in that category")
	ErrUnknownCommand   = errors.New("game: unknown command")
	ErrBadPayload       = errors.New("game: malformed command payload")
)

// DuplicateCategoryError is returned (wrapped in ErrInvalidSettings) when a
// category clashes with another one. Label is the label that already exists,
// e.g. "País", so the UI can show "Ya existe: País".
type DuplicateCategoryError struct {
	Label string
}

func (e *DuplicateCategoryError) Error() string {
	return fmt.Sprintf("game: category already exists: %s", e.Label)
}

// Unwrap makes errors.Is(err, ErrInvalidSettings) true.
func (e *DuplicateCategoryError) Unwrap() error { return ErrInvalidSettings }

var errorCodes = []struct {
	err  error
	code string
}{
	{ErrNotHost, CodeNotHost},
	{ErrWrongPhase, CodeWrongPhase},
	{ErrNotYourTurn, CodeNotYourTurn},
	{ErrLobbyFull, CodeLobbyFull},
	{ErrInvalidSettings, CodeInvalidSettings},
	{ErrBastaNotAllowed, CodeBastaNotAllowed},
	{ErrStopVoteDisabled, CodeStopVoteDisabled},
	{ErrStopVotePending, CodeStopVotePending},
	{ErrNoStopVote, CodeNoStopVote},
	{ErrStopNotNeeded, CodeStopNotNeeded},
	{ErrUnknownPlayer, CodeUnknownPlayer},
	{ErrNotParticipant, CodeNotParticipant},
	{ErrNotEnoughPlayers, CodeNotEnoughPlayers},
	{ErrKicked, CodeKicked},
	{ErrCannotKickSelf, CodeCannotKickSelf},
	{ErrInvalidName, CodeInvalidName},
	{ErrInvalidAvatar, CodeInvalidAvatar},
	{ErrInvalidCategory, CodeInvalidCategory},
	{ErrAnswerTooLong, CodeAnswerTooLong},
	{ErrSelfVote, CodeSelfVote},
	{ErrEmptyAnswer, CodeEmptyAnswer},
	{ErrAlreadySame, CodeAlreadySame},
	{ErrProposalExists, CodeProposalExists},
	{ErrInvalidMerge, CodeInvalidMerge},
	{ErrUnknownProposal, CodeUnknownProposal},
	{ErrJudgePending, CodeJudgePending},
	{ErrAlreadyJudged, CodeAlreadyJudged},
	{ErrNothingToJudge, CodeNothingToJudge},
	{ErrUnknownCommand, CodeUnknownCommand},
	{ErrBadPayload, CodeBadPayload},
}

// ErrorCode returns the stable snake_case code for an engine error, or
// "internal" if err is not one of the engine's sentinel errors.
func ErrorCode(err error) string {
	for _, e := range errorCodes {
		if errors.Is(err, e.err) {
			return e.code
		}
	}
	return CodeInternal
}

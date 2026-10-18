package game

// Wire error codes: the stable snake_case value of each sentinel error above
// (what ErrorCode returns and what clients map to messages).
const (
	CodeNotHost          = "not_host"
	CodeWrongPhase       = "wrong_phase"
	CodeNotYourTurn      = "not_your_turn"
	CodeLobbyFull        = "lobby_full"
	CodeInvalidSettings  = "invalid_settings"
	CodeBastaNotAllowed  = "basta_not_allowed"
	CodeStopVoteDisabled = "stop_vote_disabled"
	CodeStopVotePending  = "stop_vote_pending"
	CodeNoStopVote       = "no_stop_vote"
	CodeStopNotNeeded    = "stop_not_needed"
	CodeUnknownPlayer    = "unknown_player"
	CodeNotParticipant   = "not_participant"
	CodeNotEnoughPlayers = "not_enough_players"
	CodeKicked           = "kicked"
	CodeCannotKickSelf   = "cannot_kick_self"
	CodeInvalidName      = "invalid_name"
	CodeInvalidAvatar    = "invalid_avatar"
	CodeInvalidCategory  = "invalid_category"
	CodeAnswerTooLong    = "answer_too_long"
	CodeSelfVote         = "self_vote"
	CodeEmptyAnswer      = "empty_answer"
	CodeAlreadySame      = "already_same"
	CodeProposalExists   = "proposal_exists"
	CodeInvalidMerge     = "invalid_merge"
	CodeUnknownProposal  = "unknown_proposal"
	CodeUnknownCommand   = "unknown_command"
	CodeBadPayload       = "bad_payload"
	// CodeInternal is returned for errors that are not engine sentinels.
	CodeInternal = "internal"
)

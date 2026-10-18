package game

// EventType names a game event.
type EventType string

// Event types.
const (
	// EventGameStarted: the host started the game.
	EventGameStarted EventType = "game_started"
	// EventRoundStarted: a new round began (Round, PlayerID = chooser).
	EventRoundStarted EventType = "round_started"
	// EventLetterRevealed: the letter was chosen (Letter).
	EventLetterRevealed EventType = "letter_revealed"
	// EventBasta: a player called Basta (PlayerID, Name).
	EventBasta EventType = "basta"
	// EventStopAsked: a player without every category filled asked everyone
	// to stop (PlayerID, Name). The state carries the open vote.
	EventStopAsked EventType = "stop_asked"
	// EventStopRejected: the vote to stop failed (PlayerID, Name = who asked;
	// Reason "declined" when a voter said no, "timeout" when 15 s passed,
	// "ended" when the round locked or someone called Basta first, or the
	// asker left or lost their connection). A vote that passes is announced
	// as EventBasta (PlayerID = who asked), without a stop_rejected.
	EventStopRejected EventType = "stop_rejected"
	// EventTimeUp: the round time limit expired (no caller).
	EventTimeUp EventType = "time_up"
	// EventRoundLocked: answers are locked and the review begins (Round).
	EventRoundLocked EventType = "round_locked"
	// EventRoundScored: the review finished and the round was scored (Round).
	EventRoundScored EventType = "round_scored"
	// EventKicked: a player was kicked (PlayerID).
	EventKicked EventType = "kicked"
	// EventHostChanged: the host changed (PlayerID = new host, empty if none).
	EventHostChanged EventType = "host_changed"
	// EventGameOver: the game ended (Winners).
	EventGameOver EventType = "game_over"
	// EventRematch: the game went back to the lobby.
	EventRematch EventType = "rematch"
)

// Reasons of EventStopRejected (Event.Reason).
const (
	// StopReasonDeclined: a voter said no.
	StopReasonDeclined = "declined"
	// StopReasonTimeout: StopVoteDuration passed without agreement.
	StopReasonTimeout = "timeout"
	// StopReasonEnded: the round moved on without the vote (Basta by someone
	// else, the time limit) or its asker left or lost their connection.
	StopReasonEnded = "ended"
)

// Event is something the room layer should announce to clients in addition to
// the state snapshot. Only the fields documented on the event type are set.
type Event struct {
	Type     EventType `json:"type"`
	PlayerID string    `json:"playerId,omitempty"`
	Name     string    `json:"name,omitempty"`
	Letter   string    `json:"letter,omitempty"`
	Round    int       `json:"round,omitempty"`
	Winners  []string  `json:"winners,omitempty"`
	// Reason is set on EventStopRejected: StopReasonDeclined,
	// StopReasonTimeout or StopReasonEnded.
	Reason string `json:"reason,omitempty"`
}

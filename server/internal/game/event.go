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

// Event is something the room layer should announce to clients in addition to
// the state snapshot. Only the fields documented on the event type are set.
type Event struct {
	Type     EventType `json:"type"`
	PlayerID string    `json:"playerId,omitempty"`
	Name     string    `json:"name,omitempty"`
	Letter   string    `json:"letter,omitempty"`
	Round    int       `json:"round,omitempty"`
	Winners  []string  `json:"winners,omitempty"`
}

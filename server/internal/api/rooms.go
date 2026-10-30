package api

import "github.com/AresiusXP/tutti-frutti/server/internal/game"

// CreateRoomRequest creates an invite-only room unless visibility is public.
type CreateRoomRequest struct {
	Name       string      `json:"name"`
	Avatar     game.Avatar `json:"avatar"`
	Lang       game.Lang   `json:"lang,omitempty"`
	Visibility string      `json:"visibility,omitempty"`
}

type CreateRoomResponse struct {
	Code string `json:"code"`
}

// RoomInfo is the code-based preflight, not a discovery eligibility promise.
// RoomInstanceID identifies the room (not its reusable code), so a client that
// remembers a room can tell it from an unrelated one that got the same code
// ("resume recent game"). It is not a credential: knowing it authorizes nothing,
// and the join still needs the code and the guest cookie.
type RoomInfo struct {
	Code           string     `json:"code"`
	Phase          game.Phase `json:"phase"`
	Players        int        `json:"players"`
	MaxPlayers     int        `json:"maxPlayers"`
	Lang           game.Lang  `json:"lang"`
	Joinable       bool       `json:"joinable"`
	Visibility     string     `json:"visibility"`
	RoomInstanceID string     `json:"roomInstanceId"`
}

// PublicRoom contains only the metadata needed to choose a waiting lobby. The
// directory is open to everybody, so it never carries text a player typed:
// CategoryLabels holds the built-in categories only (in the lobby language, in
// selection order) and CustomCategories counts the custom ones, which the
// client shows as a localized "Custom category". The real labels are only
// visible inside the room.
type PublicRoom struct {
	Code             string    `json:"code"`
	Lang             game.Lang `json:"lang"`
	Players          int       `json:"players"`
	MaxPlayers       int       `json:"maxPlayers"`
	CategoryLabels   []string  `json:"categoryLabels"`
	CustomCategories int       `json:"customCategories"`
	Rounds           int       `json:"rounds"`
	TimeLimitSeconds int       `json:"timeLimitSeconds"`
}

// PublicRoomsResponse is the body of GET /api/rooms. Paused is true while the
// operator has public games switched off (docs/MODERATION.md, "Absence"): Rooms
// is then empty and the client says that public games are paused.
type PublicRoomsResponse struct {
	Rooms  []PublicRoom `json:"rooms"`
	Paused bool         `json:"paused,omitempty"`
}

// ErrCodePublicUnavailable (403): POST /api/rooms asked for a public room while
// the operator has public games switched off. Private rooms are unaffected. It
// is a decision, not a failure, so it is not a 5xx.
const ErrCodePublicUnavailable = "public_unavailable"

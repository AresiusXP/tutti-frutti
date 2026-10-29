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
type RoomInfo struct {
	Code       string     `json:"code"`
	Phase      game.Phase `json:"phase"`
	Players    int        `json:"players"`
	MaxPlayers int        `json:"maxPlayers"`
	Lang       game.Lang  `json:"lang"`
	Joinable   bool       `json:"joinable"`
	Visibility string     `json:"visibility"`
}

// PublicRoom contains only the metadata needed to choose a waiting lobby.
type PublicRoom struct {
	Code             string    `json:"code"`
	Lang             game.Lang `json:"lang"`
	Players          int       `json:"players"`
	MaxPlayers       int       `json:"maxPlayers"`
	CategoryLabels   []string  `json:"categoryLabels"`
	Rounds           int       `json:"rounds"`
	TimeLimitSeconds int       `json:"timeLimitSeconds"`
}

type PublicRoomsResponse struct {
	Rooms []PublicRoom `json:"rooms"`
}

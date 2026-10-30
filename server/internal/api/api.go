// Package api defines the JSON bodies of room and account REST endpoints.
// Like internal/protocol it is a source for TypeScript types: `go tool tygo
// generate` (server/tygo.yaml) emits web/src/lib/protocol/api.ts from it, and
// CI fails when the generated file is stale. The contract is documented in
// docs/DESIGN.md section 9b.
package api

import "github.com/AresiusXP/tutti-frutti/server/internal/game"

// SessionInfo is the body of GET /api/session.
type SessionInfo struct {
	// Guest is always true: the call mints the guest cookie when it is missing.
	Guest bool `json:"guest"`
	// GoogleEnabled is whether the server can log users in with Google.
	GoogleEnabled bool `json:"googleEnabled"`
	// LoggedIn is whether the request carries a valid, unexpired account
	// session cookie. It is not a database lookup: GET /api/me tells whether
	// the user still exists.
	LoggedIn bool `json:"loggedIn"`
	// Terms tells whether this guest has accepted the current terms.
	Terms TermsStatus `json:"terms"`
}

// Profile is the nickname and avatar a user saved; it prefills the join form.
type Profile struct {
	Nickname string      `json:"nickname"`
	Avatar   game.Avatar `json:"avatar"`
}

// UserInfo is the body of GET /api/me.
type UserInfo struct {
	// ID is the opaque user id (a UUID).
	ID string `json:"id"`
	// Name is the Google display name, cleaned and cut to 16 characters; the
	// default nickname. It can be empty (Google accounts without a name).
	Name string `json:"name"`
	// Profile is what the user saved, null until the first PUT /api/me/profile.
	Profile *Profile `json:"profile" tstype:"Profile | null,required"`
}

// Stats are the lifetime statistics of a user (GET /api/me/stats); all zero
// for a user who has not finished a game yet.
type Stats struct {
	// GamesPlayed counts the finished games the user took part in.
	GamesPlayed int `json:"gamesPlayed"`
	// Wins counts the games the user won (a tie for first place is a win).
	Wins int `json:"wins"`
	// TotalPoints is the sum of the final scores.
	TotalPoints int `json:"totalPoints"`
	// BestRound is the highest score of a single round.
	BestRound int `json:"bestRound"`
	// UniqueAnswers counts the answers worth 10 points (nobody else had them).
	UniqueAnswers int `json:"uniqueAnswers"`
}

// ErrorResponse is the body of every REST error: {"error":"<code>"}.
type ErrorResponse struct {
	Error string `json:"error"`
}

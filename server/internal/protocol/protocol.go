// Package protocol defines the WebSocket wire format shared by the server and
// the web client. It is the single source for TypeScript types: `go tool tygo
// generate` (see server/tygo.yaml) emits web/src/lib/protocol/*.ts from this
// package and internal/game, and CI fails when the generated files are stale.
//
// Every frame, in both directions, is a JSON text message:
//
//	{"t": "<type>", "v": <Version>, "d": { ...payload... }}
//
// Client -> server types: the game commands (game.Cmd* constants, whose
// payloads are the game.* command structs), plus "ping". The first frame of a
// connection MUST be "join" (payload JoinRequest).
//
// Server -> client types: TypeState, TypeEvent, TypeError, TypePong.
package protocol

import (
	"encoding/json"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
)

// Version is the wire protocol version. Bump it on every breaking change of
// frames or of the game.View / game.Settings JSON. A client frame with a
// different "v" is answered with ErrUnsupportedVersion and the connection is
// closed with CloseUnsupportedVersion; the web app then asks the user to
// reload (new version available).
//
// Version 2 added the Dutch lobby language ("nl"): a version 1 client would
// not know the language of a room (labels, alphabet, normalization), so it
// must reload.
const Version = 2

// Frame types that are not game commands.
const (
	// TypePing is sent by clients every PingInterval (empty payload).
	TypePing = "ping"
	// TypePong answers TypePing (payload Pong).
	TypePong = "pong"
	// TypeState carries a full, per-viewer filtered snapshot (payload State).
	TypeState = "state"
	// TypeEvent announces something in addition to the snapshot (payload game.Event).
	TypeEvent = "event"
	// TypeError reports a rejected frame or command (payload ErrorBody).
	TypeError = "error"
)

// Room-level event types (game.Event.Type), in addition to the engine's.
const (
	// EventServerRestarting: the server is shutting down; the client should
	// reconnect with backoff (the room is restored from its snapshot).
	EventServerRestarting game.EventType = "server_restarting"
	// EventReplaced: another connection of the same player took over this seat.
	EventReplaced game.EventType = "replaced"
)

// Envelope is the frame both directions use.
type Envelope struct {
	// T is the frame type.
	T string `json:"t"`
	// V is the protocol version of the sender.
	V int `json:"v"`
	// D is the type-specific payload (absent for empty payloads).
	D json.RawMessage `json:"d,omitempty"`
}

// JoinRequest is the payload of the first client frame ("join"). It also
// reclaims the seat after a reconnect (the guest cookie identifies the player).
type JoinRequest struct {
	// Code is the 4-character room code.
	Code string `json:"code"`
	// Name is the nickname (<= 16 runes after cleaning).
	Name string `json:"name"`
	// Avatar is the chosen emoji and colour.
	Avatar game.Avatar `json:"avatar"`
}

// State is the payload of TypeState: the room as seen by one player.
type State struct {
	// Code is the room code.
	Code string `json:"code"`
	// ServerTime is the server clock in Unix milliseconds when the frame was
	// built. Clients compute their clock offset from it to render deadlines
	// (game.View carries absolute times).
	ServerTime int64 `json:"serverTime"`
	// View is the engine's filtered snapshot for the receiving player.
	View game.View `json:"view"`
}

// Pong is the payload of TypePong.
type Pong struct {
	// ServerTime is the server clock in Unix milliseconds.
	ServerTime int64 `json:"serverTime"`
}

// ErrorBody is the payload of TypeError.
type ErrorBody struct {
	// Code is a stable snake_case code: an Err* constant below, or an engine
	// code from game.ErrorCode (not_host, wrong_phase, lobby_full, ...).
	Code string `json:"code"`
	// Ref is the "t" of the frame that caused the error, when known.
	Ref string `json:"ref,omitempty"`
}

// Protocol-level error codes (engine errors keep their game.ErrorCode value).
const (
	// ErrBadMessage: malformed JSON, unknown frame type or invalid payload.
	ErrBadMessage = "bad_message"
	// ErrUnsupportedVersion: the frame's "v" is not Version.
	ErrUnsupportedVersion = "unsupported_version"
	// ErrRoomNotFound: no room with that code (or it was closed).
	ErrRoomNotFound = "room_not_found"
	// ErrNotJoined: a command arrived before a successful "join".
	ErrNotJoined = "not_joined"
	// ErrAlreadyJoined: "join" sent twice on the same connection.
	ErrAlreadyJoined = "already_joined"
	// ErrRateLimited: the connection exceeded ClientRatePerSecond.
	ErrRateLimited = "rate_limited"
	// ErrMessageTooLarge: the frame exceeded MaxMessageBytes.
	ErrMessageTooLarge = "message_too_large"
	// ErrUnauthorized: missing/invalid guest cookie.
	ErrUnauthorized = "unauthorized"
	// ErrRoomClosed: the room was closed while the frame was in flight.
	ErrRoomClosed = "room_closed"
	// ErrServerBusy: the server refused new rooms (MaxRooms reached).
	ErrServerBusy = "server_busy"
)

// WebSocket close codes. Clients reconnect with exponential backoff after
// 1001, 1006 and 1012, and MUST NOT reconnect after a 40xx code.
const (
	// ClosePolicy: rate limit exceeded or repeated malformed frames.
	ClosePolicy = 1008
	// CloseTooBig: a frame exceeded MaxMessageBytes.
	CloseTooBig = 1009
	// CloseServiceRestart: the server is restarting; reconnect.
	CloseServiceRestart = 1012
	// CloseReplaced: another connection of the same player took over.
	CloseReplaced = 4001
	// CloseKicked: the host kicked this player.
	CloseKicked = 4002
	// CloseUnsupportedVersion: protocol version mismatch; reload the app.
	CloseUnsupportedVersion = 4003
	// CloseRoomClosed: the room no longer exists; go back to the home screen.
	CloseRoomClosed = 4004
	// CloseLeft: the player left the game on purpose.
	CloseLeft = 4005
)

// Transport limits and timings.
const (
	// MaxMessageBytes is the largest accepted frame (a full settings update
	// with 10 custom categories is well below 2 KiB).
	MaxMessageBytes = 4096
	// PingInterval is how often clients send TypePing.
	PingInterval = 20 * time.Second
	// ReadIdleTimeout closes a connection that sent nothing for this long
	// (three missed pings).
	ReadIdleTimeout = 60 * time.Second
	// JoinTimeout is how long a new connection has to send its "join" frame
	// (pings do not count); the transport closes it with ClosePolicy after
	// that. Once the room accepted the connection, the room layer owns the
	// deadline for the seat.
	JoinTimeout = 10 * time.Second
	// WriteTimeout bounds a single frame write.
	WriteTimeout = 10 * time.Second
	// ClientRatePerSecond and ClientBurst bound the frames per connection.
	ClientRatePerSecond = 20
	ClientBurst         = 40
	// SendQueueSize is the per-connection outbound queue; a client that cannot
	// keep up is disconnected (it will reconnect and get a fresh snapshot).
	SendQueueSize = 32
)

// GuestCookie is the name of the signed, HttpOnly cookie that carries the
// guest player ID (see internal/auth).
const GuestCookie = "tf_guest"

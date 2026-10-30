// Package room owns the live games: a Hub (registry, codes, restore, reaper,
// shutdown) and one actor goroutine per room that drives a *game.Game.
//
// This file is the exported contract the transport layer (internal/ws,
// internal/httpapi) and cmd/server are written against; the implementation
// (Hub, actors, persistence) lives in the other files of the package and must
// keep these signatures. See docs/DESIGN.md section 9a.
package room

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/judge"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// Errors returned by the Hub. The transport maps them to protocol error codes
// (ErrRoomNotFound -> room_not_found, ErrTooManyRooms -> server_busy,
// ErrShuttingDown -> server_busy, ErrRoomClosed -> room_closed); engine errors
// (game.Err*) pass through unchanged and map via game.ErrorCode.
var (
	// ErrRoomNotFound: no room with that code.
	ErrRoomNotFound = errors.New("room: not found")
	// ErrTooManyRooms: Config.MaxRooms reached.
	ErrTooManyRooms = errors.New("room: too many rooms")
	// ErrShuttingDown: the hub stopped accepting new rooms and connections.
	ErrShuttingDown = errors.New("room: shutting down")
	// ErrRoomClosed: the room was closed (idle reaper, everyone left, shutdown).
	ErrRoomClosed = errors.New("room: closed")
)

// Clock abstracts time so tests can drive timers deterministically.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// AfterFunc calls f in its own goroutine after d, like time.AfterFunc.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is the subset of *time.Timer the room layer needs.
type Timer interface {
	// Stop prevents the timer from firing; it reports whether it was stopped
	// before firing.
	Stop() bool
}

// Config configures a Hub. Zero values pick the documented defaults.
type Config struct {
	// Store persists snapshots. Required (use store.NewMemory in tests).
	Store store.Store
	// Clock defaults to the system clock.
	Clock Clock
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// Registerer receives the room metrics; nil disables metrics.
	Registerer prometheus.Registerer
	// IdleTTL closes a room with nobody connected for this long (default 30m).
	IdleTTL time.Duration
	// UnclaimedTTL closes a REST-created room nobody ever connected to
	// (default 2m).
	UnclaimedTTL time.Duration
	// JoinTimeout closes a connection that has not completed a successful
	// "join" this long after Connect, with protocol.ClosePolicy (default 10s).
	// A rejected join does not extend it.
	JoinTimeout time.Duration
	// SnapshotEvery is the periodic snapshot interval while a round is being
	// played (default 5s).
	SnapshotEvery time.Duration
	// SnapshotMaxAge: the janitor deletes snapshots older than this
	// (default 6h).
	SnapshotMaxAge time.Duration
	// JanitorEvery is how often the janitor runs (default 1h).
	JanitorEvery time.Duration
	// MaxRooms bounds the number of live rooms (default 1000).
	MaxRooms int
	// CodeSource is the randomness for room codes (default crypto/rand).
	CodeSource io.Reader
	// Recorder receives the results of finished games for the players linked
	// to an account (see UserLinked). Nil disables statistics: no link is kept
	// and nothing is recorded.
	Recorder Recorder
	// Accounts is where a restore checks that the accounts named by a snapshot
	// still exist (see forgetDeletedAccounts). Nil: the Recorder is used when it
	// also keeps accounts (cmd/server passes the store as both), else snapshots
	// are trusted as they are.
	Accounts AccountDirectory
	// Judge is the AI judge of the review; nil turns the feature off (the
	// host is not offered the button and ask_judge is refused).
	Judge Judge
	// BotsEnabled permits new automated seats in eligible public lobbies.
	// Existing seats keep playing when disabled. Default false.
	BotsEnabled bool
	// Filling delays are measured from uninterrupted loneliness (20–30s and
	// 40–55s by default), rather than from the preceding addition.
	BotFirstDelayMin  time.Duration
	BotFirstDelayMax  time.Duration
	BotSecondDelayMin time.Duration
	BotSecondDelayMax time.Duration
	// MaxBots defaults to 2 and is capped at 2.
	MaxBots int
	// BotRand returns a value in [0,n). Calls are serialized by the hub, so an
	// injected deterministic source need not be safe for concurrent use.
	BotRand func(n int) int
	// Terms gates "join": a guest that has not accepted the current terms is
	// refused with terms_required, and a gate that cannot tell refuses with
	// server_busy (fail closed). Nil disables the gate (tests, tools).
	Terms TermsGate
	// IDSource is the randomness of room and match instance ids (default
	// crypto/rand). Calls are serialized by the hub.
	IDSource io.Reader
	// Policy is the index of blocks and moderation restrictions the rooms
	// enforce (see policy.go). Load it before Start; nil enforces nothing.
	Policy *safety.Policy
}

// TermsGate answers whether a guest accepted the current terms. It does I/O,
// so the room consults it on the transport's goroutine (Session.Handle of a
// "join" frame), never inside an actor. *safety.Terms implements it.
type TermsGate interface {
	// Accepted reports whether the guest accepted the current terms. An error
	// means the answer is unknown.
	Accepted(ctx context.Context, guestID string) (bool, error)
}

// Judge answers whether the answers of a category are valid examples of it.
// It is called off the room's goroutine; *judge.Cloudflare implements it.
type Judge interface {
	// Judge returns one result per answer of req. It returns judge.ErrBusy
	// when the judge is out of budget or rate, any other error on failure.
	Judge(ctx context.Context, req judge.Request) ([]judge.Result, error)
}

// CreateRequest describes the host of a new room.
type CreateRequest struct {
	// PlayerID is the guest ID from the cookie (becomes the host's player ID).
	PlayerID string
	// Name is the host's nickname (cleaned with game.CleanName).
	Name string
	// Avatar is the host's emoji and colour.
	Avatar game.Avatar
	// Lang is the lobby language; empty means game.DefaultSettings().Lang.
	Lang game.Lang
	// Visibility is immutable: empty means private; private/public are valid.
	Visibility string
	// UserID is the account the request is signed in with, "" for a guest; it
	// is only used to refuse a restricted account (ErrRestricted).
	UserID string
}

// Info is the public summary of a room (GET /api/rooms/{code}).
type Info = api.RoomInfo

// PublicRoomLister is optional so transports can keep using Rooms test doubles.
type PublicRoomLister interface {
	PublicRooms(lang game.Lang) []api.PublicRoom
}

// Client is one live WebSocket connection, implemented by the transport. The
// room layer calls it from the room's goroutine, so both methods MUST NOT
// block.
type Client interface {
	// PlayerID is the authenticated guest ID of the connection.
	PlayerID() string
	// Send enqueues an already-encoded frame (a JSON text message). It returns
	// false when the client's queue is full; the room then calls Close.
	Send(frame []byte) bool
	// Close starts closing the connection with a WebSocket close code
	// (protocol.Close*) and reason. It is idempotent.
	Close(code int, reason string)
}

// UserLinked is optionally implemented by a Client whose connection carries a
// signed-in account (the transport read a valid session cookie). When the
// client joins, the room links its player to that user; the link decides who
// gets statistics when the game ends. A client that does not implement it, or
// returns "", is a guest.
type UserLinked interface {
	// UserID is the id of the user, or "" for a guest.
	UserID() string
}

// Session is the room-side handle of a connected Client.
type Session interface {
	// Handle delivers one client frame: t is the envelope type and d its
	// payload. The room enforces that "join" comes first (error not_joined
	// otherwise) and answers errors with protocol "error" frames. It never
	// blocks on the room; frames after the room closed are dropped. A "join"
	// frame first asks the terms gate (Config.Terms), which may touch the
	// database for a few milliseconds (3 s at most); no other frame does I/O.
	Handle(t string, d json.RawMessage)
	// Disconnect reports that the socket is gone (idempotent). The room marks
	// the seat disconnected and keeps it.
	Disconnect()
}

// Stats are gauges for metrics and diagnostics.
type Stats struct {
	// Rooms is the number of live rooms.
	Rooms int
	// Connections is the number of attached clients.
	Connections int
}

// Rooms is what the transport needs from the Hub; *Hub implements it.
type Rooms interface {
	// Create makes a new room whose host seat is req (disconnected until the
	// host's socket joins) and returns its code.
	Create(ctx context.Context, req CreateRequest) (string, error)
	// Info returns the public summary of a room.
	Info(code string) (Info, bool)
	// Connect attaches a client to the room with that code. The client is not
	// seated until its first "join" frame is handled.
	Connect(code string, c Client) (Session, error)
	// Stats returns gauges.
	Stats() Stats
}

package store

import (
	"context"
	"encoding/json"
	"time"
)

// Snapshot is the persisted state of one room.
type Snapshot struct {
	// Code is the 4-character room code (primary key).
	Code string
	// State is the room's serialised state (room-layer schema, JSON).
	State json.RawMessage
	// UpdatedAt is when the snapshot was last written; Save stamps it with the
	// store's clock when it is the zero value.
	UpdatedAt time.Time
}

// Store is the persistence contract used by the room layer. All methods must
// be safe for concurrent use and honour ctx cancellation.
type Store interface {
	// Save inserts or replaces the snapshot with the same Code (upsert).
	Save(ctx context.Context, s Snapshot) error
	// Delete removes a snapshot; deleting an unknown code is not an error.
	Delete(ctx context.Context, code string) error
	// LoadAll returns every stored snapshot (used once, on startup).
	LoadAll(ctx context.Context) ([]Snapshot, error)
	// DeleteOlderThan removes snapshots whose UpdatedAt is before t and
	// returns how many were removed.
	DeleteOlderThan(ctx context.Context, t time.Time) (int64, error)
	// Ping checks that the backing store is reachable (used by /readyz).
	Ping(ctx context.Context) error
	// Close releases resources.
	Close()
}

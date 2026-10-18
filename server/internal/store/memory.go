package store

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"time"
)

// Memory is an in-process Store. Nothing survives a restart; it backs tests
// and local runs without a database. It is safe for concurrent use and never
// aliases the byte slices it is given or returns.
type Memory struct {
	mu    sync.RWMutex
	now   func() time.Time
	items map[string]Snapshot

	accMu    sync.Mutex
	users    map[string]*memUser // by user id
	userSubs map[string]string   // Google subject -> user id
}

var (
	_ Store    = (*Memory)(nil)
	_ Accounts = (*Memory)(nil)
	_ Backend  = (*Memory)(nil)
)

// NewMemory returns an empty Memory store using the wall clock.
func NewMemory() *Memory {
	return NewMemoryWithClock(time.Now)
}

// NewMemoryWithClock returns an empty Memory store that stamps snapshots with
// now (time.Now when nil).
func NewMemoryWithClock(now func() time.Time) *Memory {
	if now == nil {
		now = time.Now
	}
	return &Memory{
		now:      now,
		items:    make(map[string]Snapshot),
		users:    make(map[string]*memUser),
		userSubs: make(map[string]string),
	}
}

// Save implements Store.
func (m *Memory) Save(ctx context.Context, s Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateSnapshot(s); err != nil {
		return err
	}
	stamp := s.UpdatedAt
	if stamp.IsZero() {
		stamp = m.now()
	}
	stored := Snapshot{
		Code:      s.Code,
		State:     bytes.Clone(s.State),
		UpdatedAt: normalizeTime(stamp),
	}
	m.mu.Lock()
	m.items[s.Code] = stored
	m.mu.Unlock()
	return nil
}

// Delete implements Store.
func (m *Memory) Delete(ctx context.Context, code string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.items, code)
	m.mu.Unlock()
	return nil
}

// LoadAll implements Store; snapshots are sorted by Code (byte order).
func (m *Memory) LoadAll(ctx context.Context) ([]Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	out := make([]Snapshot, 0, len(m.items))
	for _, s := range m.items {
		s.State = bytes.Clone(s.State)
		out = append(out, s)
	}
	m.mu.RUnlock()
	slices.SortFunc(out, func(a, b Snapshot) int { return strings.Compare(a.Code, b.Code) })
	return out, nil
}

// DeleteOlderThan implements Store; a snapshot updated exactly at t is kept.
func (m *Memory) DeleteOlderThan(ctx context.Context, t time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	cutoff := normalizeTime(t)
	var n int64
	m.mu.Lock()
	for code, s := range m.items {
		if s.UpdatedAt.Before(cutoff) {
			delete(m.items, code)
			n++
		}
	}
	m.mu.Unlock()
	return n, nil
}

// Ping implements Store.
func (m *Memory) Ping(ctx context.Context) error {
	return ctx.Err()
}

// Close implements Store. It is a no-op: a Memory store stays usable.
func (m *Memory) Close() {}

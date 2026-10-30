package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

type blockKey struct{ blocker, blocked string }

// memModeration is the in-memory state of the Moderation methods; it lives
// inside memSafety, under Memory.safeMu, so that a report resolved together
// with its audit record is one atomic step like the database transaction.
type memModeration struct {
	blocks       map[blockKey]safety.Block
	restrictions map[string]safety.Restriction
	actions      []safety.Action
}

// AddBlock implements Moderation.
func (m *Memory) AddBlock(ctx context.Context, b safety.Block, maxBlocks int) (safety.Block, bool, error) {
	if err := ctx.Err(); err != nil {
		return safety.Block{}, false, err
	}
	if err := validateBlock(b); err != nil {
		return safety.Block{}, false, err
	}
	b.CreatedAt = normalizeTime(b.CreatedAt)
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	k := blockKey{b.Blocker, b.Blocked}
	if existing, ok := m.safe.mod.blocks[k]; ok {
		return existing, false, nil
	}
	n := 0
	for key := range m.safe.mod.blocks {
		if key.blocker == b.Blocker {
			n++
		}
	}
	if n >= maxBlocks {
		return safety.Block{}, false, fmt.Errorf("%w: %d", ErrBlockLimit, maxBlocks)
	}
	if m.safe.mod.blocks == nil {
		m.safe.mod.blocks = map[blockKey]safety.Block{}
	}
	m.safe.mod.blocks[k] = b
	return b, true, nil
}

// RemoveBlock implements Moderation.
func (m *Memory) RemoveBlock(ctx context.Context, blocker, blocked string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	k := blockKey{blocker, blocked}
	_, ok := m.safe.mod.blocks[k]
	delete(m.safe.mod.blocks, k)
	return ok, nil
}

func sortBlocks(blocks []safety.Block) {
	slices.SortFunc(blocks, func(a, b safety.Block) int {
		if n := b.CreatedAt.Compare(a.CreatedAt); n != 0 {
			return n
		}
		return strings.Compare(a.Blocked, b.Blocked)
	})
}

// ListBlocks implements Moderation.
func (m *Memory) ListBlocks(ctx context.Context, blocker string) ([]safety.Block, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	var out []safety.Block
	for k, b := range m.safe.mod.blocks {
		if k.blocker == blocker {
			out = append(out, b)
		}
	}
	sortBlocks(out)
	return out, nil
}

// LoadBlocks implements Moderation.
func (m *Memory) LoadBlocks(ctx context.Context) ([]safety.Block, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	out := make([]safety.Block, 0, len(m.safe.mod.blocks))
	for _, b := range m.safe.mod.blocks {
		out = append(out, b)
	}
	sortBlocks(out)
	return out, nil
}

func normalizeRestriction(r safety.Restriction) safety.Restriction {
	r.CreatedAt = normalizeTime(r.CreatedAt)
	if !r.ExpiresAt.IsZero() {
		r.ExpiresAt = normalizeTime(r.ExpiresAt)
	}
	if !r.RevokedAt.IsZero() {
		r.RevokedAt = normalizeTime(r.RevokedAt)
	}
	return r
}

func normalizeAction(a safety.Action) safety.Action {
	a.At = normalizeTime(a.At)
	return a
}

// CreateRestriction implements Moderation.
func (m *Memory) CreateRestriction(ctx context.Context, r safety.Restriction, audit safety.Action) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRestriction(r); err != nil {
		return err
	}
	if err := validateAction(audit); err != nil {
		return err
	}
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	if _, dup := m.safe.mod.restrictions[r.ID]; dup {
		return fmt.Errorf("%w: restriction %q exists", ErrInvalidModeration, r.ID)
	}
	// One restriction in force per guest. The check and the insert are under the
	// same lock, like the transaction of the database store.
	for _, other := range m.safe.mod.restrictions {
		if other.GuestID == r.GuestID && other.ActiveAt(normalizeTime(r.CreatedAt)) {
			return fmt.Errorf("%w: %q", ErrAlreadyRestricted, r.GuestID)
		}
	}
	if m.safe.mod.restrictions == nil {
		m.safe.mod.restrictions = map[string]safety.Restriction{}
	}
	m.safe.mod.restrictions[r.ID] = normalizeRestriction(r)
	m.safe.mod.actions = append(m.safe.mod.actions, normalizeAction(audit))
	return nil
}

// RevokeRestriction implements Moderation.
func (m *Memory) RevokeRestriction(ctx context.Context, id string, now time.Time, audit safety.Action) (safety.Restriction, error) {
	if err := ctx.Err(); err != nil {
		return safety.Restriction{}, err
	}
	if err := validateAction(audit); err != nil {
		return safety.Restriction{}, err
	}
	now = normalizeTime(now)
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	r, ok := m.safe.mod.restrictions[id]
	switch {
	case !ok:
		return safety.Restriction{}, fmt.Errorf("%w: %q", ErrRestrictionNotFound, id)
	case !r.ActiveAt(now):
		return r, fmt.Errorf("%w: %q", ErrRestrictionEnded, id)
	}
	r.RevokedAt = now
	m.safe.mod.restrictions[id] = r
	m.safe.mod.actions = append(m.safe.mod.actions, normalizeAction(audit))
	return r, nil
}

// GetRestriction implements Moderation.
func (m *Memory) GetRestriction(ctx context.Context, id string) (safety.Restriction, error) {
	if err := ctx.Err(); err != nil {
		return safety.Restriction{}, err
	}
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	r, ok := m.safe.mod.restrictions[id]
	if !ok {
		return safety.Restriction{}, fmt.Errorf("%w: %q", ErrRestrictionNotFound, id)
	}
	return r, nil
}

// LoadRestrictions implements Moderation.
func (m *Memory) LoadRestrictions(ctx context.Context, now time.Time) ([]safety.Restriction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now = normalizeTime(now)
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	var out []safety.Restriction
	for _, r := range m.safe.mod.restrictions {
		if r.ActiveAt(now) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b safety.Restriction) int {
		if n := a.CreatedAt.Compare(b.CreatedAt); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// ResolveReportAudited implements Moderation.
func (m *Memory) ResolveReportAudited(ctx context.Context, id, status string, now time.Time, audit safety.Action) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateAction(audit); err != nil {
		return err
	}
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	if err := m.resolveReportLocked(id, status, now); err != nil {
		return err
	}
	m.safe.mod.actions = append(m.safe.mod.actions, normalizeAction(audit))
	return nil
}

// RecordAction implements Moderation.
func (m *Memory) RecordAction(ctx context.Context, a safety.Action) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateAction(a); err != nil {
		return err
	}
	m.safeMu.Lock()
	m.safe.mod.actions = append(m.safe.mod.actions, normalizeAction(a))
	m.safeMu.Unlock()
	return nil
}

// ListActions implements Moderation.
func (m *Memory) ListActions(ctx context.Context, limit int) ([]safety.Action, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.safeMu.Lock()
	out := slices.Clone(m.safe.mod.actions)
	m.safeMu.Unlock()
	slices.SortStableFunc(out, func(a, b safety.Action) int { return b.At.Compare(a.At) })
	return out[:min(len(out), clampLimit(limit, 50, 1000))], nil
}

// ClearProfile implements Moderation.
func (m *Memory) ClearProfile(ctx context.Context, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.accMu.Lock()
	defer m.accMu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUserNotFound, userID)
	}
	u.Profile, u.Name = nil, ""
	return nil
}

// PurgeModeration implements Moderation.
func (m *Memory) PurgeModeration(ctx context.Context, now time.Time) (ModerationPurge, error) {
	if err := ctx.Err(); err != nil {
		return ModerationPurge{}, err
	}
	restrictionCut := normalizeTime(safety.RestrictionPurgeBefore(now))
	auditCut := normalizeTime(safety.AuditPurgeBefore(now))
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	var p ModerationPurge
	for id, r := range m.safe.mod.restrictions {
		if end := r.EndedAt(); !end.IsZero() && end.Before(restrictionCut) {
			delete(m.safe.mod.restrictions, id)
			p.Restrictions++
		}
	}
	kept := m.safe.mod.actions[:0]
	for _, a := range m.safe.mod.actions {
		if a.At.Before(auditCut) {
			p.Actions++
			continue
		}
		kept = append(kept, a)
	}
	m.safe.mod.actions = kept
	return p, nil
}

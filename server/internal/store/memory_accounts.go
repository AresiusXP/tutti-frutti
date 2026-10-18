package store

import (
	"context"
	"fmt"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
)

// memUser is a user and everything that hangs off it (what the database
// deletes by cascade).
type memUser struct {
	User
	stats api.Stats
	games map[string]struct{} // ids of the games already recorded
}

// UpsertGoogleUser implements Accounts.
func (m *Memory) UpsertGoogleUser(ctx context.Context, sub, name string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	if err := validateUser(sub, name); err != nil {
		return User{}, err
	}
	now := normalizeTime(m.now())

	m.accMu.Lock()
	defer m.accMu.Unlock()
	if id, ok := m.userSubs[sub]; ok {
		u := m.users[id]
		u.Name = name
		u.LastLoginAt = now
		return cloneUser(u.User), nil
	}
	u := &memUser{
		User:  User{ID: NewID(), Name: name, CreatedAt: now, LastLoginAt: now},
		games: make(map[string]struct{}),
	}
	m.users[u.ID] = u
	m.userSubs[sub] = u.ID
	return cloneUser(u.User), nil
}

// GetUser implements Accounts.
func (m *Memory) GetUser(ctx context.Context, id string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	m.accMu.Lock()
	defer m.accMu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return User{}, fmt.Errorf("%w: %q", ErrUserNotFound, id)
	}
	return cloneUser(u.User), nil
}

// SaveProfile implements Accounts.
func (m *Memory) SaveProfile(ctx context.Context, id string, p api.Profile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateProfile(p); err != nil {
		return err
	}
	m.accMu.Lock()
	defer m.accMu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUserNotFound, id)
	}
	u.Profile = &p
	return nil
}

// DeleteUser implements Accounts.
func (m *Memory) DeleteUser(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.accMu.Lock()
	defer m.accMu.Unlock()
	if _, ok := m.users[id]; !ok {
		return fmt.Errorf("%w: %q", ErrUserNotFound, id)
	}
	delete(m.users, id)
	for sub, uid := range m.userSubs {
		if uid == id {
			delete(m.userSubs, sub)
		}
	}
	return nil
}

// RecordGame implements Accounts.
func (m *Memory) RecordGame(ctx context.Context, rec GameRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateGame(rec); err != nil {
		return err
	}
	m.accMu.Lock()
	defer m.accMu.Unlock()
	for _, r := range firstPerUser(rec.Results) {
		u, ok := m.users[r.UserID]
		if !ok {
			continue
		}
		if _, done := u.games[rec.ID]; done {
			continue
		}
		u.games[rec.ID] = struct{}{}
		u.stats = addResult(u.stats, r)
	}
	return nil
}

// UserStats implements Accounts.
func (m *Memory) UserStats(ctx context.Context, id string) (api.Stats, error) {
	if err := ctx.Err(); err != nil {
		return api.Stats{}, err
	}
	m.accMu.Lock()
	defer m.accMu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return api.Stats{}, fmt.Errorf("%w: %q", ErrUserNotFound, id)
	}
	return u.stats, nil
}

// cloneUser copies the profile pointer so callers never alias the store.
func cloneUser(u User) User {
	if u.Profile != nil {
		p := *u.Profile
		u.Profile = &p
	}
	return u
}

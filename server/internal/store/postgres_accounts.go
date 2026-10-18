package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
)

var _ Backend = (*Postgres)(nil)

const (
	upsertUserSQL = `INSERT INTO users (id, google_sub, name, created_at, last_login_at)
VALUES ($1, $2, $3, $4, $4)
ON CONFLICT (google_sub) DO UPDATE SET name = EXCLUDED.name, last_login_at = EXCLUDED.last_login_at
RETURNING id, name, nickname, avatar_emoji, avatar_color, created_at, last_login_at`
	getUserSQL = `SELECT id, name, nickname, avatar_emoji, avatar_color, created_at, last_login_at
FROM users WHERE id = $1`
	saveProfileSQL = `UPDATE users SET nickname = $2, avatar_emoji = $3, avatar_color = $4 WHERE id = $1`
	deleteUserSQL  = `DELETE FROM users WHERE id = $1`
	userStatsSQL   = `SELECT COALESCE(s.games_played, 0), COALESCE(s.wins, 0), COALESCE(s.total_points, 0),
       COALESCE(s.best_round, 0), COALESCE(s.unique_answers, 0)
FROM users u LEFT JOIN user_stats s ON s.user_id = u.id
WHERE u.id = $1`

	// lockUserSQL takes a share lock on the user's key: a concurrent DeleteUser
	// waits for the transaction, and a user deleted a moment ago is simply not
	// found, so a game is never recorded for somebody who just left.
	lockUserSQL = `SELECT 1 FROM users WHERE id = $1 FOR KEY SHARE`
	// insertGameSQL claims the (user, game) pair; no affected row means it was
	// recorded before.
	insertGameSQL = `INSERT INTO user_games (user_id, game_id, played_at, won, points, best_round, unique_answers)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (user_id, game_id) DO NOTHING`
	addStatsSQL = `INSERT INTO user_stats (user_id, games_played, wins, total_points, best_round, unique_answers, updated_at)
VALUES ($1, 1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id) DO UPDATE SET
  games_played   = user_stats.games_played + 1,
  wins           = user_stats.wins + EXCLUDED.wins,
  total_points   = user_stats.total_points + EXCLUDED.total_points,
  best_round     = GREATEST(user_stats.best_round, EXCLUDED.best_round),
  unique_answers = user_stats.unique_answers + EXCLUDED.unique_answers,
  updated_at     = EXCLUDED.updated_at`
)

// UpsertGoogleUser implements Accounts.
func (p *Postgres) UpsertGoogleUser(ctx context.Context, sub, name string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	if err := validateUser(sub, name); err != nil {
		return User{}, err
	}
	u, err := scanUser(p.pool.QueryRow(ctx, upsertUserSQL, NewID(), sub, name, normalizeTime(time.Now())))
	if err != nil {
		return User{}, fmt.Errorf("%supsert user: %w", opErrPrefix, err)
	}
	return u, nil
}

// GetUser implements Accounts.
func (p *Postgres) GetUser(ctx context.Context, id string) (User, error) {
	if !ValidID(id) {
		return User{}, fmt.Errorf("%w: %q", ErrUserNotFound, id)
	}
	u, err := scanUser(p.pool.QueryRow(ctx, getUserSQL, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return User{}, fmt.Errorf("%w: %q", ErrUserNotFound, id)
	case err != nil:
		return User{}, fmt.Errorf("%sget user: %w", opErrPrefix, err)
	}
	return u, nil
}

func scanUser(row pgx.Row) (User, error) {
	var (
		u                      User
		nick, emoji, color     *string
		createdAt, lastLoginAt time.Time
	)
	if err := row.Scan(&u.ID, &u.Name, &nick, &emoji, &color, &createdAt, &lastLoginAt); err != nil {
		return User{}, err
	}
	if nick != nil && emoji != nil && color != nil {
		u.Profile = &api.Profile{Nickname: *nick}
		u.Profile.Avatar.Emoji, u.Profile.Avatar.Color = *emoji, *color
	}
	u.CreatedAt, u.LastLoginAt = createdAt.UTC(), lastLoginAt.UTC()
	return u, nil
}

// SaveProfile implements Accounts.
func (p *Postgres) SaveProfile(ctx context.Context, id string, prof api.Profile) error {
	if err := validateProfile(prof); err != nil {
		return err
	}
	if !ValidID(id) {
		return fmt.Errorf("%w: %q", ErrUserNotFound, id)
	}
	tag, err := p.pool.Exec(ctx, saveProfileSQL, id, prof.Nickname, prof.Avatar.Emoji, prof.Avatar.Color)
	switch {
	case err != nil:
		return fmt.Errorf("%ssave profile: %w", opErrPrefix, err)
	case tag.RowsAffected() == 0:
		return fmt.Errorf("%w: %q", ErrUserNotFound, id)
	}
	return nil
}

// DeleteUser implements Accounts; user_stats and user_games go with the user
// (ON DELETE CASCADE).
func (p *Postgres) DeleteUser(ctx context.Context, id string) error {
	if !ValidID(id) {
		return fmt.Errorf("%w: %q", ErrUserNotFound, id)
	}
	tag, err := p.pool.Exec(ctx, deleteUserSQL, id)
	switch {
	case err != nil:
		return fmt.Errorf("%sdelete user: %w", opErrPrefix, err)
	case tag.RowsAffected() == 0:
		return fmt.Errorf("%w: %q", ErrUserNotFound, id)
	}
	return nil
}

// RecordGame implements Accounts. The whole game is one transaction; the
// users are processed in id order so that concurrent games with players in
// common take their locks in the same order.
func (p *Postgres) RecordGame(ctx context.Context, rec GameRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateGame(rec); err != nil {
		return err
	}
	results := firstPerUser(rec.Results)
	slices.SortStableFunc(results, func(a, b UserResult) int { return strings.Compare(a.UserID, b.UserID) })
	at := rec.PlayedAt
	if at.IsZero() {
		at = time.Now()
	}
	at = normalizeTime(at)

	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		for _, r := range results {
			var one int
			switch err := tx.QueryRow(ctx, lockUserSQL, r.UserID).Scan(&one); {
			case errors.Is(err, pgx.ErrNoRows):
				continue // the user is gone
			case err != nil:
				return err
			}
			tag, err := tx.Exec(ctx, insertGameSQL, r.UserID, rec.ID, at, r.Won, r.Points, r.BestRound, r.UniqueAnswers)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				continue // recorded before
			}
			wins := 0
			if r.Won {
				wins = 1
			}
			if _, err := tx.Exec(ctx, addStatsSQL, r.UserID, wins, r.Points, r.BestRound, r.UniqueAnswers, at); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("%srecord game %s: %w", opErrPrefix, rec.ID, err)
	}
	return nil
}

// UserStats implements Accounts.
func (p *Postgres) UserStats(ctx context.Context, id string) (api.Stats, error) {
	if !ValidID(id) {
		return api.Stats{}, fmt.Errorf("%w: %q", ErrUserNotFound, id)
	}
	var s api.Stats
	err := p.pool.QueryRow(ctx, userStatsSQL, id).Scan(&s.GamesPlayed, &s.Wins, &s.TotalPoints, &s.BestRound, &s.UniqueAnswers)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return api.Stats{}, fmt.Errorf("%w: %q", ErrUserNotFound, id)
	case err != nil:
		return api.Stats{}, fmt.Errorf("%suser stats: %w", opErrPrefix, err)
	}
	return s, nil
}

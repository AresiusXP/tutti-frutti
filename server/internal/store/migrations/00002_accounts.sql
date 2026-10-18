-- +goose Up
-- Accounts (Phase 4). No email, no tokens: the Google "sub" (an opaque, stable
-- identifier), the display name and what the user chose to save.
CREATE TABLE users (
  id            uuid PRIMARY KEY,
  google_sub    text NOT NULL UNIQUE,
  name          text NOT NULL,
  nickname      text,
  avatar_emoji  text,
  avatar_color  text,
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_login_at timestamptz NOT NULL DEFAULT now(),
  -- A saved profile is complete or absent, never half there.
  CONSTRAINT users_profile_complete CHECK (
    (nickname IS NULL) = (avatar_emoji IS NULL) AND (nickname IS NULL) = (avatar_color IS NULL)
  )
);

-- Lifetime statistics: one row per user, created by the first recorded game.
CREATE TABLE user_stats (
  user_id        uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  games_played   integer NOT NULL DEFAULT 0,
  wins           integer NOT NULL DEFAULT 0,
  total_points   bigint  NOT NULL DEFAULT 0,
  best_round     integer NOT NULL DEFAULT 0,
  unique_answers integer NOT NULL DEFAULT 0,
  updated_at     timestamptz NOT NULL DEFAULT now()
);

-- One row per (user, finished game): what makes recording a game idempotent
-- (retries, a restarted server re-recording a finished game) and what the
-- statistics were built from.
CREATE TABLE user_games (
  user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  game_id        uuid NOT NULL,
  played_at      timestamptz NOT NULL DEFAULT now(),
  won            boolean NOT NULL,
  points         integer NOT NULL,
  best_round     integer NOT NULL,
  unique_answers integer NOT NULL,
  PRIMARY KEY (user_id, game_id)
);

-- +goose Down
DROP TABLE user_games;
DROP TABLE user_stats;
DROP TABLE users;

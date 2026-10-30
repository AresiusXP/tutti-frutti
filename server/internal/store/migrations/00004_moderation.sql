-- +goose Up
-- Blocks, restrictions and the moderation audit log (docs/DESIGN.md, "Safety").
-- Like the other safety tables they have no foreign keys: a block or a
-- restriction names guest ids (the player id of the tf_guest cookie) and, for a
-- restriction, optionally a user id, and must outlive rooms and accounts until
-- their own retention ends.

-- "blocker does not want to share a room with blocked". Kept until the blocker
-- removes it; blocked_name only labels the blocker's own list.
CREATE TABLE blocks (
  blocker_guest text        NOT NULL,
  blocked_guest text        NOT NULL,
  blocked_name  text        NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL,
  PRIMARY KEY (blocker_guest, blocked_guest),
  CONSTRAINT blocks_not_self CHECK (blocker_guest <> blocked_guest)
);
CREATE INDEX blocks_blocked_guest_idx ON blocks (blocked_guest);

-- A moderation restriction of a guest (and, when a report proved the link, of
-- an account). expires_at NULL lasts until revoked_at is set. A row is deleted
-- 90 days after it ended (expired or revoked).
CREATE TABLE restrictions (
  id          uuid        PRIMARY KEY,
  guest_id    text        NOT NULL,
  user_id     uuid,
  reason      text        NOT NULL,
  report_id   uuid,
  created_at  timestamptz NOT NULL,
  expires_at  timestamptz,
  revoked_at  timestamptz,
  operator    text        NOT NULL
);
CREATE INDEX restrictions_guest_idx ON restrictions (guest_id);
CREATE INDEX restrictions_user_idx ON restrictions (user_id) WHERE user_id IS NOT NULL;

-- The audit log of what the operator did (never player-written text). Kept one
-- year.
CREATE TABLE moderation_actions (
  id          uuid        PRIMARY KEY,
  at          timestamptz NOT NULL,
  operator    text        NOT NULL,
  action      text        NOT NULL,
  target_kind text        NOT NULL,
  target_id   text        NOT NULL,
  report_id   uuid,
  details     text        NOT NULL DEFAULT ''
);
CREATE INDEX moderation_actions_at_idx ON moderation_actions (at);

-- +goose Down
DROP TABLE moderation_actions;
DROP TABLE restrictions;
DROP TABLE blocks;

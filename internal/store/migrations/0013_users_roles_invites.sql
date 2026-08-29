-- +goose Up
-- These per-user columns are inert and will stay that way. Reverb is a
-- single-owner local app: separation between people comes from separate
-- instances with separate databases, not from a user_id on every row. The
-- users table holds exactly one row, which exists only as the FK target for
-- the columns below.
--
-- Nothing filters on initiated_by, owner_user_id, plays.user_id or the
-- scrobble_*.user_id columns added later. Do not mistake any of them for a
-- live scoping mechanism -- adding a WHERE on one would enforce nothing.
-- They are left in place because dropping them is churn for no gain.
CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    created_at    INTEGER NOT NULL DEFAULT (unixepoch())
);

ALTER TABLE download_jobs  ADD COLUMN initiated_by  TEXT REFERENCES users(id);
ALTER TABLE synced_playlists ADD COLUMN owner_user_id TEXT REFERENCES users(id);

-- +goose Down
ALTER TABLE synced_playlists DROP COLUMN owner_user_id;
ALTER TABLE download_jobs    DROP COLUMN initiated_by;
DROP TABLE users;
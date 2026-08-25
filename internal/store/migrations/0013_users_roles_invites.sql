-- +goose Up
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
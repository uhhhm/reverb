-- +goose Up
-- Drop the sessions table from 0001. It has never been read or written: there
-- is no login and no session cookie. The browser UI is the household owner by
-- virtue of reaching the loopback listener, and paired devices authenticate
-- with Bearer tokens (device.token_hash) or libp2p peer identity. Leaving an
-- empty sessions table in the schema invites someone to mistake it for a live
-- authentication mechanism.

DROP TABLE IF EXISTS sessions;

-- +goose Down
CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    expires_at INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL DEFAULT (unixepoch())
);

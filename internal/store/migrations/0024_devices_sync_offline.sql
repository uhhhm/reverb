-- +goose Up
CREATE TABLE device (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  is_server  INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  last_seen  INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE TABLE pairing_code (
  code       TEXT PRIMARY KEY,
  expires_at INTEGER NOT NULL,
  used_at    INTEGER,
  used_by_device_id TEXT REFERENCES device(id),
  created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE TABLE sync_change (
  revision   INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id  TEXT NOT NULL REFERENCES device(id),
  entity_type TEXT NOT NULL,
  entity_id   TEXT NOT NULL,
  field       TEXT NOT NULL,
  value_json  TEXT NOT NULL DEFAULT 'null',
  updated_at  INTEGER NOT NULL,
  created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_sync_change_revision ON sync_change(revision);
CREATE INDEX idx_sync_change_entity ON sync_change(entity_type, entity_id);
CREATE TABLE sync_cursor (
  device_id TEXT PRIMARY KEY REFERENCES device(id),
  revision  INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE TABLE offline_set (
  device_id   TEXT NOT NULL REFERENCES device(id),
  playlist_id TEXT NOT NULL REFERENCES synced_playlists(id) ON DELETE CASCADE,
  enabled     INTEGER NOT NULL DEFAULT 1,
  updated_at  INTEGER NOT NULL,
  PRIMARY KEY (device_id, playlist_id)
);
-- Settings key sync_revision is NOT a table; it lives in settings, but AUTOINCREMENT revision is canonical. Also ensure settings key server_device_id can be stored.

-- +goose Down
DROP TABLE IF EXISTS offline_set;
DROP TABLE IF EXISTS sync_cursor;
DROP INDEX IF EXISTS idx_sync_change_entity;
DROP INDEX IF EXISTS idx_sync_change_revision;
DROP TABLE IF EXISTS sync_change;
DROP TABLE IF EXISTS pairing_code;
DROP TABLE IF EXISTS device;

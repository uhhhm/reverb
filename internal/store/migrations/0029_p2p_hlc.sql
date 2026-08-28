-- +goose Up
-- Phase 0 foundation for P2P CRDT: HLC, vector, file manifest, local device, track_override fix.

-- Add HLC + per-device seq to sync_change (default 0 for back-compat; HLC populated going forward)
ALTER TABLE sync_change ADD COLUMN hlc INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sync_change ADD COLUMN seq INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_sync_change_hlc ON sync_change(hlc, device_id);
CREATE INDEX IF NOT EXISTS idx_sync_change_device_seq ON sync_change(device_id, seq);

-- Content-addressed file manifest for full sync
CREATE TABLE IF NOT EXISTS file_manifest (
  canonical_id TEXT PRIMARY KEY,
  content_hash TEXT NOT NULL,
  size         INTEGER NOT NULL,
  rel_path     TEXT NOT NULL,
  mtime        INTEGER NOT NULL,
  device_id    TEXT NOT NULL REFERENCES device(id)
);
CREATE INDEX IF NOT EXISTS idx_file_manifest_hash ON file_manifest(content_hash);
CREATE INDEX IF NOT EXISTS idx_file_manifest_device ON file_manifest(device_id);

-- Per-peer vector clock replaces global revision cursor for P2P (sync_cursor kept for compat this phase)
CREATE TABLE IF NOT EXISTS sync_vector (
  device_id  TEXT PRIMARY KEY REFERENCES device(id),
  seq        INTEGER NOT NULL DEFAULT 0,
  hlc        INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

-- Track override fix: catalog_id is the correct CRDT key (track_id is backend-volatile).
-- Keep track_id for compat, add catalog_id nullable; Phase 1 will migrate reads to catalog_id.
ALTER TABLE track_override ADD COLUMN catalog_id TEXT;
CREATE INDEX IF NOT EXISTS idx_track_override_catalog ON track_override(catalog_id);

-- +goose Down
DROP INDEX IF EXISTS idx_track_override_catalog;
-- SQLite >=3.35 supports DROP COLUMN; guard with IF EXISTS via raw exec may fail on older — best-effort
DROP INDEX IF EXISTS idx_file_manifest_device;
DROP INDEX IF EXISTS idx_file_manifest_hash;
DROP TABLE IF EXISTS sync_vector;
DROP TABLE IF EXISTS file_manifest;
DROP INDEX IF EXISTS idx_sync_change_device_seq;
DROP INDEX IF EXISTS idx_sync_change_hlc;
-- columns dropped last (if supported)
-- ALTER TABLE sync_change DROP COLUMN seq;
-- ALTER TABLE sync_change DROP COLUMN hlc;
-- ALTER TABLE track_override DROP COLUMN catalog_id;

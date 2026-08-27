-- +goose Up
-- Deduplicate server devices if a prior TOCTOU race created duplicates: keep earliest, clean refs, delete rest
DELETE FROM sync_change WHERE device_id IN (SELECT id FROM device WHERE is_server = 1 ORDER BY created_at ASC LIMIT -1 OFFSET 1);
DELETE FROM sync_cursor WHERE device_id IN (SELECT id FROM device WHERE is_server = 1 ORDER BY created_at ASC LIMIT -1 OFFSET 1);
DELETE FROM offline_set WHERE device_id IN (SELECT id FROM device WHERE is_server = 1 ORDER BY created_at ASC LIMIT -1 OFFSET 1);
UPDATE pairing_code SET used_by_device_id = NULL WHERE used_by_device_id IN (SELECT id FROM device WHERE is_server = 1 ORDER BY created_at ASC LIMIT -1 OFFSET 1);
DELETE FROM device WHERE is_server = 1 AND id NOT IN (SELECT id FROM device WHERE is_server = 1 ORDER BY created_at ASC LIMIT 1);
-- Fix sync_change indexes: composite for GetLatestSyncChangeForField, device_id for cleanup, drop redundant PK index
DROP INDEX IF EXISTS idx_sync_change_revision;
DROP INDEX IF EXISTS idx_sync_change_entity;
CREATE INDEX IF NOT EXISTS idx_sync_change_entity_field ON sync_change(entity_type, entity_id, field);
CREATE INDEX IF NOT EXISTS idx_sync_change_device_id ON sync_change(device_id);

-- +goose Down
DROP INDEX IF EXISTS idx_sync_change_entity_field;
DROP INDEX IF EXISTS idx_sync_change_device_id;
CREATE INDEX IF NOT EXISTS idx_sync_change_revision ON sync_change(revision);
CREATE INDEX IF NOT EXISTS idx_sync_change_entity ON sync_change(entity_type, entity_id);

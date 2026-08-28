-- name: AppendSyncChange :one
INSERT INTO sync_change (device_id, entity_type, entity_id, field, value_json, updated_at) VALUES (?, ?, ?, ?, ?, ?) RETURNING revision;

-- name: AppendSyncChangeWithHLC :one
INSERT INTO sync_change (device_id, entity_type, entity_id, field, value_json, updated_at, hlc, seq, sig) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING revision;

-- name: ListSyncChangesSince :many
SELECT revision, device_id, entity_type, entity_id, field, value_json, updated_at, created_at, hlc, seq, sig FROM sync_change WHERE revision > ? ORDER BY revision ASC LIMIT ?;

-- name: ListSyncChangesSinceHLC :many
SELECT revision, device_id, entity_type, entity_id, field, value_json, updated_at, created_at, hlc, seq, sig FROM sync_change WHERE hlc > ? ORDER BY hlc ASC, revision ASC LIMIT ?;

-- name: GetMaxSyncRevision :one
SELECT COALESCE(MAX(revision), 0) AS max_revision FROM sync_change;

-- name: GetMaxHLC :one
SELECT COALESCE(MAX(hlc), 0) AS max_hlc FROM sync_change;

-- name: GetLatestSyncChangeForField :one
SELECT revision, device_id, entity_type, entity_id, field, value_json, updated_at, created_at, hlc, seq, sig FROM sync_change WHERE entity_type = ? AND entity_id = ? AND field = ? ORDER BY revision DESC LIMIT 1;

-- name: GetLatestSyncChangeForFieldByHLC :one
SELECT revision, device_id, entity_type, entity_id, field, value_json, updated_at, created_at, hlc, seq, sig FROM sync_change WHERE entity_type = ? AND entity_id = ? AND field = ? ORDER BY hlc DESC, revision DESC LIMIT 1;

-- name: CountSyncChanges :one
SELECT COUNT(*) FROM sync_change;

-- name: GetSyncCursor :one
SELECT * FROM sync_cursor WHERE device_id = ?;

-- name: UpsertSyncCursor :exec
INSERT INTO sync_cursor (device_id, revision, updated_at) VALUES (?, ?, unixepoch()) ON CONFLICT(device_id) DO UPDATE SET revision = excluded.revision, updated_at = unixepoch();

-- name: DeleteSyncCursor :exec
DELETE FROM sync_cursor WHERE device_id = ?;

-- name: GetSyncVector :one
SELECT * FROM sync_vector WHERE device_id = ?;

-- name: UpsertSyncVector :exec
INSERT INTO sync_vector (device_id, seq, hlc, updated_at) VALUES (?, ?, ?, unixepoch()) ON CONFLICT(device_id) DO UPDATE SET seq = excluded.seq, hlc = excluded.hlc, updated_at = unixepoch();

-- name: ListSyncVectors :many
SELECT * FROM sync_vector;

-- name: DeleteSyncVector :exec
DELETE FROM sync_vector WHERE device_id = ?;

-- name: ListUnsignedSyncChangesForDevice :many
SELECT revision, device_id, entity_type, entity_id, field, value_json, updated_at, created_at, hlc, seq, sig FROM sync_change WHERE device_id = ? AND sig = '' ORDER BY revision ASC;

-- name: UpdateSyncChangeSig :exec
UPDATE sync_change SET sig = ?2 WHERE revision = ?1;

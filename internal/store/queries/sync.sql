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
SELECT revision, device_id, entity_type, entity_id, field, value_json, updated_at, created_at, hlc, seq, sig FROM sync_change WHERE entity_type = ? AND entity_id = ? AND field = ? AND revision NOT IN (SELECT revision FROM sync_nonwinning) ORDER BY revision DESC LIMIT 1;

-- name: GetLatestSyncChangeForFieldByHLC :one
SELECT revision, device_id, entity_type, entity_id, field, value_json, updated_at, created_at, hlc, seq, sig FROM sync_change WHERE entity_type = ? AND entity_id = ? AND field = ? AND revision NOT IN (SELECT revision FROM sync_nonwinning) ORDER BY hlc DESC, revision DESC LIMIT 1;

-- name: GetSyncChangeBySequence :one
SELECT revision FROM sync_change WHERE device_id = ? AND seq = ? LIMIT 1;

-- name: MarkSyncChangeNonwinning :exec
INSERT INTO sync_nonwinning (revision) VALUES (?);

-- name: ListDeletedFileHashes :many
SELECT DISTINCT entity_id FROM sync_change WHERE entity_type = 'file' AND field = '__deleted'
AND revision NOT IN (SELECT revision FROM sync_nonwinning);

-- name: MarkSyncProjectionPending :exec
INSERT INTO sync_projection_pending(revision) VALUES (?);

-- name: CompleteSyncProjection :exec
DELETE FROM sync_projection_pending WHERE revision = ?;

-- name: ListPendingSyncProjections :many
SELECT c.revision, c.device_id, c.entity_type, c.entity_id, c.field, c.value_json,
       c.updated_at, c.created_at, c.hlc, c.seq, c.sig
FROM sync_change c JOIN sync_projection_pending p ON p.revision = c.revision
WHERE c.revision > ? ORDER BY c.revision LIMIT ?;

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

-- name: ListLatestSyncFieldsForEntity :many
-- Highest revision per field, matching GetLatestSyncChangeForField. Reconcile
-- excludes changes retained only to relay author sequence numbers.
SELECT c.revision, c.device_id, c.entity_type, c.entity_id, c.field, c.value_json, c.updated_at, c.created_at, c.hlc, c.seq, c.sig
FROM sync_change c
WHERE c.entity_type = ? AND c.entity_id = ?
  AND c.revision = (
    SELECT c2.revision FROM sync_change c2
    WHERE c2.entity_type = c.entity_type AND c2.entity_id = c.entity_id AND c2.field = c.field
      AND c2.revision NOT IN (SELECT revision FROM sync_nonwinning)
    ORDER BY c2.revision DESC LIMIT 1
  )
ORDER BY c.field ASC;

-- name: ListUnprojectedPlays :many
-- Accepted facts whose projection failed or was interrupted. The log is the
-- durable retry queue; an empty catalog_id retries all missing history.
SELECT s.revision, s.device_id, s.entity_type, s.entity_id, s.field, s.value_json,
       s.updated_at, s.created_at, s.hlc, s.seq, s.sig
FROM sync_change s
WHERE s.entity_type = 'play' AND s.field = 'record'
  -- A row an older build stored unparsed can never project, and json_extract
  -- raises on it, so it is skipped rather than allowed to fail this query for
  -- every catalog identity. The CASE below guards the extract itself, so the
  -- skip does not depend on the planner evaluating this term first.
  AND json_valid(s.value_json)
  AND s.revision NOT IN (SELECT revision FROM sync_nonwinning)
  AND NOT EXISTS (SELECT 1 FROM sync_change d WHERE d.entity_type = 'play' AND d.entity_id = s.entity_id AND d.field = '__deleted')
  AND (CAST(sqlc.arg(catalog_id) AS TEXT) = ''
       OR CASE WHEN json_valid(s.value_json)
               THEN json_extract(s.value_json, '$.catalogId') = sqlc.arg(catalog_id) END)
  AND NOT EXISTS (SELECT 1 FROM plays p WHERE p.id = s.entity_id)
  AND s.revision = (SELECT MAX(c.revision) FROM sync_change c
                   WHERE c.entity_type = s.entity_type AND c.entity_id = s.entity_id AND c.field = s.field
                     AND c.revision NOT IN (SELECT revision FROM sync_nonwinning))
ORDER BY s.revision;

-- name: QuarantineSyncCopy :exec
INSERT INTO sync_quarantine(revision, change_json, reason)
VALUES (?, ?, ?) ON CONFLICT(revision) DO NOTHING;

-- name: DeleteCorruptSyncChange :exec
DELETE FROM sync_change WHERE revision = ?;

-- name: ResetSyncVector :exec
UPDATE sync_vector SET seq = 0, hlc = 0 WHERE device_id = ?;

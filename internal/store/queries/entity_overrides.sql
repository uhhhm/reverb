-- name: GetEntityOverride :one
SELECT * FROM entity_override WHERE entity_type = ? AND entity_id = ?;

-- name: GetEntityOverrideByKey :one
SELECT * FROM entity_override WHERE entity_type = ? AND entity_key = ? LIMIT 1;

-- name: ListEntityOverrides :many
SELECT * FROM entity_override WHERE entity_type = ?;

-- name: UpsertEntityOverride :exec
INSERT INTO entity_override (entity_type, entity_id, entity_key, name, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(entity_type, entity_id) DO UPDATE SET
  entity_key = excluded.entity_key,
  name = excluded.name,
  updated_at = excluded.updated_at;

-- name: DeleteEntityOverride :exec
DELETE FROM entity_override WHERE entity_type = ? AND entity_id = ?;

-- name: DeleteEntityOverrideByKey :exec
DELETE FROM entity_override WHERE entity_type = ? AND entity_key = ?;

-- name: GetEntityCover :one
SELECT * FROM entity_cover WHERE entity_type = ? AND entity_id = ?;

-- name: GetEntityCoverByKey :one
SELECT * FROM entity_cover WHERE entity_type = ? AND entity_key = ? LIMIT 1;

-- name: ListEntityCovers :many
SELECT * FROM entity_cover;

-- name: UpsertEntityCover :exec
INSERT INTO entity_cover (entity_type, entity_id, entity_key, sha256, ext, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(entity_type, entity_id) DO UPDATE SET
  entity_key = excluded.entity_key,
  sha256 = excluded.sha256,
  ext = excluded.ext,
  updated_at = excluded.updated_at;

-- name: DeleteEntityCover :exec
DELETE FROM entity_cover WHERE entity_type = ? AND entity_id = ?;

-- name: DeleteEntityCoverByKey :exec
DELETE FROM entity_cover WHERE entity_type = ? AND entity_key = ?;

-- name: CountEntityCoverRefs :one
SELECT COUNT(*) FROM entity_cover WHERE sha256 = ?;

-- name: RepointEntityCoverKey :exec
UPDATE entity_cover SET entity_key = ? WHERE entity_type = ? AND entity_key = ?;

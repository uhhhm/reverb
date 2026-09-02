-- name: GetTrackCrop :one
SELECT * FROM track_crop WHERE track_id = ?;

-- name: ListTrackCrops :many
SELECT * FROM track_crop;

-- name: UpsertTrackCrop :exec
INSERT INTO track_crop (track_id, start_ms, end_ms, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(track_id) DO UPDATE SET
  start_ms = excluded.start_ms,
  end_ms = excluded.end_ms,
  updated_at = excluded.updated_at;

-- name: DeleteTrackCrop :exec
DELETE FROM track_crop WHERE track_id = ?;

-- name: GetTrackCropByCatalogID :one
SELECT * FROM track_crop WHERE catalog_id = ?;

-- name: UpsertTrackCropByCatalogID :exec
INSERT INTO track_crop (track_id, start_ms, end_ms, updated_at, catalog_id)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(track_id) DO UPDATE SET
  start_ms = excluded.start_ms,
  end_ms = excluded.end_ms,
  updated_at = excluded.updated_at,
  catalog_id = excluded.catalog_id;

-- name: DeleteTrackCropByCatalogID :exec
DELETE FROM track_crop WHERE catalog_id = ?;

-- name: RepointTrackCropCatalog :exec
UPDATE track_crop SET catalog_id = ? WHERE catalog_id = ?;

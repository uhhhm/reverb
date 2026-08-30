-- name: GetTrackLoudness :one
SELECT * FROM track_loudness WHERE track_id = ?;

-- name: UpsertTrackLoudness :exec
INSERT INTO track_loudness (track_id, gain_db, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(track_id) DO UPDATE SET
  gain_db = excluded.gain_db,
  updated_at = excluded.updated_at;

-- name: DeleteTrackLoudness :exec
DELETE FROM track_loudness WHERE track_id = ?;

-- name: UpsertTrackLoudnessByCatalogID :exec
INSERT INTO track_loudness (track_id, gain_db, updated_at, catalog_id)
VALUES (?, ?, ?, ?)
ON CONFLICT(track_id) DO UPDATE SET
  gain_db = excluded.gain_db,
  updated_at = excluded.updated_at,
  catalog_id = excluded.catalog_id;

-- name: GetTrackLoudnessByCatalogID :one
SELECT * FROM track_loudness WHERE catalog_id = ?;

-- name: ListTrackLoudness :many
SELECT * FROM track_loudness;

-- name: GetTrackOverride :one
SELECT * FROM track_override WHERE track_id = ?;

-- name: ListTrackOverrides :many
SELECT * FROM track_override;

-- name: UpsertTrackOverride :exec
INSERT INTO track_override (track_id, title, artist, album, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(track_id) DO UPDATE SET
  title = excluded.title,
  artist = excluded.artist,
  album = excluded.album,
  updated_at = excluded.updated_at;

-- name: DeleteTrackOverride :exec
DELETE FROM track_override WHERE track_id = ?;

-- name: GetTrackOverrideByCatalogID :one
SELECT * FROM track_override WHERE catalog_id = ?;

-- name: UpsertTrackOverrideByCatalogID :exec
INSERT INTO track_override (track_id, title, artist, album, updated_at, catalog_id) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(track_id) DO UPDATE SET title = excluded.title, artist = excluded.artist, album = excluded.album, updated_at = excluded.updated_at, catalog_id = excluded.catalog_id;

-- name: ListTrackOverridesByCatalogIDs :many
SELECT * FROM track_override WHERE catalog_id IN (sqlc.slice('catalog_ids'));

-- name: DeleteTrackOverrideByCatalogID :exec
DELETE FROM track_override WHERE catalog_id = ?;

-- name: RepointTrackOverrideCatalog :exec
UPDATE track_override SET catalog_id = ? WHERE catalog_id = ?;

-- name: GetTrackOverride :one
SELECT * FROM track_override WHERE track_id = ?;

-- name: ListTrackOverrides :many
SELECT * FROM track_override;

-- name: UpsertTrackOverride :exec
INSERT INTO track_override (track_id, title, artist, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(track_id) DO UPDATE SET
  title = excluded.title,
  artist = excluded.artist,
  updated_at = excluded.updated_at;

-- name: DeleteTrackOverride :exec
DELETE FROM track_override WHERE track_id = ?;

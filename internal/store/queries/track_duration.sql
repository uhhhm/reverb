-- name: GetTrackDuration :one
SELECT * FROM track_duration WHERE track_id = ?;

-- name: UpsertTrackDuration :exec
INSERT INTO track_duration (track_id, duration_ms, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(track_id) DO UPDATE SET
  duration_ms = excluded.duration_ms,
  updated_at = excluded.updated_at;

-- name: DeleteTrackDuration :exec
DELETE FROM track_duration WHERE track_id = ?;

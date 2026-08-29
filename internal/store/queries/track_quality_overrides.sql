-- name: GetTrackQualityOverride :one
SELECT * FROM track_quality_override WHERE track_id = ?;

-- name: ListTrackQualityOverrides :many
SELECT * FROM track_quality_override;

-- name: UpsertTrackQualityOverride :exec
INSERT INTO track_quality_override (track_id, quality, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(track_id) DO UPDATE SET
  quality = excluded.quality,
  updated_at = excluded.updated_at;

-- name: DeleteTrackQualityOverride :exec
DELETE FROM track_quality_override WHERE track_id = ?;

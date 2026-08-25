-- name: UpsertOfflineSet :exec
INSERT INTO offline_set (device_id, playlist_id, enabled, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(device_id, playlist_id) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at;

-- name: ListOfflineSetForDevice :many
SELECT * FROM offline_set WHERE device_id = ? ORDER BY playlist_id;

-- name: GetOfflineSetEntry :one
SELECT * FROM offline_set WHERE device_id = ? AND playlist_id = ?;

-- name: DeleteOfflineSetEntry :exec
DELETE FROM offline_set WHERE device_id = ? AND playlist_id = ?;

-- name: DeleteOfflineSetForPlaylist :exec
DELETE FROM offline_set WHERE playlist_id = ?;

-- name: GetFileFetchFailure :one
SELECT * FROM file_fetch_failure WHERE peer_id = ? AND content_hash = ?;

-- name: ListFileFetchFailures :many
SELECT * FROM file_fetch_failure ORDER BY last_failed_at DESC;

-- name: UpsertFileFetchFailure :exec
INSERT INTO file_fetch_failure (peer_id, content_hash, rel_path, reason, detail, attempts, first_failed_at, last_failed_at, next_attempt_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(peer_id, content_hash) DO UPDATE SET
  rel_path = excluded.rel_path,
  reason = excluded.reason,
  detail = excluded.detail,
  attempts = excluded.attempts,
  last_failed_at = excluded.last_failed_at,
  next_attempt_at = excluded.next_attempt_at;

-- name: DeleteFileFetchFailure :exec
DELETE FROM file_fetch_failure WHERE peer_id = ? AND content_hash = ?;

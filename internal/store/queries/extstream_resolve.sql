-- name: GetExtstreamResolve :one
SELECT * FROM extstream_resolve WHERE source = ? AND external_id = ?;

-- name: UpsertExtstreamVideoID :exec
-- Records which upstream track this is. Leaves any still-valid URL alone: the
-- search answer changing does not invalidate a URL already fetched for it.
INSERT INTO extstream_resolve (source, external_id, video_id, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(source, external_id) DO UPDATE SET
  video_id = excluded.video_id,
  updated_at = excluded.updated_at;

-- name: UpsertExtstreamURL :exec
INSERT INTO extstream_resolve (source, external_id, video_id, url, url_expires_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(source, external_id) DO UPDATE SET
  video_id = excluded.video_id,
  url = excluded.url,
  url_expires_at = excluded.url_expires_at,
  updated_at = excluded.updated_at;

-- name: ClearExtstreamURL :exec
-- Drops a URL the upstream rejected, keeping the video id that is still good.
UPDATE extstream_resolve SET url = '', url_expires_at = 0, updated_at = ?
WHERE source = ? AND external_id = ?;

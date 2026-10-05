-- name: InsertDownloadJob :exec
INSERT INTO download_jobs (
    id, dedup_key, request_json, downloader_name, status, progress, error,
    output_path, library_track_id, priority, requested_by, attempts, downloader_ref,
    initiated_by, created_at, started_at, finished_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, unixepoch(), NULL, NULL);

-- name: GetDownloadJob :one
SELECT id, dedup_key, request_json, downloader_name, status, progress, error,
       output_path, library_track_id, cover_art_id, canonical_id, priority, requested_by, attempts,
       downloader_ref, completion_pending, created_at, started_at, finished_at
FROM download_jobs WHERE id = ?;

-- name: GetActiveDownloadJobByDedup :one
SELECT id, dedup_key, request_json, downloader_name, status, progress, error,
       output_path, library_track_id, cover_art_id, canonical_id, priority, requested_by, attempts,
       downloader_ref, completion_pending, created_at, started_at, finished_at
FROM download_jobs
WHERE dedup_key = ? AND status IN ('queued', 'running')
ORDER BY created_at ASC
LIMIT 1;

-- name: GetDownloadJobByDedup :one
SELECT id, dedup_key, request_json, downloader_name, status, progress, error,
       output_path, library_track_id, cover_art_id, canonical_id, priority, requested_by, attempts,
       downloader_ref, completion_pending, created_at, started_at, finished_at
FROM download_jobs
WHERE dedup_key = ?
ORDER BY created_at ASC
LIMIT 1;

-- name: ListDownloadJobs :many
SELECT id, dedup_key, request_json, downloader_name, status, progress, error,
       output_path, library_track_id, cover_art_id, canonical_id, priority, requested_by, attempts,
       downloader_ref, completion_pending, created_at, started_at, finished_at
FROM download_jobs
ORDER BY created_at DESC;

-- name: ListDownloadJobsByStatus :many
SELECT id, dedup_key, request_json, downloader_name, status, progress, error,
       output_path, library_track_id, cover_art_id, canonical_id, priority, requested_by, attempts,
       downloader_ref, completion_pending, created_at, started_at, finished_at
FROM download_jobs
WHERE status = ?
ORDER BY created_at DESC;

-- name: UpdateDownloadJobStatus :exec
UPDATE download_jobs
SET status     = @status,
    started_at  = CASE WHEN @status = 'running' AND started_at IS NULL THEN unixepoch() ELSE started_at END,
    finished_at = CASE WHEN @status = 'completed' OR @status = 'failed' OR @status = 'canceled' THEN unixepoch() ELSE finished_at END
WHERE id = @id;

-- name: UpdateDownloadJobError :exec
UPDATE download_jobs SET error = ? WHERE id = ?;

-- name: UpdateDownloadJobOutputPath :exec
UPDATE download_jobs SET output_path = ? WHERE id = ?;

-- name: UpdateDownloadJobLibraryTrackID :exec
UPDATE download_jobs SET library_track_id = ? WHERE id = ?;

-- name: UpdateDownloadJobCoverArtID :exec
UPDATE download_jobs SET cover_art_id = ? WHERE id = ?;

-- name: UpdateDownloadJobRequestJson :exec
UPDATE download_jobs SET request_json = ? WHERE id = ?;

-- name: IncrementDownloadJobAttempts :exec
UPDATE download_jobs SET attempts = attempts + 1 WHERE id = ?;

-- name: DeleteDownloadJob :execrows
-- Only a finished job is removed: one retried in the meantime is active again.
DELETE FROM download_jobs WHERE id = ? AND status IN ('completed', 'failed', 'canceled') AND completion_pending = 0;

-- name: DeleteFinishedDownloadJobs :many
DELETE FROM download_jobs
WHERE status IN ('completed', 'failed', 'canceled')
RETURNING id;

-- name: ClearMatchedDownloadJobLibraryRefs :exec
UPDATE download_jobs SET library_track_id = '', cover_art_id = '' WHERE library_track_id != '';

-- name: UpdateDownloadJobCanonicalID :exec
UPDATE download_jobs SET canonical_id = @canonical_id WHERE id = @id;

-- name: RepointDownloadJobs :exec
UPDATE download_jobs SET canonical_id = @canonical_id WHERE canonical_id = @canonical_id_2;

-- name: UpdateDownloadJob :execrows
UPDATE download_jobs SET
    status = @status,
    progress = @progress,
    error = @error,
    output_path = @output_path,
    library_track_id = @library_track_id,
    cover_art_id = @cover_art_id,
    completion_pending = @completion_pending,
    attempts = @attempts,
    downloader_name = @downloader_name,
    downloader_ref = @downloader_ref,
    started_at = @started_at,
    finished_at = @finished_at
WHERE id = @id;

-- name: UpdateDownloadJobProgress :execrows
-- A progress sample applies only to the attempt that produced it, while that
-- attempt is still running.
UPDATE download_jobs SET progress = @progress
WHERE id = @id AND status = 'running' AND completion_pending = 0 AND attempts = @attempts;

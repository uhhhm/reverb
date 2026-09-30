-- +goose Up
ALTER TABLE download_jobs ADD COLUMN completion_pending INTEGER NOT NULL DEFAULT 0;
-- Only the legacy active completion gate owns an output. A failed job's message
-- must never turn it into a completed-output recovery candidate.
UPDATE download_jobs SET completion_pending = 1
WHERE status = 'running' AND substr(error, 1, 27) = 'record completed download: ';

-- +goose Down
ALTER TABLE download_jobs DROP COLUMN completion_pending;

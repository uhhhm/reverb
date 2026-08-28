-- +goose Up
-- Backfill HLC and seq for legacy sync_change rows (pre-0029) which were created
-- with hlc=0, seq=0. Without backfill, ListSinceVector treats every legacy row as
-- unseen (ch.Seq==0) and resends the full history on every anti-entropy round.
-- This migration sets hlc from updated_at and assigns per-device seq via ROW_NUMBER
-- ordered by revision. It also seeds sync_vector so fresh peers have a vector.

-- Backfill hlc from wall time where 0 (legacy rows)
UPDATE sync_change SET hlc = updated_at WHERE hlc = 0 AND updated_at != 0;

-- Backfill seq per device for legacy rows only. Only rows with seq=0 are legacy;
-- re-numbering all rows would resequence already-correct post-0029 seq values and
-- invalidate sync vectors already synced to peers. Compute ROW_NUMBER over all
-- revisions per device, but only apply to legacy rows so existing seq values keep
-- their correct positions (ROW_NUMBER gives same seq for already-correct rows).
UPDATE sync_change SET seq = (
  SELECT rn FROM (
    SELECT revision, ROW_NUMBER() OVER (PARTITION BY device_id ORDER BY revision) AS rn
    FROM sync_change
  ) AS sub WHERE sub.revision = sync_change.revision
) WHERE seq = 0;

-- Seed sync_vector from the (now backfilled) sync_change max per device.
-- Use INSERT OR REPLACE to ensure vector exists; existing larger values are
-- preserved via MAX in the SELECT (since we just backfilled, max is correct).
INSERT OR REPLACE INTO sync_vector (device_id, seq, hlc, updated_at)
SELECT device_id, COALESCE(MAX(seq), 0), COALESCE(MAX(hlc), 0), unixepoch()
FROM sync_change
GROUP BY device_id;

-- +goose Down
-- No down: keep backfilled values. Vector is derived, and seq/hlc are not reverted.
SELECT 1;

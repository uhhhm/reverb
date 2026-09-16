-- +goose Up
CREATE TABLE sync_projection_pending (
    revision INTEGER PRIMARY KEY REFERENCES sync_change(revision) ON DELETE CASCADE
);
-- Repair projections lost by an interrupted older build. Replay only winners.
INSERT INTO sync_projection_pending(revision)
SELECT MAX(c.revision) FROM sync_change c
WHERE c.revision NOT IN (SELECT revision FROM sync_nonwinning)
GROUP BY c.entity_type, c.entity_id, c.field;

-- +goose Down
DROP TABLE sync_projection_pending;

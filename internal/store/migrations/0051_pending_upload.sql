-- +goose Up
-- The Downloads made on this device that no paired device has confirmed it
-- holds. A phone keeps each one until a peer's file manifest lists its content,
-- then removes it unless an offline playlist names it (ADR 0003); until then it
-- is never pruned, whatever the offline set or the free space. Local-only.
CREATE TABLE IF NOT EXISTS pending_upload (
  rel_path      TEXT PRIMARY KEY,
  downloaded_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS pending_upload;

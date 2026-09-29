-- +goose Up
-- The plays that count as listens: taste, stats and co-occurrence read this, not
-- plays, so no reader can forget to leave out an attempt that never qualified.
-- Only recommendation outcome stats read every play.
CREATE VIEW IF NOT EXISTS qualified_plays AS
SELECT id, user_id, catalog_id, played_at, ms_played, completed, created_at, origin, session_id
FROM plays
WHERE qualified = 1;

-- +goose Down
DROP VIEW IF EXISTS qualified_plays;

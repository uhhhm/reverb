-- +goose Up

-- A catalog id is minted locally from a random token, so two devices that have
-- never met mint different ids for the same track. Every catalog-id-keyed
-- change that crosses the wire therefore arrives naming an id this device has
-- never seen. The self-alias makes that id resolvable: it maps a catalog id to
-- the entity it names, and because a merge repoints aliases, it keeps
-- resolving after two entities are fused — which is exactly what happens when
-- a peer's entity turns out to be a track we already had.
INSERT OR IGNORE INTO catalog_alias (alias_kind, alias_value, catalog_id, created_at)
SELECT 'catalog', id, id, created_at FROM catalog_entity;

-- Quality overrides and measured loudness were keyed only on the backend track
-- id, which is local to one library backend. catalog_id is the identity peers
-- can agree on, mirroring what track_override and track_crop already do.
ALTER TABLE track_quality_override ADD COLUMN catalog_id TEXT;
CREATE INDEX IF NOT EXISTS idx_track_quality_override_catalog ON track_quality_override(catalog_id);
ALTER TABLE track_loudness ADD COLUMN catalog_id TEXT;
CREATE INDEX IF NOT EXISTS idx_track_loudness_catalog ON track_loudness(catalog_id);

-- +goose Down
DELETE FROM catalog_alias WHERE alias_kind = 'catalog';
DROP INDEX IF EXISTS idx_track_quality_override_catalog;
DROP INDEX IF EXISTS idx_track_loudness_catalog;

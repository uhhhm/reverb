-- +goose Up
-- Album is the third name a track can carry, and an album rename cascades into
-- the tracks under it, so it lives beside title and artist.
ALTER TABLE track_override ADD COLUMN album TEXT NOT NULL DEFAULT '';

-- User-supplied display names for albums and artists. Backend ids are local to
-- one library backend, so entity_key carries a stable identity peers agree on:
-- the normalised "artist\x1falbum" for an album, the normalised name for an
-- artist. Renames replicate on entity_key and bind to whatever backend id this
-- device currently has.
CREATE TABLE entity_override (
  entity_type TEXT NOT NULL,
  entity_id   TEXT NOT NULL,
  entity_key  TEXT NOT NULL DEFAULT '',
  name        TEXT NOT NULL DEFAULT '',
  updated_at  INTEGER NOT NULL,
  PRIMARY KEY (entity_type, entity_id)
);
CREATE INDEX idx_entity_override_key ON entity_override(entity_type, entity_key);

-- User-uploaded cover art for albums and tracks. The bytes live on disk under
-- dataDir/entity-covers keyed by sha256, so the same image applied to fifty
-- albums is stored once; this table only records which entity points at it.
CREATE TABLE entity_cover (
  entity_type TEXT NOT NULL,
  entity_id   TEXT NOT NULL,
  entity_key  TEXT NOT NULL DEFAULT '',
  sha256      TEXT NOT NULL,
  ext         TEXT NOT NULL,
  updated_at  INTEGER NOT NULL,
  PRIMARY KEY (entity_type, entity_id)
);
CREATE INDEX idx_entity_cover_key ON entity_cover(entity_type, entity_key);
CREATE INDEX idx_entity_cover_sha ON entity_cover(sha256);

-- +goose Down
DROP TABLE entity_cover;
DROP TABLE entity_override;
ALTER TABLE track_override DROP COLUMN album;

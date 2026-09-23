-- name: InsertCatalogEntity :exec
INSERT INTO catalog_entity (id, kind, title, artist, album, duration_ms, isrc, mbid, source, external_id, created_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?);

-- name: GetCatalogEntity :one
SELECT * FROM catalog_entity WHERE id = ?;

-- name: ListBrowsableCatalogTracks :many
SELECT * FROM catalog_entity
WHERE kind = 'track'
  -- The latest libraryPresent decides membership. Without one, a library-minted
  -- identity (source '') counts as present and a search-minted one does not.
  AND COALESCE((
    SELECT m.value_json FROM sync_change m WHERE m.entity_type = 'track' AND m.entity_id = catalog_entity.id
      AND m.field = 'libraryPresent' AND m.revision NOT IN (SELECT revision FROM sync_nonwinning)
    ORDER BY m.revision DESC LIMIT 1
  ), CASE WHEN source = '' THEN 'true' ELSE 'false' END) = 'true'
  AND NOT EXISTS (
    SELECT 1 FROM sync_change d WHERE d.entity_type = 'track' AND d.entity_id = catalog_entity.id
      AND d.field = '__deleted' AND d.revision NOT IN (SELECT revision FROM sync_nonwinning)
  )
  -- instr matches @query literally (no LIKE wildcards). A rename matches too,
  -- because the phone lists and filters by the renamed names.
  AND (CAST(@query AS TEXT) = ''
    OR instr(lower(title), lower(@query)) > 0
    OR instr(lower(artist), lower(@query)) > 0
    OR instr(lower(album), lower(@query)) > 0
    OR EXISTS (
      SELECT 1 FROM track_override o WHERE o.catalog_id = catalog_entity.id
        AND (instr(lower(o.title), lower(@query)) > 0
          OR instr(lower(o.artist), lower(@query)) > 0
          OR instr(lower(o.album), lower(@query)) > 0)
    ))
ORDER BY artist COLLATE NOCASE, album COLLATE NOCASE, title COLLATE NOCASE, id
LIMIT @limit OFFSET @offset;

-- name: InsertCatalogAlias :exec
INSERT INTO catalog_alias (alias_kind, alias_value, catalog_id, created_at)
VALUES (?,?,?,?) ON CONFLICT(alias_kind, alias_value) DO NOTHING;

-- name: GetAliasCatalogID :one
SELECT catalog_id FROM catalog_alias WHERE alias_kind = ? AND alias_value = ?;

-- name: ListAliasesForCatalog :many
SELECT alias_kind, alias_value FROM catalog_alias WHERE catalog_id = ?;

-- name: RepointAliases :exec
UPDATE catalog_alias SET catalog_id = ? WHERE catalog_id = ?;

-- name: DeleteCatalogEntity :exec
DELETE FROM catalog_entity WHERE id = ?;

-- name: GetBackendBinding :one
SELECT * FROM backend_binding WHERE catalog_id = ? AND library_identity = ?;

-- name: UpsertBackendBinding :exec
INSERT INTO backend_binding (catalog_id, library_identity, backend_id, cover_art_id, known_absent, binding_epoch, resolved_at)
VALUES (?,?,?,?,?,?,?)
ON CONFLICT(catalog_id, library_identity) DO UPDATE SET
  backend_id=excluded.backend_id, cover_art_id=excluded.cover_art_id,
  known_absent=excluded.known_absent, binding_epoch=excluded.binding_epoch, resolved_at=excluded.resolved_at;

-- name: DeleteBindingsForCatalog :exec
DELETE FROM backend_binding WHERE catalog_id = ?;

-- name: RepointBindings :exec
UPDATE backend_binding SET catalog_id = ? WHERE catalog_id = ?;

-- name: GetCatalogIDByBackendID :one
SELECT catalog_id FROM backend_binding WHERE backend_id = ? LIMIT 1;

-- name: ListCatalogIDsByBackendIDs :many
SELECT backend_id, catalog_id FROM backend_binding WHERE backend_id IN (sqlc.slice('backend_ids'));

-- name: ListTrackIdentitiesByBackendIDs :many
SELECT b.backend_id, c.isrc, c.mbid
FROM backend_binding b
JOIN catalog_entity c ON c.id = b.catalog_id
WHERE b.backend_id IN (sqlc.slice('backend_ids')) AND c.kind = 'track';

-- name: GetBackendIDByCatalogID :one
SELECT backend_id FROM backend_binding WHERE catalog_id = ? LIMIT 1;

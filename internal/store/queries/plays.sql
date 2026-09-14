-- name: DistinctDurableCanonicalIDs :many
SELECT DISTINCT catalog_id FROM plays WHERE catalog_id != ''
UNION
SELECT DISTINCT canonical_id FROM download_jobs WHERE canonical_id != ''
LIMIT ?;

-- name: InsertPlay :exec
INSERT INTO plays (id, user_id, catalog_id, played_at, ms_played, completed, created_at, origin, session_id, qualified)
VALUES (?,?,?,?,?,?,?,?,?,?);

-- name: RepointPlays :exec
UPDATE plays SET catalog_id = ? WHERE catalog_id = ?;

-- name: DeletePlay :exec
DELETE FROM plays WHERE id = ? AND user_id = ?;

-- name: ListRecentPlays :many
SELECT p.id, p.catalog_id, p.played_at, e.title, e.artist, e.album
FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id
WHERE p.user_id = ? AND p.played_at < ?
  AND p.qualified = 1
ORDER BY p.played_at DESC LIMIT ?;

-- name: ListPlayedSince :many
SELECT DISTINCT e.title, e.artist
FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id
WHERE p.played_at >= ? AND p.qualified = 1;

-- name: StatsSummary :one
SELECT
    COUNT(*)                    AS plays,
    COUNT(DISTINCT p.catalog_id) AS distinct_tracks,
    COUNT(DISTINCT e.artist)    AS distinct_artists,
    COUNT(DISTINCT e.album)     AS distinct_albums,
    COALESCE(SUM(p.ms_played), 0) AS ms_played
FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id
WHERE p.user_id = ? AND p.played_at >= ? AND p.played_at < ? AND p.qualified = 1;

-- name: StatsTopTracks :many
SELECT
    p.catalog_id,
    e.title,
    e.artist,
    e.album,
    e.source,
    e.external_id,
    COUNT(*)          AS plays,
    SUM(p.ms_played)  AS ms_played
FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id
WHERE p.user_id = ? AND p.played_at >= ? AND p.played_at < ? AND p.qualified = 1
GROUP BY p.catalog_id
ORDER BY COUNT(*) DESC, SUM(p.ms_played) DESC
LIMIT ?;

-- name: StatsTopArtists :many
WITH aggregated AS (
    SELECT
        MIN(p.catalog_id) AS catalog_id,
        e.artist,
        COUNT(*)         AS plays,
        SUM(p.ms_played) AS ms_played
    FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id
    WHERE p.user_id = ? AND p.played_at >= ? AND p.played_at < ? AND p.qualified = 1
    GROUP BY e.artist
)
SELECT
    CAST(a.catalog_id AS TEXT) AS catalog_id,
    e.title,
    a.artist,
    e.album,
    CAST(e.source AS TEXT) AS source,
    CAST(e.external_id AS TEXT) AS external_id,
    a.plays,
    a.ms_played
FROM aggregated a JOIN catalog_entity e ON e.id = a.catalog_id
ORDER BY a.plays DESC, a.ms_played DESC
LIMIT ?;

-- name: StatsTopAlbums :many
WITH aggregated AS (
    SELECT
        MIN(p.catalog_id) AS catalog_id,
        e.album,
        e.artist,
        COUNT(*)         AS plays,
        SUM(p.ms_played) AS ms_played
    FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id
    WHERE p.user_id = ? AND p.played_at >= ? AND p.played_at < ? AND p.qualified = 1
    GROUP BY e.album, e.artist
)
SELECT
    CAST(a.catalog_id AS TEXT) AS catalog_id,
    e.title,
    a.album,
    a.artist,
    CAST(e.source AS TEXT) AS source,
    CAST(e.external_id AS TEXT) AS external_id,
    a.plays,
    a.ms_played
FROM aggregated a JOIN catalog_entity e ON e.id = a.catalog_id
ORDER BY a.plays DESC, a.ms_played DESC
LIMIT ?;

-- name: StatsPlaysInWindow :many
SELECT p.played_at, p.ms_played
FROM plays p
WHERE p.user_id = ? AND p.played_at >= ? AND p.played_at < ? AND p.qualified = 1
ORDER BY p.played_at ASC;

-- name: StatsEntityArtist :one
SELECT
    COUNT(*)         AS plays,
    COALESCE(SUM(p.ms_played), 0) AS ms_played,
    MIN(p.played_at) AS first_played,
    MAX(p.played_at) AS last_played
FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id
WHERE p.user_id = ? AND e.artist = ? AND p.played_at >= ? AND p.played_at < ? AND p.qualified = 1;

-- name: StatsEntityAlbum :one
SELECT
    COUNT(*)         AS plays,
    COALESCE(SUM(p.ms_played), 0) AS ms_played,
    MIN(p.played_at) AS first_played,
    MAX(p.played_at) AS last_played
FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id
WHERE p.user_id = ? AND e.album = ? AND e.artist = ? AND p.played_at >= ? AND p.played_at < ? AND p.qualified = 1;

-- name: StatsEntityTrack :one
SELECT
    COUNT(*)         AS plays,
    COALESCE(SUM(p.ms_played), 0) AS ms_played,
    MIN(p.played_at) AS first_played,
    MAX(p.played_at) AS last_played
FROM plays p
WHERE p.user_id = ? AND p.catalog_id = ? AND p.played_at >= ? AND p.played_at < ? AND p.qualified = 1;

-- name: StatsTopTracksByArtist :many
SELECT
    p.catalog_id,
    e.title,
    e.artist,
    e.album,
    COUNT(*)          AS plays,
    SUM(p.ms_played)  AS ms_played
FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id
WHERE p.user_id = ? AND e.artist = ? AND p.played_at >= ? AND p.played_at < ? AND p.qualified = 1
GROUP BY p.catalog_id
ORDER BY COUNT(*) DESC, SUM(p.ms_played) DESC
LIMIT ?;

-- name: StatsTopTracksByAlbum :many
SELECT
    p.catalog_id,
    e.title,
    e.artist,
    e.album,
    COUNT(*)          AS plays,
    SUM(p.ms_played)  AS ms_played
FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id
WHERE p.user_id = ? AND e.album = ? AND e.artist = ? AND p.played_at >= ? AND p.played_at < ? AND p.qualified = 1
GROUP BY p.catalog_id
ORDER BY COUNT(*) DESC, SUM(p.ms_played) DESC
LIMIT ?;

-- name: StatsTopTracksByCatalogID :many
SELECT
    p.catalog_id,
    e.title,
    e.artist,
    e.album,
    COUNT(*)          AS plays,
    SUM(p.ms_played)  AS ms_played
FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id
WHERE p.user_id = ? AND p.catalog_id = ? AND p.played_at >= ? AND p.played_at < ? AND p.qualified = 1
GROUP BY p.catalog_id
ORDER BY COUNT(*) DESC, SUM(p.ms_played) DESC
LIMIT ?;

-- name: CountPlays :one
SELECT COUNT(*) FROM plays WHERE qualified = 1;

-- name: CountPlaysByCatalog :one
SELECT COUNT(*) FROM plays WHERE user_id = ? AND catalog_id = ? AND qualified = 1;

-- name: InsertPlayIfAbsent :exec
INSERT OR IGNORE INTO plays (id, user_id, catalog_id, played_at, ms_played, completed, created_at, origin, session_id, qualified)
VALUES (?,?,?,?,?,?,?,?,?,?);

-- name: GetPlay :one
SELECT * FROM plays WHERE id = ?;

-- name: ListAllPlays :many
SELECT * FROM plays ORDER BY played_at ASC;

-- name: RecommendationStats :many
WITH origins AS (
  SELECT p.origin FROM plays p
  WHERE p.user_id = sqlc.arg(user_id) AND p.origin != '' AND p.played_at >= sqlc.arg(from_time) AND p.played_at < sqlc.arg(to_time)
  UNION
  SELECT a.origin FROM recommendation_add a
  WHERE a.user_id = sqlc.arg(user_id) AND a.created_at >= sqlc.arg(from_time) AND a.created_at < sqlc.arg(to_time)
), play_totals AS (
  SELECT p.origin, COUNT(*) AS plays,
         SUM(CASE WHEN p.qualified = 0 THEN 1 ELSE 0 END) AS skips,
         SUM(CASE WHEN p.completed = 1 THEN 1 ELSE 0 END) AS completions
  FROM plays p
  WHERE p.user_id = sqlc.arg(user_id) AND p.origin != '' AND p.played_at >= sqlc.arg(from_time) AND p.played_at < sqlc.arg(to_time)
  GROUP BY p.origin
), add_totals AS (
  SELECT a.origin, COUNT(*) AS additions
  FROM recommendation_add a
  WHERE a.user_id = sqlc.arg(user_id) AND a.created_at >= sqlc.arg(from_time) AND a.created_at < sqlc.arg(to_time)
  GROUP BY a.origin
)
SELECT origins.origin,
       COALESCE(play_totals.plays, 0) AS plays,
       COALESCE(play_totals.skips, 0) AS skips,
       COALESCE(play_totals.completions, 0) AS completions,
       COALESCE(add_totals.additions, 0) AS additions
FROM origins
LEFT JOIN play_totals USING (origin)
LEFT JOIN add_totals USING (origin)
ORDER BY origins.origin;

-- name: InsertRecommendationAddIfAbsent :exec
INSERT OR IGNORE INTO recommendation_add (id, user_id, origin, action, created_at)
VALUES (?, ?, ?, ?, ?);

-- name: ListAllRecommendationAdds :many
SELECT * FROM recommendation_add ORDER BY created_at, id;

-- name: LocalRecommendationTracks :many
WITH seed AS (
  SELECT id, artist, album
  FROM catalog_entity
  WHERE kind = 'track' AND lower(trim(artist)) = lower(trim(sqlc.arg(seed_artist)))
    AND (CAST(sqlc.arg(seed_title) AS TEXT) = '' OR lower(trim(title)) = lower(trim(CAST(sqlc.arg(seed_title) AS TEXT))))
  LIMIT 1
), candidates AS (
  SELECT e.id, e.title, e.artist, e.album, e.duration_ms, e.isrc, e.mbid,
         b.backend_id, b.cover_art_id,
         CASE WHEN lower(e.artist) = lower(seed.artist) THEN 3 ELSE 0 END +
         CASE WHEN seed.album != '' AND lower(e.album) = lower(seed.album) THEN 2 ELSE 0 END +
         4 * EXISTS (
           SELECT 1 FROM synced_playlists sp
           WHERE EXISTS (SELECT 1 FROM json_each(sp.tracks_json) jt
                         WHERE lower(json_extract(jt.value, '$.artist')) = lower(seed.artist)
                           AND (CAST(sqlc.arg(seed_title) AS TEXT) = '' OR lower(json_extract(jt.value, '$.title')) = lower(CAST(sqlc.arg(seed_title) AS TEXT))))
             AND EXISTS (SELECT 1 FROM json_each(sp.tracks_json) jt
                         WHERE lower(json_extract(jt.value, '$.artist')) = lower(e.artist)
                           AND lower(json_extract(jt.value, '$.title')) = lower(e.title))
         ) +
         4 * EXISTS (
           SELECT 1 FROM plays ps JOIN plays pc ON pc.session_id = ps.session_id
           WHERE ps.catalog_id = seed.id AND pc.catalog_id = e.id AND ps.session_id != ''
         ) AS score
  FROM catalog_entity e
  JOIN backend_binding b ON b.catalog_id = e.id AND b.backend_id != '' AND b.known_absent = 0
  JOIN seed
  WHERE e.kind = 'track' AND (CAST(sqlc.arg(seed_title) AS TEXT) = '' OR e.id != seed.id)
)
SELECT * FROM candidates WHERE score > 0
GROUP BY id ORDER BY score DESC, lower(artist), lower(title) LIMIT sqlc.arg(result_limit);

-- name: LocalRecommendationArtists :many
WITH candidate_artists AS (
  SELECT e.artist,
         4 * EXISTS (
           SELECT 1 FROM synced_playlists sp
           WHERE EXISTS (SELECT 1 FROM json_each(sp.tracks_json) jt
                         WHERE lower(json_extract(jt.value, '$.artist')) = lower(sqlc.arg(seed_artist)))
             AND EXISTS (SELECT 1 FROM json_each(sp.tracks_json) jt
                         WHERE lower(json_extract(jt.value, '$.artist')) = lower(e.artist))
         ) +
         4 * EXISTS (
           SELECT 1
           FROM plays ps
           JOIN catalog_entity se ON se.id = ps.catalog_id
           JOIN plays pc ON pc.session_id = ps.session_id AND pc.session_id != ''
           JOIN catalog_entity ce ON ce.id = pc.catalog_id
           WHERE lower(se.artist) = lower(sqlc.arg(seed_artist)) AND lower(ce.artist) = lower(e.artist)
         ) AS score
  FROM catalog_entity e
  JOIN backend_binding b ON b.catalog_id = e.id AND b.backend_id != '' AND b.known_absent = 0
  WHERE e.kind = 'track' AND lower(e.artist) != lower(sqlc.arg(seed_artist))
  GROUP BY e.artist
)
SELECT artist, score FROM candidate_artists WHERE score > 0
ORDER BY score DESC, lower(artist) LIMIT sqlc.arg(result_limit);

-- name: TopPlayedTracksBetween :many
SELECT CAST(MIN(e.title) AS TEXT) AS title, CAST(MIN(e.artist) AS TEXT) AS artist, COUNT(*) AS plays
FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id
WHERE p.played_at >= sqlc.arg(since) AND p.played_at < sqlc.arg(until) AND p.qualified = 1
  AND e.title != '' AND e.artist != ''
GROUP BY lower(e.artist), lower(e.title)
ORDER BY plays DESC, lower(MIN(e.artist)), lower(MIN(e.title))
LIMIT sqlc.arg(result_limit);

-- name: TopPlayedArtistsBetween :many
SELECT CAST(MIN(e.artist) AS TEXT) AS artist, COUNT(*) AS plays
FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id
WHERE p.played_at >= sqlc.arg(since) AND p.played_at < sqlc.arg(until) AND p.qualified = 1
  AND e.artist != ''
GROUP BY lower(e.artist)
ORDER BY plays DESC, lower(MIN(e.artist))
LIMIT sqlc.arg(result_limit);

-- name: LibraryArtistTrackCounts :many
SELECT CAST(MIN(e.artist) AS TEXT) AS artist, COUNT(DISTINCT e.id) AS tracks
FROM catalog_entity e
JOIN backend_binding b ON b.catalog_id = e.id AND b.backend_id != '' AND b.known_absent = 0
WHERE e.kind = 'track' AND e.artist != ''
GROUP BY lower(e.artist)
ORDER BY tracks DESC, lower(MIN(e.artist))
LIMIT sqlc.arg(result_limit);

-- name: LibraryTracksByArtist :many
SELECT e.id, e.title, e.artist, e.album, e.duration_ms, e.isrc, e.mbid,
       CAST(MIN(b.backend_id) AS TEXT) AS backend_id, CAST(MIN(b.cover_art_id) AS TEXT) AS cover_art_id
FROM catalog_entity e
JOIN backend_binding b ON b.catalog_id = e.id AND b.backend_id != '' AND b.known_absent = 0
WHERE e.kind = 'track' AND lower(e.artist) = lower(sqlc.arg(artist))
GROUP BY e.id
ORDER BY lower(e.album), lower(e.title), e.id
LIMIT sqlc.arg(result_limit);

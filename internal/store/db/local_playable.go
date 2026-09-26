package db

import (
	"context"
	"encoding/json"
	"strings"
)

// PlayableTrack is a track a library can play that no binding names: a
// phone's offline copy of a peer's file.
type PlayableTrack struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	DurationMs int64  `json:"durationMs"`
	CoverArtID string `json:"coverArtId"`
}

type LocalRecommendationPlayableTracksParams struct {
	Candidates  []PlayableTrack
	SeedArtist  string
	SeedTitle   string
	ResultLimit int64
}

type LocalRecommendationPlayableTracksRow struct {
	BackendID  string
	Title      string
	Artist     string
	Album      string
	DurationMs int64
	CoverArtID string
	// CatalogID is the household catalogue's entity for the same artist and
	// title, or "" when the catalogue has none.
	CatalogID string
	Score     int64
}

// localRecommendationPlayableTracks scores candidates on the signals of
// LocalRecommendationTracks: shared artist and album, managed-playlist
// co-occurrence, and playback-session co-occurrence. Plays name catalog
// entities, so session co-occurrence and the catalog id match on normalised
// artist and title. The seed's album comes from the candidates, else from the
// catalogue. sqlc cannot analyse a CTE that selects from json_each, so this is
// written by hand. ?1 candidates JSON, ?2 seed artist, ?3 seed title ("" for
// an artist seed), ?4 limit.
const localRecommendationPlayableTracks = `
WITH playable AS (
  SELECT CAST(json_extract(c.value, '$.id') AS TEXT) AS backend_id,
         CAST(ifnull(json_extract(c.value, '$.title'), '') AS TEXT) AS title,
         CAST(ifnull(json_extract(c.value, '$.artist'), '') AS TEXT) AS artist,
         CAST(ifnull(json_extract(c.value, '$.album'), '') AS TEXT) AS album,
         CAST(ifnull(json_extract(c.value, '$.durationMs'), 0) AS INTEGER) AS duration_ms,
         CAST(ifnull(json_extract(c.value, '$.coverArtId'), '') AS TEXT) AS cover_art_id
  FROM json_each(?1) c
  WHERE ifnull(json_extract(c.value, '$.id'), '') != ''
), seed AS (
  SELECT artist, album FROM (
    SELECT 0 AS preference, artist, album FROM playable
    WHERE lower(trim(artist)) = lower(?2) AND (?3 = '' OR lower(trim(title)) = lower(?3))
    UNION ALL
    SELECT 1, artist, album FROM catalog_entity
    WHERE kind = 'track' AND lower(trim(artist)) = lower(?2) AND (?3 = '' OR lower(trim(title)) = lower(?3))
    UNION ALL
    SELECT 2, ?2, ''
  ) ORDER BY preference LIMIT 1
), candidates AS (
  SELECT p.backend_id, p.title, p.artist, p.album, p.duration_ms, p.cover_art_id,
         ifnull((SELECT min(e.id) FROM catalog_entity e
                 WHERE e.kind = 'track' AND lower(trim(e.artist)) = lower(trim(p.artist))
                   AND lower(trim(e.title)) = lower(trim(p.title))), '') AS catalog_id,
         CASE WHEN lower(trim(p.artist)) = lower(trim(seed.artist)) THEN 3 ELSE 0 END +
         CASE WHEN seed.album != '' AND lower(trim(p.album)) = lower(trim(seed.album)) THEN 2 ELSE 0 END +
         4 * EXISTS (
           SELECT 1 FROM synced_playlists sp
           WHERE EXISTS (SELECT 1 FROM json_each(sp.tracks_json) jt
                         WHERE lower(trim(json_extract(jt.value, '$.artist'))) = lower(?2)
                           AND (?3 = '' OR lower(trim(json_extract(jt.value, '$.title'))) = lower(?3)))
             AND EXISTS (SELECT 1 FROM json_each(sp.tracks_json) jt
                         WHERE lower(trim(json_extract(jt.value, '$.artist'))) = lower(trim(p.artist))
                           AND lower(trim(json_extract(jt.value, '$.title'))) = lower(trim(p.title)))
         ) +
         4 * EXISTS (
           SELECT 1
           FROM plays ps
           JOIN catalog_entity se ON se.id = ps.catalog_id
           JOIN plays pc ON pc.session_id = ps.session_id AND pc.session_id != '' AND pc.id != ps.id
           JOIN catalog_entity ce ON ce.id = pc.catalog_id
           WHERE lower(trim(se.artist)) = lower(?2) AND (?3 = '' OR lower(trim(se.title)) = lower(?3))
             AND lower(trim(ce.artist)) = lower(trim(p.artist)) AND lower(trim(ce.title)) = lower(trim(p.title))
         ) AS score
  FROM playable p
  JOIN seed
  WHERE ?3 = '' OR lower(trim(p.artist)) != lower(?2) OR lower(trim(p.title)) != lower(?3)
)
SELECT backend_id, title, artist, album, duration_ms, cover_art_id, catalog_id, score
FROM candidates WHERE score > 0
GROUP BY backend_id ORDER BY score DESC, lower(artist), lower(title) LIMIT ?4`

// LocalRecommendationPlayableTracks is LocalRecommendationTracks for a
// library whose tracks carry no binding.
func (q *Queries) LocalRecommendationPlayableTracks(ctx context.Context, arg LocalRecommendationPlayableTracksParams) ([]LocalRecommendationPlayableTracksRow, error) {
	candidates := arg.Candidates
	if candidates == nil {
		candidates = []PlayableTrack{}
	}
	blob, err := json.Marshal(candidates)
	if err != nil {
		return nil, err
	}
	rows, err := q.db.QueryContext(ctx, localRecommendationPlayableTracks,
		string(blob), strings.TrimSpace(arg.SeedArtist), strings.TrimSpace(arg.SeedTitle), arg.ResultLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []LocalRecommendationPlayableTracksRow
	for rows.Next() {
		var r LocalRecommendationPlayableTracksRow
		if err := rows.Scan(&r.BackendID, &r.Title, &r.Artist, &r.Album, &r.DurationMs, &r.CoverArtID, &r.CatalogID, &r.Score); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

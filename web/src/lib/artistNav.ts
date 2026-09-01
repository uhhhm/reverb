import type { QueryClient } from '@tanstack/react-query'
import { api } from './api'
import type { Artist } from './types'

/** The subset of a playing track that can address an artist page. */
export interface ArtistRef {
  artist?: string
  artistId?: string
  artistExternalId?: string
}

// Normalise a name for comparison: case, accents and surrounding punctuation
// vary between the search sources and the library backend.
function norm(name: string): string {
  return name
    .normalize('NFKD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, ' ')
    .trim()
}

// The primary artist, for credits like "A, B" or "A feat. B". Library backends
// key an artist row on the primary name, so a full-credit string never matches.
function primary(name: string): string {
  return name.split(/,|&|\bfeat\.?\b|\bft\.?\b|\bwith\b|\bx\b/i)[0] ?? name
}

/**
 * The artist route for a playing track, or undefined when nothing addresses one.
 *
 * Tracks synthesised from a download job, a recently-played row or a search
 * result carry the artist's NAME but no id, so the ids alone would leave the
 * artist unlinked for most of what actually plays. Those fall back to matching
 * the name against the library's artist list, which is already cached by the
 * library page and the rail.
 */
export async function artistPath(qc: QueryClient, track: ArtistRef): Promise<string | undefined> {
  if (track.artistExternalId) return `/artist/spotify/${track.artistExternalId}`
  if (track.artistId) return `/artist/library/${track.artistId}`
  if (!track.artist) return undefined

  const artists = await qc.fetchQuery({
    queryKey: ['library', 'artists'],
    queryFn: () => api.get<Artist[]>('/library/artists'),
  })
  const want = norm(track.artist)
  const wantPrimary = norm(primary(track.artist))
  const hit =
    artists.find((a) => norm(a.name) === want) ??
    artists.find((a) => norm(a.name) === wantPrimary)
  return hit ? `/artist/library/${hit.id}` : undefined
}

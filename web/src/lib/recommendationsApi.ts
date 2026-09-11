import { useQuery } from '@tanstack/react-query'
import { api } from './api'
import { externalTrackFromRef } from './externalTrack'
import type { components } from './generated/api'
import type { Track } from './types'

export type SimilarArtists = components['schemas']['SimilarArtists']
export type RecommendedTrack = components['schemas']['RecommendedTrack']
export type SimilarTracksResult = components['schemas']['SimilarTracks']
type RadioRequest = components['schemas']['RadioRequest']
type RadioSeed = components['schemas']['RadioSeed']

/** Similarity data moves slowly and the server caches it too. */
const STALE_MS = 60 * 60 * 1000

/**
 * Artists related to one on a search source or in the library. The server
 * never fails this lookup — a source error comes back as an empty list — so
 * callers only have to decide whether there is anything to show.
 */
export function useSimilarArtists(source: string, id: string) {
  return useQuery({
    queryKey: ['similar-artists', source, id],
    queryFn: () =>
      api.get<SimilarArtists>(`/recommendations/artists/${encodeURIComponent(source)}/${encodeURIComponent(id)}`),
    enabled: !!source && !!id,
    staleTime: STALE_MS,
  })
}

/** Playable tracks similar to a seed. available is false without Last.fm. */
export function useSimilarTracks(artist: string, title: string) {
  return useQuery({
    queryKey: ['similar-tracks', artist, title],
    queryFn: () =>
      api.get<SimilarTracksResult>(`/recommendations/similar-tracks?${new URLSearchParams({ artist, title })}`),
    enabled: !!artist && !!title,
    staleTime: STALE_MS,
  })
}

/** The next Radio tracks for some seeds, ready to queue. */
export async function fetchRadio(seeds: RadioSeed[]): Promise<Track[]> {
  const res = await api.post<SimilarTracksResult>('/recommendations/radio', { seeds } satisfies RadioRequest)
  return res.tracks.map(recommendedTrackToTrack)
}

/**
 * The Track a recommendation plays as. An owned recommendation plays the
 * library copy by its backend id; anything else streams from its source.
 */
export function recommendedTrackToTrack(r: RecommendedTrack): Track {
  if (r.source === 'library') {
    return {
      id: r.externalId,
      title: r.title,
      albumId: r.match?.albumId ?? '',
      album: r.album,
      artistId: r.match?.artistId ?? '',
      artist: r.artist,
      coverArtId: r.coverArtId || r.match?.coverArtId || '',
      trackNumber: 0,
      discNumber: 0,
      durationMs: r.durationMs,
      bitRate: 0,
      suffix: '',
      contentType: '',
      ...(r.isrc ? { isrc: r.isrc } : {}),
    }
  }
  return externalTrackFromRef(
    { source: r.source, externalId: r.externalId, title: r.title, artist: r.artist, album: r.album, isrc: r.isrc, durationMs: r.durationMs },
    r.artistExternalId ? { artistExternalId: r.artistExternalId } : {},
  )
}

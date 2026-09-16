import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './api'
import { externalTrackFromRef } from './externalTrack'
import type { components } from './generated/api'
import type { RecommendationOrigin, RecommendationReason, SyncedPlaylistDetail, Track } from './types'

export type SimilarArtists = components['schemas']['SimilarArtists']
export type RecommendedTrack = components['schemas']['RecommendedTrack']
export type SimilarTracksResult = components['schemas']['SimilarTracks']
export type RecommendationSettings = components['schemas']['RecommendationSettings']
export type HomeShelves = components['schemas']['HomeShelves']
export type Shelf = components['schemas']['Shelf']
export type Mix = components['schemas']['Mix']
export type MixKind = components['schemas']['MixKind']
type MixList = components['schemas']['MixList']
type RecommendationSettingsPatch = components['schemas']['RecommendationSettingsPatch']
type RadioRequest = components['schemas']['RadioRequest']
type RadioSeed = components['schemas']['RadioSeed']

/** Similarity data moves slowly and the server caches it too. */
const STALE_MS = 60 * 60 * 1000
/**
 * While the server refreshes shelves or a Mix in the background, what it had
 * cached shows and is fetched again this often until the refresh lands.
 */
const REFRESH_POLL_MS = 3000
const SETTINGS_KEY = ['recommendation-settings']

/** Home's "For you" shelves, from the server's cache at once. */
export function useShelves() {
  return useQuery({
    queryKey: ['shelves'],
    queryFn: () => api.get<HomeShelves>('/recommendations/shelves'),
    refetchInterval: (query) => (query.state.data?.refreshing ? REFRESH_POLL_MS : false),
    placeholderData: keepPreviousData,
  })
}

/** Every Mix; an empty one is hidden by the caller. */
export function useMixes() {
  return useQuery({
    queryKey: ['mixes'],
    queryFn: () => api.get<MixList>('/recommendations/mixes'),
    refetchInterval: (query) => ((query.state.data?.mixes ?? []).some((m) => m.refreshing) ? REFRESH_POLL_MS : false),
    placeholderData: keepPreviousData,
  })
}

/** A Mix by kind. A null kind names no Mix, so nothing is fetched. */
export function useMix(kind: MixKind | null) {
  return useQuery({
    queryKey: ['mix', kind],
    queryFn: () => {
      if (kind === null) throw new Error('no Mix kind')
      return api.get<Mix>(`/recommendations/mixes/${encodeURIComponent(kind)}`)
    },
    refetchInterval: (query) => (query.state.data?.refreshing ? REFRESH_POLL_MS : false),
    enabled: kind !== null,
  })
}

/** Copies a Mix into a new managed playlist, which can then go offline. */
export function saveMixAsPlaylist(kind: MixKind, name?: string): Promise<SyncedPlaylistDetail> {
  return api.post<SyncedPlaylistDetail>(`/recommendations/mixes/${encodeURIComponent(kind)}/playlist`, name ? { name } : {})
}

/** Suggested songs for a managed playlist; page asks for the next best. */
export function usePlaylistSuggestions(playlistId: string, page: number, enabled: boolean) {
  return useQuery({
    queryKey: ['playlist-suggestions', playlistId, page],
    queryFn: () =>
      api.get<SimilarTracksResult>(`/recommendations/playlists/${encodeURIComponent(playlistId)}/suggestions?page=${page}`),
    enabled: enabled && !!playlistId,
    staleTime: STALE_MS,
    placeholderData: keepPreviousData,
  })
}

export function mixTitle(kind: MixKind): string {
  return kind === 'discoverWeekly' ? 'Discover Weekly' : 'Release Radar'
}

export function mixDescription(kind: MixKind): string {
  return kind === 'discoverWeekly'
    ? 'New music picked for you. Refreshes every Monday.'
    : 'New releases from artists you listen to. Refreshes every Friday.'
}

/** A shelf's title, which carries its reason. */
export function shelfTitle(shelf: Shelf): string {
  switch (shelf.kind) {
    case 'becauseYouPlayed':
      return `Because you played ${shelf.seed?.title ?? shelf.seed?.artist ?? ''}`
    case 'similarTo':
      return `Similar to ${shelf.seed?.title ?? shelf.seed?.artist ?? ''}`
    case 'artistsYouMightLike':
      return 'Artists you might like'
    case 'moreFromArtistsYouLove':
      return 'More from artists you love'
  }
}

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

/** Playable tracks similar to a seed. Sources are merged by the server. */
export function useSimilarTracks(artist: string, title: string, mbid?: string) {
  return useQuery({
    queryKey: ['similar-tracks', artist, title, mbid],
    queryFn: () =>
      api.get<SimilarTracksResult>(`/recommendations/similar-tracks?${new URLSearchParams({ artist, title, ...(mbid ? { mbid } : {}) })}`),
    enabled: !!artist && !!title,
    staleTime: STALE_MS,
  })
}

/** The next Radio tracks for some seeds, ready to queue. */
export async function fetchRadio(seeds: RadioSeed[]): Promise<Track[]> {
  const res = await api.post<SimilarTracksResult>('/recommendations/radio', { seeds } satisfies RadioRequest)
  return res.tracks.map((track) => recommendedTrackToTrack(track, 'radio'))
}

/** The short reason shown with a recommendation. */
export function reasonText(reason: RecommendationReason | undefined): string | undefined {
  switch (reason?.kind) {
    case 'played':
      return `Because you played ${reason.title}`
    case 'similar':
      return `Similar to ${reason.title}`
    case 'fansAlsoLike':
      return `Fans of ${reason.artist} also like`
    case 'radioArtist':
      return `Radio from ${reason.artist}`
    case 'moreFrom':
      return `More from ${reason.artist}`
    case 'newRelease':
      return reason.title ? `New from ${reason.artist} · ${reason.title}` : `New from ${reason.artist}`
    case 'personal':
      return 'Recommended for you on ListenBrainz'
    default:
      return undefined
  }
}

/** The household's Adventurousness and Online recommendations switch. */
export function useRecommendationSettings() {
  return useQuery({
    queryKey: SETTINGS_KEY,
    queryFn: () => api.get<RecommendationSettings>('/recommendations/settings'),
  })
}

/** Changes recommendation settings on every paired device. */
export function useUpdateRecommendationSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (patch: RecommendationSettingsPatch) => api.put<RecommendationSettings>('/recommendations/settings', patch),
    onSuccess: (settings) => {
      qc.setQueryData(SETTINGS_KEY, settings)
      // Either setting changes what every recommendation list holds.
      for (const queryKey of [['similar-artists'], ['similar-tracks']]) {
        void qc.invalidateQueries({ queryKey })
      }
    },
  })
}

/**
 * The Track a recommendation plays as. An owned recommendation plays the
 * library copy by its backend id; anything else streams from its source.
 */
export function recommendedTrackToTrack(r: RecommendedTrack, origin?: RecommendationOrigin): Track {
	const reason = r.reason ? { reason: r.reason } : {}
	const attribution = origin ? { recommendationOrigin: origin } : {}
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
      ...(r.mbid ? { mbid: r.mbid } : {}),
      ...reason,
			...attribution,
    }
  }
  return {
    ...externalTrackFromRef(
      { source: r.source, externalId: r.externalId, title: r.title, artist: r.artist, album: r.album, isrc: r.isrc, mbid: r.mbid, durationMs: r.durationMs },
      r.artistExternalId ? { artistExternalId: r.artistExternalId } : {},
    ),
    ...reason,
		...attribution,
  }
}

import type { QueryClient } from '@tanstack/react-query'
import type { components } from './generated/api'

type MixKind = components['schemas']['MixKind']

const results = {
  shelves: ['shelves'],
  mixes: ['mixes'],
  mix: ['mix'],
  playlistSuggestions: ['playlist-suggestions'],
  similarArtists: ['similar-artists'],
  similarTracks: ['similar-tracks'],
} as const

/** Query identities and mutation effects for every recommendation surface. */
export const recommendationQueries = {
  marks: ['not-interested'] as const,
  settings: ['recommendation-settings'] as const,
  shelves: results.shelves,
  mixes: results.mixes,
  mix: (kind: MixKind | null) => [...results.mix, kind] as const,
  playlistSuggestions: (playlistId: string, page?: number) =>
    page === undefined ? [...results.playlistSuggestions, playlistId] as const : [...results.playlistSuggestions, playlistId, page] as const,
  similarArtists: (source: string, id: string) => [...results.similarArtists, source, id] as const,
  similarTracks: (artist: string, title: string, mbid?: string) => [...results.similarTracks, artist, title, mbid] as const,
}

/** Refetch active results; expire every cached kind, seed, playlist and page. */
export function refreshRecommendations(client: QueryClient, effect: 'marks' | 'settings') {
  const keys = [...Object.values(results), ...(effect === 'marks' ? [recommendationQueries.marks] : [])]
  for (const queryKey of keys) void client.invalidateQueries({ queryKey })
}

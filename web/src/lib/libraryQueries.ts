import type { QueryClient } from '@tanstack/react-query'

const roots = {
  library: ['library'],
  artistDetail: ['artist-detail'],
  albumDetail: ['album-detail'],
  playlists: ['synced-playlists'],
  playlist: ['synced-playlist'],
} as const

/** Shared identities for library metadata readers, including managed playlists. */
export const libraryQueries = {
  status: ['library', 'status'] as const,
  search: (query: string) => [...roots.library, 'search', query] as const,
  artist: (id: string) => [...roots.library, 'artist', id] as const,
  album: (id: string) => [...roots.library, 'album', id] as const,
  artists: ['library', 'artists'] as const,
  albums: (type: string, size = 0) => [...roots.library, 'albums', type, size] as const,
  songs: (size = 0) => [...roots.library, 'songs', size] as const,
  artistDetail: (source: string, id: string) => [...roots.artistDetail, source, id] as const,
  albumDetail: (source: string, id: string) => [...roots.albumDetail, source, id] as const,
  playlists: roots.playlists,
  playlist: (id: string) => [...roots.playlist, id] as const,
}

/** Refresh active views and expire inactive variants after accepted library edits. */
export function refreshLibrary(client: QueryClient) {
  return Promise.all(Object.values(roots).map((queryKey) => client.invalidateQueries({ queryKey })))
}

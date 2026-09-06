export type PlaylistSort = 'custom' | 'title' | 'artist' | 'album' | 'duration'

export function playlistOrder<T extends { title: string; artist: string; album?: string; durationMs: number }>(tracks: T[], order: number[], query: string, sort: PlaylistSort): number[] {
  const needle = query.trim().toLocaleLowerCase()
  const filtered = order.filter((i) => `${tracks[i].title} ${tracks[i].artist} ${tracks[i].album ?? ''}`.toLocaleLowerCase().includes(needle))
  if (sort === 'custom') return filtered
  return filtered.sort((a, b) => sort === 'duration' ? tracks[a].durationMs - tracks[b].durationMs : (tracks[a][sort] ?? '').localeCompare(tracks[b][sort] ?? '', undefined, { sensitivity: 'base' }))
}

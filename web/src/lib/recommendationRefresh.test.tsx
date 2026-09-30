import { act, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './api'
import { useMarkNotInterested, useNotInterested, useUndoNotInterested } from './notInterestedApi'
import {
  useMix, useMixes, usePlaylistSuggestions, useRecommendationSettings, useShelves,
  useSimilarArtists, useSimilarTracks, useUpdateRecommendationSettings,
} from './recommendationsApi'
import { useToastStore } from './toastStore'

const mark = { key: 'track:seed', kind: 'track', artist: 'Seed Artist', title: 'Seed Song', markedAt: 1 }
const actions = ['mark track', 'mark artist', 'undo', 'adventurousness', 'online recommendations'] as const
type Action = typeof actions[number]

function useActions() {
  const markMutation = useMarkNotInterested()
  const undoMutation = useUndoNotInterested()
  const settingsMutation = useUpdateRecommendationSettings()
  return (action: Action) => {
    switch (action) {
      case 'mark track': return markMutation.mutateAsync({ kind: 'track', source: 'library', trackId: 'seed' })
      case 'mark artist': return markMutation.mutateAsync({ kind: 'artist', source: 'library', id: 'artist', name: 'Seed Artist' })
      case 'undo': return undoMutation.mutateAsync(mark.key)
      case 'adventurousness': return settingsMutation.mutateAsync({ adventurousness: 80 })
      case 'online recommendations': return settingsMutation.mutateAsync({ onlineRecommendations: false })
    }
  }
}

function useActiveViews() {
  return {
    shelves: useShelves(), mixes: useMixes(), mix: useMix('discoverWeekly'),
    suggestions: usePlaylistSuggestions('playlist-a', 0, true),
    artists: useSimilarArtists('library', 'artist'), tracks: useSimilarTracks('Seed Artist', 'Seed Song'),
    marks: useNotInterested(), settings: useRecommendationSettings(),
    library: useQuery({ queryKey: ['library', 'albums'], queryFn: () => api.get('/library/albums') }),
    downloads: useQuery({ queryKey: ['downloads'], queryFn: () => api.get('/downloads') }),
  }
}

function useInactiveViews() {
  return {
    mix: useMix('releaseRadar'),
    nextPage: usePlaylistSuggestions('playlist-a', 1, true),
    otherPlaylist: usePlaylistSuggestions('playlist-b', 0, true),
  }
}

function fixture(fail: boolean) {
  let revision = 'Before'
  let marks = [mark]
  let settings = { adventurousness: 50, onlineRecommendations: true }
  const requests: string[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
    const path = url.replace('/api/v1', '')
    requests.push(`${init.method} ${path}`)
    let body: unknown
    if (init.method !== 'GET') {
      if (fail) return new Response(JSON.stringify({ error: 'rejected' }), { status: 500 })
      revision = 'After'
      if (path === '/recommendations/settings') {
        settings = { ...settings, ...JSON.parse(init.body as string) }
        body = settings
      } else {
        marks = init.method === 'DELETE' ? [] : [{ ...mark, title: 'After' }]
        body = init.method === 'DELETE' ? null : marks[0]
      }
    } else {
      const track = { source: 'library', externalId: 'song', title: revision, artist: 'Artist', album: '', durationMs: 1000, type: 'track' }
      const mix = { kind: path.endsWith('releaseRadar') ? 'releaseRadar' : 'discoverWeekly', tracks: [track], period: '2026-09-28', available: true, refreshing: false }
      if (path === '/not-interested') body = { marks }
      else if (path === '/recommendations/settings') body = settings
      else if (path === '/recommendations/shelves') body = { shelves: [{ kind: 'similarTo', tracks: [track], artists: [] }], refreshing: false }
      else if (path === '/recommendations/mixes') body = { mixes: [mix] }
      else if (path.startsWith('/recommendations/mixes/')) body = mix
      else if (path.startsWith('/recommendations/artists/')) body = { available: true, artists: [{ source: 'library', externalId: 'artist', name: revision }] }
      else if (path.startsWith('/recommendations/')) body = { available: true, tracks: [track] }
      else body = []
    }
    return new Response(JSON.stringify(body), { status: 200 })
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } })
  function wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>
  }
  return { client, wrapper, requests }
}

function activeTitles(views: ReturnType<typeof useActiveViews>) {
  return [views.shelves.data?.shelves[0].tracks[0].title, views.mixes.data?.mixes[0].tracks[0].title,
    views.mix.data?.tracks[0].title, views.suggestions.data?.tracks[0].title,
    views.artists.data?.artists[0].name, views.tracks.data?.tracks[0].title]
}

afterEach(() => {
  vi.unstubAllGlobals()
  useToastStore.setState({ toasts: [] })
})

describe('recommendation actions with populated caches', () => {
  it.each(actions)('%s refreshes active views and expires inactive kinds, playlists and pages', async (action) => {
    const { client, wrapper, requests } = fixture(false)
    const active = renderHook(useActiveViews, { wrapper })
    const inactive = renderHook(useInactiveViews, { wrapper })
    const mutation = renderHook(useActions, { wrapper })
    await waitFor(() => {
      expect(Object.values(active.result.current).every((q) => q.isSuccess)).toBe(true)
      expect(Object.values(inactive.result.current).every((q) => q.isSuccess)).toBe(true)
      expect(activeTitles(active.result.current)).toEqual(['Before', 'Before', 'Before', 'Before', 'Before', 'Before'])
      expect(active.result.current.marks.data?.marks).toEqual([mark])
      expect(active.result.current.settings.data).toEqual({ adventurousness: 50, onlineRecommendations: true })
    })
    inactive.unmount()
    const initial = requests.slice()
    await act(async () => { await mutation.result.current(action) })
    await waitFor(() => expect(activeTitles(active.result.current)).toEqual(['After', 'After', 'After', 'After', 'After', 'After']))
    if (action === 'undo') await waitFor(() => expect(active.result.current.marks.data?.marks).toEqual([]))
    else if (action.startsWith('mark')) await waitFor(() => expect(active.result.current.marks.data?.marks[0].title).toBe('After'))
    else expect(active.result.current.settings.data).toEqual(action === 'adventurousness'
      ? { adventurousness: 80, onlineRecommendations: true } : { adventurousness: 50, onlineRecommendations: false })
    const after = requests.slice(initial.length)
    expect(after.filter((r) => r.includes('releaseRadar') || r.includes('page=1') || r.includes('playlist-b'))).toEqual([])
    expect(after.filter((r) => r.startsWith('GET /library/') || r === 'GET /downloads')).toEqual([])
    expect(after.filter((r) => !r.startsWith('GET '))).toHaveLength(1)
    const reopened = renderHook(useInactiveViews, { wrapper })
    await waitFor(() => {
      expect(reopened.result.current.mix.data?.tracks[0].title).toBe('After')
      expect(reopened.result.current.nextPage.data?.tracks[0].title).toBe('After')
      expect(reopened.result.current.otherPlaylist.data?.tracks[0].title).toBe('After')
    })
    active.unmount(); reopened.unmount(); mutation.unmount(); client.clear()
  })

  it.each(actions)('failed %s retains cached answers and marks/settings', async (action) => {
    const { client, wrapper, requests } = fixture(true)
    const active = renderHook(useActiveViews, { wrapper })
    const mutation = renderHook(useActions, { wrapper })
    await waitFor(() => expect(Object.values(active.result.current).every((q) => q.isSuccess)).toBe(true))
    const initial = requests.length
    await act(async () => { await expect(mutation.result.current(action)).rejects.toThrow('500') })
    expect(activeTitles(active.result.current)).toEqual(['Before', 'Before', 'Before', 'Before', 'Before', 'Before'])
    expect(active.result.current.marks.data?.marks).toEqual([mark])
    expect(active.result.current.settings.data).toEqual({ adventurousness: 50, onlineRecommendations: true })
    expect(requests.slice(initial).filter((r) => r.startsWith('GET '))).toEqual([])
    if (action.startsWith('mark')) expect(useToastStore.getState().toasts.at(-1)).toMatchObject({ message: 'Could not mark that Not interested', kind: 'error' })
    active.unmount(); mutation.unmount(); client.clear()
  })
})

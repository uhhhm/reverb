import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useRealtime } from './realtimeWiring'
import { useDownloads } from './downloadStore'
import { useLibraryRevision } from './libraryRevisionStore'
import type { WebSocketLike } from './realtime'
import { setQueueTransport, usePlayer } from './playerStore'
import { FakeQueue } from '../test/fakeQueue'
import type { Track } from './types'

// downloadApi resync is stubbed (no real network).
vi.mock('./downloadApi', () => ({
  getDownloads: vi.fn(() => Promise.resolve([])),
  getQueueState: vi.fn(() => Promise.resolve({ paused: false })),
}))

// A controllable stub socket the test drives.
const sockets: StubSocket[] = []
class StubSocket implements WebSocketLike {
  onopen: (() => void) | null = null
  onmessage: ((ev: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  closed = false
  url: string
  constructor(url: string) {
    this.url = url
    sockets.push(this)
  }
  close() {
    this.closed = true
    this.onclose?.()
  }
}

function frame(type: string, payload: unknown) {
  return { data: JSON.stringify({ type, payload }) }
}

describe('useRealtime', () => {
  let qc: QueryClient
  let invalidateSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    sockets.length = 0
    useDownloads.setState({ jobs: {} })
    useLibraryRevision.setState({ revision: 0 })
    qc = new QueryClient()
    invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
  })

  function wrapper({ children }: { children: ReactNode }) {
    return createElement(QueryClientProvider, { client: qc }, children)
  }

  it('updates the store on progress, handles completion, and invalidates', () => {
    useDownloads.getState().upsert({
      id: 'j1', dedupKey: 'dk', status: 'running', progress: 0, downloaderName: 'spotdl',
      priority: 0, attempts: 0, source: 'spotify', externalId: 'sp1', playWhenReady: false,
      title: 'Song', artist: 'Artist', album: 'Album', createdAt: 1, startedAt: 0, finishedAt: 0,
    } as never)

    const { unmount } = renderHook(() => useRealtime((url) => new StubSocket(url)), { wrapper })
    const s = sockets[0]
    expect(s.url).toContain('/api/v1/ws')

    // A progress event patches the store.
    s.onmessage?.(frame('download.progress', { jobId: 'j1', dedupKey: 'dk', status: 'running', progress: 42, source: 'spotify', externalId: 'sp1' }))
    expect(useDownloads.getState().jobs['j1'].progress).toBe(42)

    // A completion event: store reflects completed + libraryTrackId and invalidates.
    s.onmessage?.(frame('download.complete', { jobId: 'j1', dedupKey: 'dk', status: 'completed', progress: 100, source: 'spotify', externalId: 'sp1', libraryTrackId: 't9' }))
    expect(useDownloads.getState().jobs['j1'].status).toBe('completed')
    expect(useDownloads.getState().jobs['j1'].libraryTrackId).toBe('t9')
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['library'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['album-detail'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['artist-detail'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['synced-playlist'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['synced-playlists'] })

    // library.updated also invalidates (broad fallback even with empty IDs).
    invalidateSpy.mockClear()
    s.onmessage?.(frame('library.updated', { artistIds: [], albumIds: [] }))
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['library'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['album-detail'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['artist-detail'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['synced-playlist'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['synced-playlists'] })

    // Unmount closes the socket.
    unmount()
    expect(s.closed).toBe(true)
  })

  it('bumps library revision on download.complete', () => {
    vi.useFakeTimers()
    try {
      useDownloads.getState().upsert({
        id: 'j3', dedupKey: 'dk3', status: 'running', progress: 0, downloaderName: 'spotdl',
        priority: 0, attempts: 0, source: 'spotify', externalId: 'sp3', playWhenReady: false,
        title: 'Song3', artist: 'Artist3', album: 'Album3', createdAt: 1, startedAt: 0, finishedAt: 0,
      } as never)

      renderHook(() => useRealtime((url) => new StubSocket(url)), { wrapper })
      const s = sockets[0]
      expect(useLibraryRevision.getState().revision).toBe(0)

      s.onmessage?.(frame('download.complete', { jobId: 'j3', dedupKey: 'dk3', status: 'completed', progress: 100, source: 'spotify', externalId: 'sp3', libraryTrackId: 't3' }))
      vi.advanceTimersByTime(300)
      expect(useLibraryRevision.getState().revision).toBe(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('bumps library revision on library.updated', () => {
    vi.useFakeTimers()
    try {
      renderHook(() => useRealtime((url) => new StubSocket(url)), { wrapper })
      const s = sockets[0]
      expect(useLibraryRevision.getState().revision).toBe(0)

      s.onmessage?.(frame('library.updated', { artistIds: [], albumIds: [] }))
      vi.advanceTimersByTime(300)
      expect(useLibraryRevision.getState().revision).toBe(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('refreshes listening stats, and only them, when the core records a listen', () => {
    renderHook(() => useRealtime((url) => new StubSocket(url)), { wrapper })
    sockets[0].onmessage?.(frame('player.listen', { session: 'tab-1' }))
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['stats'] })
    expect(invalidateSpy).toHaveBeenCalledTimes(1)
    expect(useLibraryRevision.getState().revision).toBe(0)
  })

  it('handles download.queue (paused) and download.removed (drop jobs)', () => {
    useDownloads.setState({
      jobs: {
        x: { id: 'x', dedupKey: 'x', status: 'completed', progress: 100, downloaderName: 'spotdl', priority: 0, attempts: 0, source: 's', externalId: 'x', playWhenReady: false, createdAt: 1, startedAt: 0, finishedAt: 0 } as never,
      },
      paused: false,
    })
    renderHook(() => useRealtime((url) => new StubSocket(url)), { wrapper })
    const s = sockets[0]

    s.onmessage?.(frame('download.queue', { paused: true }))
    expect(useDownloads.getState().paused).toBe(true)

    s.onmessage?.(frame('download.removed', { jobIds: ['x'] }))
    expect(useDownloads.getState().jobs['x']).toBeUndefined()
  })

  // A playing player catches up through its once-a-second progress answer; a
  // paused one sends nothing, so only the notice can tell it. Failure cases:
  // the notice is ignored; a notice for another tab's session moves this one;
  // or a notice is skipped because its revision matches the one held, which a
  // session the core recreated can repeat.
  it('shows a paused player a queue change made by another request, from its session notice', async () => {
    const flush = () => act(async () => { await new Promise((r) => setTimeout(r, 0)) })
    const track = (id: string) => ({
      id, title: 'T' + id, albumId: 'al', album: 'Album', artistId: 'ar', artist: 'Artist',
      coverArtId: 'co', trackNumber: 1, discNumber: 1, durationMs: 1000, bitRate: 320,
      suffix: 'mp3', contentType: 'audio/mpeg',
    }) as Track
    const core = new FakeQueue()
    const transport = core.transport()
    const get = vi.fn(transport.get)
    setQueueTransport({ ...transport, get })
    usePlayer.getState().playTrackList([track('1'), track('2')], 0)
    await flush()
    usePlayer.getState().pause()
    renderHook(() => useRealtime((url) => new StubSocket(url)), { wrapper })
    const s = sockets[0]

    // The core dropped this session and recreated it: a new queue that has
    // counted up to the revision the player holds.
    const held = core.revision
    core.play([track('9')], 0)
    core.revision = held
    s.onmessage?.(frame('player.queue', { session: transport.session, revision: held }))
    await flush()
    expect(usePlayer.getState().queue.map((t) => t.id)).toEqual(['9'])
    expect(usePlayer.getState().playing).toBe(false)
    // Setup for what follows: back to the two-track queue, paused.
    core.play([track('1'), track('2')], 0)
    s.onmessage?.(frame('player.queue', { session: transport.session, revision: core.revision }))
    await flush()
    expect(usePlayer.getState().queue.map((t) => t.id)).toEqual(['1', '2'])
    get.mockClear()

    // A Radio refill lands after pause.
    core.enqueue([track('3'), track('4')], 'radio')
    s.onmessage?.(frame('player.queue', { session: 'another-tab', revision: core.revision }))
    await flush()
    expect(usePlayer.getState().queue.map((t) => t.id)).toEqual(['1', '2'])

    s.onmessage?.(frame('player.queue', { session: transport.session, revision: core.revision }))
    await flush()
    expect(usePlayer.getState().queue.map((t) => t.id)).toEqual(['1', '2', '3', '4'])
    expect(usePlayer.getState().origins).toEqual(['listener', 'listener', 'radio', 'radio'])
    expect(usePlayer.getState().current?.id).toBe('1')
    expect(usePlayer.getState().playing).toBe(false)
  })
})

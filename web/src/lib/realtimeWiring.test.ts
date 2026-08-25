import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useRealtime } from './realtimeWiring'
import { useDownloads } from './downloadStore'
import { useLibraryRevision } from './libraryRevisionStore'
import { useToastStore } from './toastStore'
import type { WebSocketLike } from './realtime'

// Player spy: usePlayer((s) => s.playTrackList) must return our spy, and
// usePlayer.getState() must expose a controllable `current` plus `enqueue`.
const playTrackList = vi.fn()
const enqueue = vi.fn()
const playerState: { current: unknown } = { current: null }
function usePlayerImpl(sel: (s: { playTrackList: typeof playTrackList }) => unknown) {
  return sel({ playTrackList })
}
usePlayerImpl.getState = () => ({ current: playerState.current, enqueue, playTrackList })
vi.mock('./playerStore', () => ({
  usePlayer: usePlayerImpl,
}))

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
    playTrackList.mockClear()
    enqueue.mockClear()
    playerState.current = null
    useDownloads.setState({ jobs: {} })
    useLibraryRevision.setState({ revision: 0 })
    useToastStore.setState({ toasts: [] })
    qc = new QueryClient()
    invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
  })

  function wrapper({ children }: { children: ReactNode }) {
    return createElement(QueryClientProvider, { client: qc }, children)
  }

  it('updates the store on progress, plays a play-when-ready completion, and invalidates', () => {
    // Seed a job started with playWhenReady so completion auto-plays.
    useDownloads.getState().upsert({
      id: 'j1', dedupKey: 'dk', status: 'running', progress: 0, downloaderName: 'spotdl',
      priority: 0, attempts: 0, source: 'spotify', externalId: 'sp1', playWhenReady: true,
      title: 'Song', artist: 'Artist', album: 'Album', createdAt: 1, startedAt: 0, finishedAt: 0,
    } as never)

    const { unmount } = renderHook(() => useRealtime((url) => new StubSocket(url)), { wrapper })
    const s = sockets[0]
    expect(s.url).toContain('/api/v1/ws')

    // A progress event patches the store.
    s.onmessage?.(frame('download.progress', { jobId: 'j1', dedupKey: 'dk', status: 'running', progress: 42, source: 'spotify', externalId: 'sp1' }))
    expect(useDownloads.getState().jobs['j1'].progress).toBe(42)

    // A completion event: store reflects completed + libraryTrackId, player auto-plays
    // (job had playWhenReady), and library + detail queries are invalidated.
    s.onmessage?.(frame('download.complete', { jobId: 'j1', dedupKey: 'dk', status: 'completed', progress: 100, source: 'spotify', externalId: 'sp1', libraryTrackId: 't9' }))
    expect(useDownloads.getState().jobs['j1'].status).toBe('completed')
    expect(useDownloads.getState().jobs['j1'].libraryTrackId).toBe('t9')
    expect(playTrackList).toHaveBeenCalledTimes(1)
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['library'] })
    // Detail-page query keys must also be invalidated so missing rows flip to playable.
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['album-detail'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['artist-detail'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['synced-playlist'] })

    // library.updated also invalidates (broad fallback even with empty IDs).
    invalidateSpy.mockClear()
    s.onmessage?.(frame('library.updated', { artistIds: [], albumIds: [] }))
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['library'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['album-detail'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['artist-detail'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['synced-playlist'] })

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

  it('does NOT auto-play a completion whose job had playWhenReady=false', () => {
    useDownloads.getState().upsert({
      id: 'j2', dedupKey: 'dk2', status: 'running', progress: 0, downloaderName: 'spotdl',
      priority: 0, attempts: 0, source: 'spotify', externalId: 'sp2', playWhenReady: false,
      title: 'Song2', artist: 'Artist2', album: 'Album2', createdAt: 1, startedAt: 0, finishedAt: 0,
    } as never)
    renderHook(() => useRealtime((url) => new StubSocket(url)), { wrapper })
    sockets[0].onmessage?.(frame('download.complete', { jobId: 'j2', dedupKey: 'dk2', status: 'completed', progress: 100, source: 'spotify', externalId: 'sp2', libraryTrackId: 't5' }))
    expect(playTrackList).not.toHaveBeenCalled()
  })

  it('enqueues (instead of auto-playing) and toasts when a play-when-ready completion arrives while a track is already loaded', () => {
    playerState.current = { id: 'now-playing' } as never
    useDownloads.getState().upsert({
      id: 'j4', dedupKey: 'dk4', status: 'running', progress: 0, downloaderName: 'spotdl',
      priority: 0, attempts: 0, source: 'spotify', externalId: 'sp4', playWhenReady: true,
      title: 'Song4', artist: 'Artist4', album: 'Album4', createdAt: 1, startedAt: 0, finishedAt: 0,
    } as never)

    renderHook(() => useRealtime((url) => new StubSocket(url)), { wrapper })
    sockets[0].onmessage?.(frame('download.complete', { jobId: 'j4', dedupKey: 'dk4', status: 'completed', progress: 100, source: 'spotify', externalId: 'sp4', libraryTrackId: 't4' }))

    expect(enqueue).toHaveBeenCalledTimes(1)
    expect(playTrackList).not.toHaveBeenCalled()
    const toasts = useToastStore.getState().toasts
    expect(toasts).toHaveLength(1)
    expect(toasts[0].kind).toBe('success')
    expect(toasts[0].message).toContain('Song4')
    expect(toasts[0].message).toContain('added to your queue')
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
})
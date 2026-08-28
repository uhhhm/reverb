import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { RealtimeConnection, type WebSocketLike } from './realtime'
import { useDownloads } from './downloadStore'
import { useLibraryRevision } from './libraryRevisionStore'
import { getDownloads, getQueueState } from './downloadApi'
import type { DownloadEvent, DownloadRemovedEvent, LibraryUpdatedEvent, QueueStateEvent, RealtimeEvent } from './types'

// useRealtime opens ONE app-wide WebSocket (distinct from the SSE search stream),
// fans typed events into the download store and drives TanStack invalidation.
// makeSocket is injectable for tests (a stub socket; no real network/media).
export function useRealtime(makeSocket?: (url: string) => WebSocketLike): void {
  const qc = useQueryClient()

  useEffect(() => {
    // Broad library invalidation is the MVP behavior; per-album/artist is a
    // best-effort optimization applied only when the id is present (deferred:
    // the backend may surface empty artistId/albumId on download.complete).
    // Detail-page queries use separate root keys — invalidate them too so a
    // completed download flips a missing row to playable without a hard reload.
    function invalidateLibrary(ids?: { artistId?: string; albumId?: string }) {
      void qc.invalidateQueries({ queryKey: ['library'] })
      if (ids?.albumId) void qc.invalidateQueries({ queryKey: ['library', 'album', ids.albumId] })
      if (ids?.artistId) void qc.invalidateQueries({ queryKey: ['library', 'artist', ids.artistId] })
      void qc.invalidateQueries({ queryKey: ['album-detail'] })
      void qc.invalidateQueries({ queryKey: ['artist-detail'] })
      void qc.invalidateQueries({ queryKey: ['synced-playlist'] })
    }

    function onEvent(frame: RealtimeEvent) {
      switch (frame.type) {
        case 'download.queued':
        case 'download.progress':
        case 'download.failed': {
          const event = frame.payload as DownloadEvent
          useDownloads.getState().applyEvent(event)
          break
        }
        case 'download.complete': {
          const ev = frame.payload as DownloadEvent
          useDownloads.getState().applyEvent(ev)
          invalidateLibrary({ artistId: ev.artistId, albumId: ev.albumId })
          useLibraryRevision.getState().bump()
          break
        }
        case 'library.updated': {
          const ev = frame.payload as LibraryUpdatedEvent
          const albumId = ev.albumIds?.[0]
          const artistId = ev.artistIds?.[0]
          invalidateLibrary({ artistId, albumId })
          // Bump the library revision so coverage streams re-open and chips flip.
          useLibraryRevision.getState().bump()
          break
        }
        case 'download.queue': {
          useDownloads.getState().setPaused((frame.payload as QueueStateEvent).paused)
          break
        }
        case 'download.removed': {
          useDownloads.getState().remove((frame.payload as DownloadRemovedEvent).jobIds)
          break
        }
        default:
          break
      }
    }

    function onOpen() {
      // Resync the full job list on (re)connect so we never miss a transition.
      void getDownloads().then((jobs) => useDownloads.getState().setAll(jobs))
      // Resync the paused flag (another client may have paused while we were away).
      void getQueueState()
        .then((q) => useDownloads.getState().setPaused(q.paused))
        .catch(() => {})
    }

    const conn = new RealtimeConnection({ onEvent, onOpen }, makeSocket)
    return () => conn.close()
  }, [qc])

  // Polling fallback: while any download is active, refresh the job list on an
  // interval. The WebSocket is the primary channel, but a reverse proxy that
  // doesn't upgrade WebSocket connections would otherwise leave the UI frozen at
  // the optimistic "queued" state — this keeps it accurate regardless.
  const activeCount = useDownloads((s) => s.active().length)
  useEffect(() => {
    if (activeCount === 0) return
    const t = setInterval(() => {
      void getDownloads().then((jobs) => useDownloads.getState().setAll(jobs))
    }, 3000)
    return () => clearInterval(t)
  }, [activeCount])
}

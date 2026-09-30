import { refreshLibrary } from './libraryQueries'
import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { RealtimeConnection, type WebSocketLike } from './realtime'
import { useDownloads } from './downloadStore'
import { useSyncStore } from './syncStore'
import { useLibraryRevision } from './libraryRevisionStore'
import { useUpdateStore } from './updateStore'
import { EMPTY_UPDATE_STATE } from './updateApi'
import { onQueueNotice } from './playerStore'
import { getDownloads, getQueueState } from './downloadApi'
import type { RealtimeEvent } from './types'

// useRealtime opens ONE app-wide WebSocket (distinct from the SSE search stream),
// fans typed events into the download store and drives TanStack invalidation.
// makeSocket is injectable for tests (a stub socket; no real network/media).
export function useRealtime(makeSocket?: (url: string) => WebSocketLike): void {
  const qc = useQueryClient()

  useEffect(() => {
    function onEvent(frame: RealtimeEvent) {
      switch (frame.type) {
        case 'download.queued':
        case 'download.progress':
        case 'download.failed': {
          const event = frame.payload
          useDownloads.getState().applyEvent(event)
          break
        }
        case 'download.complete': {
          const ev = frame.payload
          useDownloads.getState().applyEvent(ev)
          void refreshLibrary(qc)
          void qc.invalidateQueries({ queryKey: ['stats'] })
          useLibraryRevision.getState().bump()
          break
        }
        case 'library.updated': {
          void refreshLibrary(qc)
          void qc.invalidateQueries({ queryKey: ['stats'] })
          // Bump the library revision so coverage streams re-open and chips flip.
          useLibraryRevision.getState().bump()
          break
        }
        case 'download.queue': {
          useDownloads.getState().setPaused(frame.payload.paused)
          break
        }
        case 'sync.started': {
          useSyncStore.getState().setSyncing(true)
          break
        }
        case 'sync.finished': {
          useSyncStore.getState().setSyncing(false)
          break
        }
        case 'update:state': {
          // The updater publishes its whole state on every transition, so the
          // prompt appears the moment a download finishes without polling.
          useUpdateStore.getState().setState({
            ...EMPTY_UPDATE_STATE,
            ...frame.payload,
          })
          break
        }
        case 'player.queue': {
          // Another request changed a queue; a paused player would not
          // otherwise hear of it.
          onQueueNotice(frame.payload)
          break
        }
        case 'player.listen': {
          // The core recorded a play: listening history and stats moved.
          void qc.invalidateQueries({ queryKey: ['stats'] })
          break
        }
        case 'download.removed': {
          useDownloads.getState().remove(frame.payload.jobIds ?? [])
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

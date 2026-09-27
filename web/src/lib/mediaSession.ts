import type { PlayerState } from './audioEngine'
import { trackCoverUrl } from './libraryApi'

// What OS controls drive: the player store's actions (playerStore's `player`),
// never the engine, so a seek from the lock screen reaches the core as a seek.
// Tests supply a fake.
interface Playerlike {
  subscribe(cb: (s: PlayerState) => void): () => void
  play(): void
  pause(): void
  next(): void
  prev(): void
  seekMs(ms: number): void
}

const ACTIONS: MediaSessionAction[] = [
  'play',
  'pause',
  'previoustrack',
  'nexttrack',
  'seekto',
]

/**
 * startMediaSession mirrors playback state into navigator.mediaSession so OS
 * media keys, lock screens, and headphone buttons control Reverb and show
 * track metadata + artwork. No-op where the API is unavailable.
 * Returns a teardown function (unsubscribe + clear handlers/metadata).
 */
export function startMediaSession(player: Playerlike): () => void {
  if (typeof navigator === 'undefined' || !('mediaSession' in navigator)) {
    return () => {}
  }
  const ms = navigator.mediaSession

  const handlers: Partial<Record<MediaSessionAction, MediaSessionActionHandler>> = {
    play: () => player.play(),
    pause: () => player.pause(),
    previoustrack: () => player.prev(),
    nexttrack: () => player.next(),
    seekto: (d) => {
      if (typeof d.seekTime === 'number') player.seekMs(d.seekTime * 1000)
    },
  }
  for (const action of ACTIONS) {
    try {
      ms.setActionHandler(action, handlers[action] ?? null)
    } catch {
      // action not supported by this browser — fine, skip it
    }
  }

  let lastId = ''
  const unsub = player.subscribe((s) => {
    if (!s.current) {
      lastId = ''
      ms.metadata = null
      ms.playbackState = 'none'
      return
    }
    if (s.current.id !== lastId) {
      lastId = s.current.id
      const artwork = trackCoverUrl(s.current, 512)
      ms.metadata = new MediaMetadata({
        title: s.current.title,
        artist: s.current.artist,
        album: s.current.album,
        artwork: artwork ? [{ src: artwork, sizes: '512x512' }] : [],
      })
    }
    ms.playbackState = s.playing ? 'playing' : 'paused'
    try {
      // An unknown length has to clear the OS readout rather than skip the
      // update: leaving the last state in place shows the previous track's
      // length against the new track's position.
      if (s.durationMs > 0) {
        ms.setPositionState({
          duration: s.durationMs / 1000,
          playbackRate: 1,
          position: Math.min(s.currentTimeMs, s.durationMs) / 1000,
        })
      } else {
        ms.setPositionState()
      }
    } catch {
      // browsers throw on transiently inconsistent position state — ignore
    }
  })

  return () => {
    unsub()
    for (const action of ACTIONS) {
      try {
        ms.setActionHandler(action, null)
      } catch {
        // ignore — teardown is best-effort
      }
    }
    ms.metadata = null
  }
}

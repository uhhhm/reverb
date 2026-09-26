import type { PlayerState } from './audioEngine'
import * as scrobbleApi from './scrobbleApi'

// Minimal engine interface — real AudioEngine satisfies this; tests supply a fake.
interface Enginelike {
  subscribe(cb: (s: PlayerState) => void): () => void
}

/**
 * startNowPlaying subscribes to the given engine and fires nowPlayingFn once
 * each time the current track id changes.  It is fire-and-forget: errors are
 * swallowed so they never affect playback.  Returns an unsubscribe function.
 * Whether the track is then listened to is the core's call, from the
 * player's progress samples.
 */
export function startNowPlaying(
  engine: Enginelike,
  nowPlayingFn: (t: {
    title: string
    artist: string
    album: string
    durationMs: number
  }) => Promise<void> = scrobbleApi.nowPlaying,
): () => void {
  let lastId = ''

  function handleState(s: PlayerState): void {
    if (!s.current) {
      // Reset so the same track replaying after a null gap re-fires.
      lastId = ''
      return
    }

    if (s.current.id === lastId) return

    lastId = s.current.id
    const { title, artist, album, durationMs: trackDur } = s.current
    const durationMs = trackDur > 0 ? trackDur : s.durationMs

    nowPlayingFn({ title, artist, album, durationMs }).catch(() => {})
  }

  return engine.subscribe(handleState)
}

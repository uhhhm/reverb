import type { PlayerState } from './audioEngine'
import * as scrobbleApi from './scrobbleApi'

// What is playing, as the player store reports it (playerStore's `player`).
// Tests supply a fake.
interface Playerlike {
  subscribe(cb: (s: PlayerState) => void): () => void
}

/**
 * startNowPlaying subscribes to the player and fires nowPlayingFn once
 * each time the current track id changes.  It is fire-and-forget: errors are
 * swallowed so they never affect playback.  Returns an unsubscribe function.
 * Whether the track is then listened to is the core's call, from the
 * player's progress samples.
 */
export function startNowPlaying(
  player: Playerlike,
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

  return player.subscribe(handleState)
}

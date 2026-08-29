import { API_BASE } from './api'

/**
 * The playback gain, in dB, that brings a track to Reverb's reference level.
 *
 * null means "no gain available" (a remote library with no file to inspect, or
 * no ffmpeg) — the caller plays the track unmodified rather than guessing.
 * Measurement happens server-side on first ask and is cached, so this can be
 * slow the first time a track is played and instant afterwards.
 */
export async function fetchTrackGainDb(trackId: string): Promise<number | null> {
  const res = await fetch(`${API_BASE}/library/track/${encodeURIComponent(trackId)}/gain`, {
    credentials: 'include',
  })
  if (res.status === 204 || !res.ok) return null
  const body = (await res.json()) as { gainDb?: number }
  return typeof body.gainDb === 'number' ? body.gainDb : null
}

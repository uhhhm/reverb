import { API_BASE } from './api'

/**
 * The measured playable length of a track, in ms — what the server got by
 * decoding the file, not what its tag claims.
 *
 * null means "no measurement available" (a remote library with no file to
 * inspect, or no ffmpeg), and the caller keeps the length the library reported.
 * The first ask for a track runs a decode server-side and is cached after, so
 * it can be slow once and is instant afterwards.
 */
export async function fetchTrackDurationMs(trackId: string): Promise<number | null> {
  const res = await fetch(`${API_BASE}/library/track/${encodeURIComponent(trackId)}/duration`, {
    credentials: 'include',
  })
  if (res.status === 204 || !res.ok) return null
  const body = (await res.json()) as { durationMs?: number }
  return typeof body.durationMs === 'number' && body.durationMs > 0 ? body.durationMs : null
}

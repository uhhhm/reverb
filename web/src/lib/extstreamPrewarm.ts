import { prewarmExternalStream } from './libraryApi'
import type { ExternalResult } from './types'

/**
 * Resolving a not-in-library track to a playable URL takes seconds, and it
 * happens on the play path where the listener is waiting. Prewarming resolves it
 * ahead of time so a play the user does make is close to instant.
 *
 * Two things keep the waste bounded: only the first few results are worth the
 * yt-dlp process each one costs, and a track already prewarmed in this session
 * is never asked for twice.
 */
const PREWARM_LIMIT = 4

const seen = new Set<string>()

/** Resets the dedup set. Tests only — a session is otherwise cumulative. */
export function resetPrewarmed() {
  seen.clear()
}

/**
 * Prewarms the top results. Callers pass the list that actually streams from a
 * source — anything already in the library plays from the library and never
 * goes near the resolver.
 */
export function prewarmTopResults(results: ExternalResult[]) {
  let started = 0
  for (const r of results) {
    if (started >= PREWARM_LIMIT) return
    const key = `${r.source}:${r.externalId}`
    if (seen.has(key)) continue
    seen.add(key)
    started++
    prewarmExternalStream(r.source, r.externalId, r.artist, r.title)
  }
}

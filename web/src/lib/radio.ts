import type { components } from './generated/api'
import type { Track } from './types'

/** A track to seed Radio from, or an artist when title is omitted. */
export type RadioSeed = components['schemas']['RadioSeed']
/** What Radio starts with: tracks to play first, and what the core recommends from. */
export type RadioStart = Omit<components['schemas']['PlayerRadioRequest'], 'lead'> & { lead: Track[] }
const LIST_SEEDS = 5
function seedFromTrack(t: Track): RadioSeed {
  return { artist: t.artist, title: t.title, ...(t.mbid ? { mbid: t.mbid } : {}) }
}
/** Select the lead and seeds for the core's Radio session. */
export function radioFromTracks(tracks: Track[]): RadioStart {
  const step = Math.max(1, Math.floor(tracks.length / LIST_SEEDS))
  const seeds: RadioSeed[] = []
  for (let i = 0; i < tracks.length && seeds.length < LIST_SEEDS; i += step) {
    seeds.push(seedFromTrack(tracks[i]))
  }
  return { lead: tracks.slice(0, 1), seeds }
}


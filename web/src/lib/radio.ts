import type { PlayerState } from './audioEngine'
import { normalize } from './trackRef'
import type { Track } from './types'

/** A track to seed Radio from, or an artist when title is omitted. */
export interface RadioSeed {
  artist: string
  title?: string
}

/** What Radio starts with: tracks to play first, and what to recommend from. */
export interface RadioStart {
  lead: Track[]
  seeds: RadioSeed[]
}

/** How many tracks Radio keeps queued after the current one. */
export const RADIO_AHEAD = 3
/** The most tracks in a row Radio plays by one artist. */
export const MAX_ARTIST_RUN = 2
/** How many recent tracks seed each refill. */
const REFILL_SEEDS = 3
/** How many upcoming external tracks are resolved in advance. */
const PREWARM_AHEAD = 2
/** How many tracks of a list seed a Radio started from it. */
const LIST_SEEDS = 5

/** What a RadioSession needs from the player. */
export interface RadioHost {
  fetch(seeds: RadioSeed[]): Promise<Track[]>
  getState(): PlayerState
  /** Replaces the queue and starts playing. */
  play(tracks: Track[]): void
  append(track: Track): void
  /** Starts resolving an external track so it plays without a wait. */
  prewarm(track: Track): void
  /** The session ended itself (nothing to play). */
  ended(): void
}

/** How long a failed request waits before Radio asks again. */
export const RETRY_MS = 10_000

// A bracketed or dashed qualifier naming a reissue, not a different version.
const reissueRe = /\s*(?:[([{][^)\]}]*\b(?:remaster(?:ed)?|deluxe|bonus track)\b[^)\]}]*[)\]}]|[-–—]\s+[^-–—]*\b(?:remaster(?:ed)?|deluxe)\b.*$)/gi

/**
 * One recording, however many sources or reissues carry it. Not the
 * library's dedup key: this one deliberately folds "2011 Remaster" into the
 * original, mirroring the server's recording key, so a reissue in a later
 * batch is not queued again.
 */
function trackKey(t: { artist: string; title?: string }): string {
  return normalize(t.artist) + '\x1f' + normalize((t.title ?? '').replace(reissueRe, ''))
}

/**
 * Starts Radio from a list (an album or playlist): the first track plays, and
 * up to LIST_SEEDS tracks spread across the list seed the recommendations.
 */
export function radioFromTracks(tracks: Track[]): RadioStart {
  const step = Math.max(1, Math.floor(tracks.length / LIST_SEEDS))
  const seeds: RadioSeed[] = []
  for (let i = 0; i < tracks.length && seeds.length < LIST_SEEDS; i += step) {
    seeds.push({ artist: tracks[i].artist, title: tracks[i].title })
  }
  return { lead: tracks.slice(0, 1), seeds }
}

/**
 * An endless queue of recommendations after a seed. The session keeps
 * RADIO_AHEAD tracks queued, asks for more (seeded by the latest tracks) as
 * its pool runs low, never queues a recording twice, and never queues a third
 * track in a row by one artist — such a track waits in the pool instead.
 *
 * The session only appends. Whoever owns the player ends it when the listener
 * plays something else or clears the queue.
 */
export class RadioSession {
  private active = true
  private started: boolean
  private busy = false
  private fetching = false
  private retryAt = 0
  private pool: Track[] = []
  private pendingSeeds: RadioSeed[]
  /** Every recording this session has queued or pooled. */
  private seen = new Set<string>()
  /** Recordings already used as seeds. */
  private seeded = new Set<string>()
  private radioTracks = new WeakSet<Track>()
  private prewarmed = new Set<string>()
  private host: RadioHost
  private start: RadioStart

  constructor(host: RadioHost, start: RadioStart) {
    this.host = host
    this.start = start
    this.pendingSeeds = start.seeds
    this.started = start.lead.length > 0
    for (const t of start.lead) this.seen.add(trackKey(t))
    for (const s of start.seeds) if (s.title) this.seeded.add(trackKey(s))
  }

  get isActive(): boolean {
    return this.active
  }

  /** Whether the session queued this track (as opposed to the listener). */
  isRadioTrack(t: Track): boolean {
    return this.radioTracks.has(t)
  }

  begin() {
    if (this.started) this.host.play(this.start.lead)
    else void this.refill()
  }

  end() {
    this.active = false
    this.pool = []
  }

  /** Called on every player state change. */
  update() {
    if (!this.active || this.busy || !this.started) return
    this.busy = true
    try {
      this.fill()
      this.prewarmUpcoming()
    } finally {
      this.busy = false
    }
    if (this.ahead() < RADIO_AHEAD) void this.refill()
  }

  private ahead(): number {
    return this.host.getState().upNext.length
  }

  private fill() {
    while (this.ahead() < RADIO_AHEAD) {
      const i = this.pickIndex(this.host.getState().queue)
      if (i < 0) return
      const [t] = this.pool.splice(i, 1)
      this.radioTracks.add(t)
      this.host.append(t)
    }
  }

  /** The first pooled track that would not extend a run of one artist too far. */
  private pickIndex(queue: Track[]): number {
    const tail = queue.slice(-MAX_ARTIST_RUN).map((t) => normalize(t.artist))
    const blocked = tail.length === MAX_ARTIST_RUN && tail.every((a) => a === tail[0]) ? tail[0] : null
    return this.pool.findIndex((t) => normalize(t.artist) !== blocked)
  }

  private prewarmUpcoming() {
    const { queue, upNext } = this.host.getState()
    for (const i of upNext.slice(0, PREWARM_AHEAD)) {
      const t = queue[i]
      if (!t?.externalStream) continue
      const key = `${t.externalStream.source}:${t.externalStream.externalId}`
      if (this.prewarmed.has(key)) continue
      this.prewarmed.add(key)
      this.host.prewarm(t)
    }
  }

  /** The start's seeds, then the latest queued tracks not yet used as seeds. */
  private nextSeeds(): RadioSeed[] {
    if (this.pendingSeeds.length) {
      const seeds = this.pendingSeeds
      this.pendingSeeds = []
      return seeds
    }
    const seeds: RadioSeed[] = []
    const queue = this.host.getState().queue
    for (let i = queue.length - 1; i >= 0 && seeds.length < REFILL_SEEDS; i--) {
      const key = trackKey(queue[i])
      if (this.seeded.has(key)) continue
      this.seeded.add(key)
      seeds.push({ artist: queue[i].artist, title: queue[i].title })
    }
    return seeds
  }

  private async refill() {
    if (this.fetching || Date.now() < this.retryAt) return
    const seeds = this.nextSeeds()
    if (seeds.length === 0) {
      // Every queued track has seeded a request and nothing new came back:
      // there is no more Radio to play.
      if (this.pool.length === 0) this.stop()
      return
    }
    this.fetching = true
    let tracks: Track[]
    try {
      tracks = await this.host.fetch(seeds)
    } catch {
      // Keep the seeds for a later attempt; a state change after RETRY_MS
      // (playback ticks often) asks again.
      if (this.active) {
        this.pendingSeeds = seeds
        this.retryAt = Date.now() + RETRY_MS
      }
      return
    } finally {
      this.fetching = false
    }
    if (!this.active) return
    for (const t of tracks) {
      const key = trackKey(t)
      if (this.seen.has(key)) continue
      this.seen.add(key)
      this.pool.push(t)
    }
    if (!this.started) {
      const first = this.pool.shift()
      if (!first) {
        this.stop()
        return
      }
      this.started = true
      this.radioTracks.add(first)
      this.host.play([first])
      return
    }
    this.update()
  }

  private stop() {
    this.end()
    this.host.ended()
  }
}

import type { PlayerState } from './audioEngine'
import { normalize } from './trackRef'
import type { Track } from './types'

/** A track to seed Radio from, or an artist when title is omitted. */
export interface RadioSeed {
  artist: string
  title?: string
  mbid?: string
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
/** Skips of one artist that stop it for the rest of the session. */
export const ARTIST_SKIP_LIMIT = 2
/** How many recent tracks seed each refill. */
const REFILL_SEEDS = 3
/** How many upcoming external tracks are resolved in advance. */
const PREWARM_AHEAD = 2
/** How many tracks of a list seed a Radio started from it. */
const LIST_SEEDS = 5
/** A track left before this share of it was heard counts as a skip. */
const SKIP_BEFORE = 0.5
/** A track heard to within this of its end counts as finished, as the play tracker judges it. */
const FINISH_WITHIN_MS = 1_500
/** How far a skip pushes away, and a finished or repeated track pulls towards, related tracks. */
const SKIP_STEER = -1
const FINISH_STEER = 1

function seedFromTrack(track: Track): RadioSeed {
  return {
    artist: track.artist,
    title: track.title,
    ...(track.mbid ? { mbid: track.mbid } : {}),
  }
}

/** What a RadioSession needs from the player. */
export interface RadioHost {
  fetch(seeds: RadioSeed[]): Promise<Track[]>
  getState(): PlayerState
  /** Replaces the queue and starts playing. */
  play(tracks: Track[]): void
  append(track: Track): void
  /** Removes one queued track (never the current one). */
  remove(index: number): void
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
 * The seed a recommendation came from. Tracks recommended from one seed stand
 * in for a style: there is no genre data to steer by.
 */
function seedOf(t: Track): string | null {
  return t.reason?.title ? trackKey({ artist: t.reason.artist, title: t.reason.title }) : null
}

/** The track playing now, and how much of it has been heard. */
interface Listening {
  track: Track
  heardMs: number
  durationMs: number
  finished: boolean
}

/**
 * Starts Radio from a list (an album or playlist): the first track plays, and
 * up to LIST_SEEDS tracks spread across the list seed the recommendations.
 */
export function radioFromTracks(tracks: Track[]): RadioStart {
  const step = Math.max(1, Math.floor(tracks.length / LIST_SEEDS))
  const seeds: RadioSeed[] = []
  for (let i = 0; i < tracks.length && seeds.length < LIST_SEEDS; i += step) {
    seeds.push(seedFromTrack(tracks[i]))
  }
  return { lead: tracks.slice(0, 1), seeds }
}

/**
 * An endless queue of recommendations after a seed. The session keeps
 * RADIO_AHEAD tracks queued, asks for more (seeded by the latest tracks) as
 * its pool runs low, never queues a recording twice, and never queues a third
 * track in a row by one artist — such a track waits in the pool instead.
 *
 * Listening steers the session on top of the server's ranking. A skip (a
 * track left before half of it was heard) pushes its artist and the tracks
 * recommended from or alongside it down the pool; finishing or repeating a
 * track pulls them up. ARTIST_SKIP_LIMIT skips of one artist stop it for the
 * rest of the session. Each change re-ranks the tracks Radio queued; tracks the
 * listener queued stay put. A new session starts with no steering.
 *
 * Whoever owns the player ends the session when the listener plays something
 * else or clears the queue.
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
  /** Recordings already used as seeds, or never to be (skipped). */
  private seeded = new Set<string>()
  private radioTracks = new WeakSet<Track>()
  private prewarmed = new Set<string>()
  /** Session steering, keyed "artist:<artist>" or "seed:<recording>". */
  private steering = new Map<string, number>()
  private artistSkips = new Map<string, number>()
  /** Artists skipped ARTIST_SKIP_LIMIT times. */
  private blocked = new Set<string>()
  /** Recordings heard to the end this session. */
  private finished = new Set<string>()
  private listening: Listening | null = null
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
      this.observe()
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

  /** Follows the current track to judge, when it changes, whether it was skipped. */
  private observe() {
    const { current, currentTimeMs, durationMs } = this.host.getState()
    const was = this.listening
    if (was && was.track.id !== current?.id) {
      this.listening = null
      if (!was.finished && was.heardMs < was.durationMs * SKIP_BEFORE) this.steer(was.track, SKIP_STEER)
    }
    if (!current) return
    const now = this.listening
    if (!now) {
      this.listening = { track: current, heardMs: currentTimeMs, durationMs: durationMs || current.durationMs, finished: false }
      // Going back to a track heard to the end is a repeat.
      if (this.finished.has(trackKey(current))) this.steer(current, FINISH_STEER)
      return
    }
    if (durationMs > 0) now.durationMs = durationMs
    if (now.finished && currentTimeMs < FINISH_WITHIN_MS) {
      // Started over after finishing: a repeat.
      now.finished = false
      now.heardMs = 0
      this.steer(current, FINISH_STEER)
    }
    now.heardMs = Math.max(now.heardMs, currentTimeMs)
    if (!now.finished && now.heardMs > 0 && now.heardMs >= now.durationMs - FINISH_WITHIN_MS) {
      now.finished = true
      this.finished.add(trackKey(current))
      this.steer(current, FINISH_STEER)
    }
  }

  private steer(t: Track, by: number) {
    const key = trackKey(t)
    const artist = normalize(t.artist)
    this.adjust('artist:' + artist, by)
    this.adjust('seed:' + key, by)
    const seed = seedOf(t)
    if (seed) this.adjust('seed:' + seed, by)
    if (by < 0) {
      this.seeded.add(key)
      const skips = (this.artistSkips.get(artist) ?? 0) + 1
      this.artistSkips.set(artist, skips)
      if (skips >= ARTIST_SKIP_LIMIT) this.blocked.add(artist)
    }
    this.restack()
  }

  private adjust(key: string, by: number) {
    this.steering.set(key, (this.steering.get(key) ?? 0) + by)
  }

  private score(t: Track): number {
    const seed = seedOf(t)
    return (this.steering.get('artist:' + normalize(t.artist)) ?? 0) + (seed ? (this.steering.get('seed:' + seed) ?? 0) : 0)
  }

  /**
   * Re-ranks what Radio has lined up: the tracks it queued go back into the
   * pool, the pool re-sorts by steering, and the queue refills from it.
   */
  private restack() {
    if (!this.active) return
    const { queue, upNext } = this.host.getState()
    const ours = upNext.filter((i) => this.radioTracks.has(queue[i])).sort((a, b) => a - b)
    const taken = ours.map((i) => queue[i])
    for (const i of [...ours].reverse()) this.host.remove(i)
    this.pool = [...taken, ...this.pool]
    this.sortPool()
    // As many go back as were taken, even past RADIO_AHEAD when the listener's
    // own tracks are queued too.
    for (let n = 0; n < taken.length && this.appendNext(); n++);
    this.fill()
  }

  /** Drops blocked artists and orders the pool by steering; ties keep the server's order. */
  private sortPool() {
    this.pool = this.pool.filter((t) => !this.blocked.has(normalize(t.artist)))
    if (this.steering.size === 0) return
    const scores = new Map(this.pool.map((t) => [t, this.score(t)]))
    this.pool.sort((a, b) => scores.get(b)! - scores.get(a)!)
  }

  private fill() {
    while (this.ahead() < RADIO_AHEAD && this.appendNext());
  }

  /** Queues the best pooled track that keeps artist runs short, if there is one. */
  private appendNext(): boolean {
    const i = this.pickIndex(this.host.getState().queue)
    if (i < 0) return false
    const [t] = this.pool.splice(i, 1)
    this.radioTracks.add(t)
    this.host.append(t)
    return true
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

  /**
   * The start's seeds, then the latest queued tracks not yet used as seeds.
   * A skipped track or a blocked artist never seeds.
   */
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
      if (this.seeded.has(key) || this.blocked.has(normalize(queue[i].artist))) continue
      this.seeded.add(key)
      seeds.push(seedFromTrack(queue[i]))
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
    this.sortPool()
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

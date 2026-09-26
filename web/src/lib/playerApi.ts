import { api } from './api'
import type { components } from './generated/api'
import type { RadioStart } from './radio'
import type { Track } from './types'

/** The core's play queue, as it answers every queue request. */
export type QueueState = components['schemas']['QueueState']
export type QueueOrigin = 'listener' | 'radio'
export type RepeatMode = 'off' | 'all' | 'one'

/** A sample of playback, which the core's Radio judges skips and finishes by. */
export type PlayerProgress = components['schemas']['PlayerProgress']

/**
 * What the player asks of the core's queue. The core owns the queue; the
 * player sends it what the listener did and plays what it answers. Swappable
 * so tests run without a server.
 */
export interface QueueTransport {
  startRadio(start: RadioStart): Promise<QueueState>
  progress(sample: PlayerProgress): Promise<QueueState>
  get(): Promise<QueueState>
  play(tracks: Track[], start: number, origin?: QueueOrigin): Promise<QueueState>
  enqueue(tracks: Track[], origin?: QueueOrigin): Promise<QueueState>
  remove(positions: number[]): Promise<QueueState>
  move(from: number, to: number): Promise<QueueState>
  jump(index: number): Promise<QueueState>
  /** Skips on. With entryId, only if that entry is still current. */
  next(entryId?: string): Promise<QueueState>
  previous(): Promise<QueueState>
  /** Reports that entryId played to its end. */
  ended(entryId: string): Promise<QueueState>
  setShuffle(on: boolean): Promise<QueueState>
  setRepeat(mode: RepeatMode): Promise<QueueState>
  /** The Radio session stopped; what it queued becomes the listener's. */
  radioEnded(): Promise<QueueState>
  clear(): Promise<QueueState>
}

/** The queue with nothing in it, before the core has answered. */
export const EMPTY_QUEUE: QueueState = {
  entries: [], index: -1, shuffle: false, repeat: 'off', upNext: [], playId: 0, finished: false, revision: 0,
}

/**
 * A session id for this page. Each open player has its own queue in the core,
 * so two browser tabs never play each other's.
 */
export function newSessionId(): string {
  const c = globalThis.crypto as Crypto | undefined
  if (c?.randomUUID) return c.randomUUID()
  return Array.from({ length: 4 }, () => Math.random().toString(36).slice(2, 10)).join('-')
}

/** The core's queue over HTTP, for one player session. */
export function httpQueue(session: string): QueueTransport {
  const base = `/player/${encodeURIComponent(session)}`
  const post = (op: string, body?: unknown) => api.post<QueueState>(`${base}/${op}`, body ?? {})
  return {
    startRadio: (start) => post('radio', start),
    progress: (sample) => post('progress', sample),
    get: () => api.get<QueueState>(base),
    play: (tracks, start, origin = 'listener') => post('play', { tracks, start, origin }),
    enqueue: (tracks, origin = 'listener') => post('enqueue', { tracks, origin }),
    remove: (positions) => post('remove', { positions }),
    move: (from, to) => post('move', { from, to }),
    jump: (index) => post('jump', { index }),
    next: (entryId) => post('next', entryId ? { entryId } : {}),
    previous: () => post('previous'),
    ended: (entryId) => post('ended', { entryId }),
    setShuffle: (on) => post('shuffle', { on }),
    setRepeat: (mode) => post('repeat', { mode }),
    radioEnded: () => post('radio-ended'),
    clear: () => post('clear'),
  }
}

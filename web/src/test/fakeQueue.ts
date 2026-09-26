import type { QueueOrigin, QueueState, QueueTransport, RepeatMode } from '../lib/playerApi'
import type { Track } from '../lib/types'

let playIds = 0

type PlayerTrack = QueueState['entries'][number]['track']
/** A track as the core's queue carries it. */
const asEntryTrack = (t: Track) => t as unknown as PlayerTrack

/**
 * A queue state that plays tracks from index, as the core would answer a new
 * list. Each call starts a new play, so applying it loads the current track.
 */
export function queueState(tracks: Track[], index = 0, patch: Partial<QueueState> = {}): QueueState {
  const i = tracks.length ? Math.min(Math.max(index, 0), tracks.length - 1) : -1
  return {
    entries: tracks.map((t, n) => ({ id: `e${n}`, origin: 'listener' as const, track: asEntryTrack(t) })),
    index: i,
    shuffle: false,
    repeat: 'off',
    upNext: tracks.map((_, n) => n).filter((n) => n > i),
    playId: ++playIds,
    finished: false,
    revision: playIds,
    ...patch,
  }
}

/**
 * A stand-in for the core's queue, for tests of the player around it. It keeps
 * the core's contract — entries, origins, a playId that changes whenever the
 * current entry starts over, stale ends ignored, the listener's tracks ahead
 * of Radio's — but plays in queue order: shuffle is only a flag here. The
 * core's own tests cover its policy.
 */
export class FakeQueue {
  entries: { id: string; origin: QueueOrigin; track: Track }[] = []
  index = -1
  shuffle = false
  repeat: RepeatMode = 'off'
  playId = 0
  finished = false
  revision = 0
  private n = 0

  state(): QueueState {
    const upNext: number[] = []
    for (let i = this.index + 1; this.index >= 0 && i < this.entries.length; i++) upNext.push(i)
    if (this.repeat === 'all') for (let i = 0; i < this.index; i++) upNext.push(i)
    return {
      entries: this.entries.map((e) => ({ ...e, track: asEntryTrack({ ...e.track }) })),
      index: this.index, shuffle: this.shuffle, repeat: this.repeat, upNext,
      playId: this.playId, finished: this.finished, revision: this.revision,
    }
  }

  private entry(t: Track, origin: QueueOrigin) {
    return { id: `e${++this.n}`, origin, track: t }
  }
  private restart() {
    this.playId++
    this.finished = false
  }
  private changed() {
    this.revision++
  }
  current(): string {
    return this.entries[this.index]?.id ?? ''
  }

  play(tracks: Track[], start: number, origin: QueueOrigin = 'listener') {
    this.entries = tracks.map((t) => this.entry(t, origin))
    this.index = tracks.length ? Math.min(Math.max(start, 0), tracks.length - 1) : -1
    this.restart()
    this.changed()
  }
  enqueue(tracks: Track[], origin: QueueOrigin = 'listener') {
    if (tracks.length === 0) return
    let at = this.entries.length
    if (origin === 'listener') {
      const r = this.entries.findIndex((e, i) => i > this.index && e.origin === 'radio')
      if (r >= 0) at = r
    }
    this.entries.splice(at, 0, ...tracks.map((t) => this.entry(t, origin)))
    if (this.index === -1 && this.entries.length) this.index = 0
    else if (at <= this.index) this.index += tracks.length
    this.changed()
  }
  remove(positions: number[]) {
    let removed = false
    for (const p of [...new Set(positions)].sort((a, b) => b - a)) {
      if (p < 0 || p >= this.entries.length) continue
      const wasCurrent = p === this.index
      this.entries.splice(p, 1)
      if (p < this.index) this.index--
      if (this.index >= this.entries.length) this.index = this.entries.length - 1
      if (wasCurrent) this.restart()
      removed = true
    }
    if (removed) this.changed()
  }
  move(from: number, to: number) {
    const n = this.entries.length
    if (from < 0 || from >= n || to < 0 || to >= n || from === to) return
    const [e] = this.entries.splice(from, 1)
    this.entries.splice(to, 0, e)
    if (this.index === from) this.index = to
    else if (from < this.index && this.index <= to) this.index--
    else if (to <= this.index && this.index < from) this.index++
    this.changed()
  }
  jump(index: number) {
    if (index < 0 || index >= this.entries.length) return
    this.index = index
    this.restart()
    this.changed()
  }
  next(fromEnded = false) {
    if (!this.entries.length) return
    let ni = this.index + 1
    if (ni >= this.entries.length) {
      if (this.repeat !== 'all') {
        if (fromEnded && !this.finished) {
          this.finished = true
          this.changed()
        }
        return
      }
      ni = 0
    }
    this.index = ni
    this.restart()
    this.changed()
  }
  previous() {
    if (!this.entries.length) return
    this.index = Math.max(0, this.index - 1)
    this.restart()
    this.changed()
  }
  ended() {
    if (this.repeat === 'one') {
      this.restart()
      this.changed()
      return
    }
    this.next(true)
  }
  clear() {
    if (this.entries.length === 0 && this.index === -1) return
    this.entries = []
    this.index = -1
    this.restart()
    this.changed()
  }

  /** This queue as the store's transport, answering as the core would. */
  transport(): QueueTransport {
    const answer = (f: () => void) => {
      f()
      return Promise.resolve(this.state())
    }
    return {
      startRadio: async () => { throw new Error('Radio is tested through the core API') },
      progress: async () => this.state(),
      get: () => Promise.resolve(this.state()),
      play: (tracks, start, origin) => answer(() => this.play(tracks, start, origin)),
      enqueue: (tracks, origin) => answer(() => this.enqueue(tracks, origin)),
      remove: (positions) => answer(() => this.remove(positions)),
      move: (from, to) => answer(() => this.move(from, to)),
      jump: (index) => answer(() => this.jump(index)),
      next: (entryId) => answer(() => {
        if (!entryId || entryId === this.current()) this.next()
      }),
      previous: () => answer(() => this.previous()),
      ended: (entryId) => answer(() => {
        if (entryId === this.current()) this.ended()
      }),
      setShuffle: (on) => answer(() => {
        if (this.shuffle === on) return
        this.shuffle = on
        this.changed()
      }),
      setRepeat: (mode) => answer(() => {
        if (this.repeat === mode) return
        this.repeat = mode
        this.changed()
      }),
      radioEnded: () => answer(() => {
        let changed = false
        for (const e of this.entries) {
          if (e.origin === 'radio') {
            e.origin = 'listener'
            changed = true
          }
        }
        if (changed) this.changed()
      }),
      clear: () => answer(() => this.clear()),
    }
  }
}

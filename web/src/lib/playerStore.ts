import { create } from 'zustand'
import { AudioEngine, type ApplyOptions, type PlayerState } from './audioEngine'
import { httpQueue, newSessionId, type QueueState, type QueueTransport } from './playerApi'
import type { RadioStart } from './radio'
import type { Track } from './types'

// Single imperative engine instance, living OUTSIDE React.
export const engine = new AudioEngine()

// The core owns the queue. This page is one player session of it.
let queue: QueueTransport = httpQueue(newSessionId())

/** Replaces the core's queue, so tests run the store without a server. */
export function setQueueTransport(t: QueueTransport) {
  queue = t
}

/** How far into a track "previous" restarts it rather than going back. */
const RESTART_AFTER_MS = 3000

interface PlayerActions {
  playTrackList(tracks: Track[], startIndex: number): void
  enqueue(t: Track): void
  removeAt(i: number): void
  moveItem(from: number, to: number): void
  jumpTo(index: number): void
  play(): void
  pause(): void
  toggle(): void
  next(): void
  prev(): void
  seekMs(ms: number): void
  setVolume(v: number): void
  toggleShuffle(): void
  cycleRepeat(): void
  /** Plays the start's lead, then an endless stream of recommendations. */
  startRadio(start: RadioStart): void
  /** Stops playback and empties the queue, ending any Radio session. */
  clearQueue(): void
}

export type PlayerStore = PlayerState & PlayerActions & {
  /** Whether a Radio session is keeping the queue filled. */
  radio: boolean
}

// Queue requests go out one at a time, in the order the listener made them,
// and each answer is played before the next request is sent. Two quick clicks
// therefore apply in order, and every request is made against the queue the
// listener can see.
let pending: Promise<unknown> = Promise.resolve()

function change(
  request: () => Promise<QueueState>,
  opts: ApplyOptions | (() => ApplyOptions) = {},
  onFail?: () => void,
): Promise<boolean> {
  const run = pending.then(async () => {
    try {
      const state = await request()
      lastQueue = state
      usePlayer.setState({ radio: state.radio ?? false })
      engine.apply(state, typeof opts === 'function' ? opts() : opts)
    } catch (err) {
      console.warn('player: queue request failed', err)
      onFail?.()
      return false
    }
    return true
  })
  // The chain itself never rejects: a throwing apply must not wedge every
  // later change. Callers still see the per-request result via run.
  pending = run.catch(() => false)
  return run
}

// Whether playback is on as the answer lands, for moves that keep playing only
// what was already playing.
// The latest queue the core answered, and the pacing of progress reports:
// during Radio at most one a second, and never two at once.
let lastQueue: QueueState | undefined
let progressBusy = false
let lastProgressAt = 0

const keepPlaying = () => ({ autoplay: engine.getState().playing })

export const usePlayer = create<PlayerStore>((set) => {
  // Without the core's answer nothing says what plays next, so playback stops
  // rather than showing a track as playing in silence.
  const stop = () => engine.pause()
  engine.setQueueHandler({
    ended: (entryId) => { reportProgress(); void change(() => queue.ended(entryId), { autoplay: true, fromEnded: true }, stop) },
    skip: (entryId) => { reportProgress(); void change(() => queue.next(entryId), { autoplay: true }, stop) },
  })

  // Mirror engine state into the store on every change.
  engine.subscribe((s) => {
    set(s)
    if (lastQueue?.radio && !progressBusy && Date.now() - lastProgressAt >= 1000) reportProgress()
  })
  return {
    ...engine.getState(),
    radio: false,
    playTrackList: (tracks, startIndex) => {
      void change(() => queue.play(tracks, startIndex), { autoplay: true })
    },
    // A track the listener queues plays before any Radio has lined up; the
    // core puts it there.
    enqueue: (t) => void change(() => queue.enqueue([t])),
    removeAt: (i) => void change(() => queue.remove([i]), keepPlaying),
    moveItem: (from, to) => void change(() => queue.move(from, to)),
    jumpTo: (index) => { reportProgress(); void change(() => queue.jump(index), { autoplay: true }) },
    play: () => engine.play(),
    pause: () => engine.pause(),
    toggle: () => engine.toggle(),
    next: () => { reportProgress(); void change(() => queue.next(), keepPlaying) },
    prev: () => {
      // Well into a track, previous starts it again; only the player knows
      // how far in it is.
      if (engine.getState().currentTimeMs > RESTART_AFTER_MS) {
        reportProgress(true, 0)
        engine.seekMs(0)
        return
      }
      reportProgress()
      void change(() => queue.previous(), keepPlaying)
    },
    seekMs: (ms) => { reportProgress(true, ms); engine.seekMs(ms) },
    setVolume: (v) => engine.setVolume(v),
    // Read at send time, so two quick toggles flip twice.
    toggleShuffle: () => void change(() => queue.setShuffle(!engine.getState().shuffle)),
    cycleRepeat: () =>
      void change(() => {
        const r = engine.getState().repeat
        return queue.setRepeat(r === 'off' ? 'all' : r === 'all' ? 'one' : 'off')
      }),
    startRadio: (start) => {
      void change(() => queue.startRadio(start), { autoplay: true })
    },
    clearQueue: () => {
      void change(() => queue.clear())
    },
  }
})

function reportProgress(seeking = false, positionMs?: number) {
  if (!lastQueue?.radio) return
  const entry = lastQueue.entries[lastQueue.index]
  if (!entry) return
  const s = engine.getState()
  const sample = { entryId: entry.id, playId: lastQueue.playId,
    positionMs: positionMs ?? s.currentTimeMs, durationMs: s.durationMs, playing: s.playing, seeking }
  progressBusy = true
  lastProgressAt = Date.now()
  void change(() => queue.progress(sample)).finally(() => { progressBusy = false })
}

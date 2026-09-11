import { create } from 'zustand'
import { AudioEngine, type PlayerState } from './audioEngine'
import { prewarmExternalStream } from './libraryApi'
import { RadioSession, type RadioHost, type RadioStart } from './radio'
import { fetchRadio } from './recommendationsApi'
import type { Track } from './types'

// Single imperative engine instance, living OUTSIDE React.
export const engine = new AudioEngine()

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

export const usePlayer = create<PlayerStore>((set) => {
  let radio: RadioSession | null = null

  function endRadio() {
    radio?.end()
    radio = null
    set({ radio: false })
  }

  const host: RadioHost = {
    fetch: (seeds) => fetchRadio(seeds),
    getState: () => engine.getState(),
    play: (tracks) => engine.playTrackList(tracks, 0),
    append: (t) => engine.enqueue(t),
    prewarm: (t) => {
      if (t.externalStream) prewarmExternalStream(t.externalStream.source, t.externalStream.externalId, t.artist, t.title)
    },
    ended: endRadio,
  }

  // Mirror engine state into the store on every change.
  engine.subscribe((s) => {
    set(s)
    radio?.update()
  })
  return {
    ...engine.getState(),
    radio: false,
    playTrackList: (tracks, startIndex) => {
      endRadio()
      engine.playTrackList(tracks, startIndex)
    },
    enqueue: (t) => {
      // A track the listener queues plays before any Radio has lined up.
      const { queue, index } = engine.getState()
      const at = radio ? queue.findIndex((q, i) => i > index && radio!.isRadioTrack(q)) : -1
      if (at >= 0) engine.insertAt(at, t)
      else engine.enqueue(t)
    },
    removeAt: (i) => engine.removeAt(i),
    moveItem: (from, to) => engine.moveItem(from, to),
    jumpTo: (index) => engine.playAt(index),
    play: () => engine.play(),
    pause: () => engine.pause(),
    toggle: () => engine.toggle(),
    next: () => engine.next(),
    prev: () => engine.prev(),
    seekMs: (ms) => engine.seekMs(ms),
    setVolume: (v) => engine.setVolume(v),
    toggleShuffle: () => engine.toggleShuffle(),
    cycleRepeat: () => engine.cycleRepeat(),
    startRadio: (start) => {
      endRadio()
      // Radio's order is its point, and it never runs out: shuffle would
      // reshuffle played tracks back into up-next, and repeat would loop the
      // queue instead of refilling it.
      if (engine.getState().shuffle) engine.toggleShuffle()
      while (engine.getState().repeat !== 'off') engine.cycleRepeat()
      radio = new RadioSession(host, start)
      set({ radio: true })
      radio.begin()
    },
    clearQueue: () => {
      endRadio()
      engine.clear()
    },
  }
})

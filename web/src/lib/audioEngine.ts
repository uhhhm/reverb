import type { Track } from './types'
import { streamUrlFor } from './trackRef'
import { fetchTrackGainDb } from './gainApi'

export type RepeatMode = 'off' | 'all' | 'one'

export interface AudioElement {
  src: string
  currentTime: number
  duration: number
  volume: number
  paused: boolean
  play(): Promise<void>
  pause(): void
  load(): void
  buffered: { length: number; end(i: number): number; start(i: number): number }
  addEventListener(type: string, cb: () => void): void
  removeEventListener(type: string, cb: () => void): void
}

/**
 * The gain control the engine drives. Modelled as an interface so tests (and any
 * future non-Web-Audio path) can stand in for a real GainNode.
 */
export interface GainControl {
  gain: { value: number }
}

export interface PlayerState {
  queue: Track[]
  index: number
  current: Track | null
  playing: boolean
  currentTimeMs: number
  durationMs: number
  bufferedMs: number
  /**
   * True while the current track has no playable audio yet. An external track
   * (not in the library) has to be resolved to a source before its first byte
   * exists, which takes seconds — without this the UI looks frozen.
   */
  loading: boolean
  volume: number
  shuffle: boolean
  repeat: RepeatMode
}

function realAudioFactory(): AudioElement {
  return new Audio() as unknown as AudioElement
}

export class AudioEngine {
  private factory: () => AudioElement
  private resolveSrc: (t: Track) => string
  private active: AudioElement
  private preload: AudioElement
  private listeners = new Set<(s: PlayerState) => void>()

  private queue: Track[] = []
  private index = -1
  private playing = false
  private currentTimeMs = 0
  private durationMs = 0
  private bufferedMs = 0
  private volume = 1
  private shuffle = false
  private repeat: RepeatMode = 'off'

  // shuffle order: a permutation of queue indices; shufflePos points into it.
  private shuffleOrder: number[] = []
  private shufflePos = -1

  private loading = false

  // Playback-time loudness normalization. The file is never re-encoded: the
  // measured per-track gain is applied by a Web Audio GainNode in front of the
  // output. A media element can only be wrapped in a MediaElementSource ONCE,
  // which is why the graph is built against the long-lived `active` element and
  // then only has its gain value updated per track.
  private normalization = false
  private audioCtx: AudioContext | null = null
  private gainNode: GainControl | null = null
  private createGain: () => GainControl | null
  private gainDbCache = new Map<string, number>()
  private fetchGainDb: (trackId: string) => Promise<number | null>

  // stream-error recovery
  private consecutiveErrors = 0
  private repeatOneReloadAttempted = false

  constructor(
    factory: () => AudioElement = realAudioFactory,
    resolveSrc: (t: Track) => string = (t) => streamUrlFor(t),
    fetchGainDb: (trackId: string) => Promise<number | null> = fetchTrackGainDb,
    createGain?: () => GainControl | null,
  ) {
    this.factory = factory
    this.resolveSrc = resolveSrc
    this.fetchGainDb = fetchGainDb
    this.createGain = createGain ?? (() => this.createWebAudioGain())
    this.active = this.factory()
    this.preload = this.factory()
    this.bindActive()
  }

  private bindActive() {
    this.active.addEventListener('timeupdate', this.onTime)
    this.active.addEventListener('durationchange', this.onTime)
    this.active.addEventListener('progress', this.onTime)
    this.active.addEventListener('ended', this.onEnded)
    this.active.addEventListener('play', this.onPlayState)
    this.active.addEventListener('pause', this.onPlayState)
    this.active.addEventListener('error', this.onError)
    this.active.addEventListener('waiting', this.onWaiting)
    this.active.addEventListener('stalled', this.onWaiting)
    this.active.addEventListener('canplay', this.onLoaded)
    this.active.addEventListener('playing', this.onLoaded)
    // Note: preload errors are intentionally not handled — a preload error should
    // null/ignore the preload src silently, never advance the queue.
  }

  /**
   * The current track's crop, as absolute file positions in ms. A crop is
   * non-destructive: the file still holds every sample, so the engine simply
   * plays the window and reports times relative to it. `end` of 0 means "to the
   * end of the file".
   */
  private cropWindow(): { start: number; end: number } {
    const t = this.getState().current
    return { start: Math.max(0, t?.cropStartMs ?? 0), end: Math.max(0, t?.cropEndMs ?? 0) }
  }

  private onTime = () => {
    const { start, end } = this.cropWindow()
    const rawMs = Math.round((this.active.currentTime || 0) * 1000)
    const rawDurationMs = Number.isFinite(this.active.duration)
      ? Math.round((this.active.duration || 0) * 1000)
      : 0

    // Playing before the crop start (a fresh load, or a seek that landed short)
    // jumps forward rather than letting the trimmed intro through.
    if (start > 0 && rawMs < start - 250) {
      this.active.currentTime = start / 1000
      this.currentTimeMs = 0
      this.emit()
      return
    }

    // The crop end is the track's end: stop there and move on, exactly as if
    // the file had run out.
    if (end > 0 && rawMs >= end) {
      this.onEnded()
      return
    }

    this.currentTimeMs = Math.max(0, rawMs - start)
    const effectiveEnd = end > 0 ? end : rawDurationMs
    if (effectiveEnd > 0) {
      this.durationMs = Math.max(0, effectiveEnd - start)
    }
    const b = this.active.buffered
    if (b && b.length > 0) {
      this.bufferedMs = Math.max(0, Math.round(b.end(b.length - 1) * 1000) - start)
    }
    this.emit()
  }

  private onWaiting = () => {
    this.setLoading(true)
  }

  private onLoaded = () => {
    this.setLoading(false)
  }

  private setLoading = (v: boolean) => {
    if (this.loading === v) return
    this.loading = v
    this.emit()
  }

  private onPlayState = () => {
    this.playing = !this.active.paused
    if (!this.active.paused) {
      // Successful play: reset error counters so isolated dead tracks don't accumulate
      this.consecutiveErrors = 0
      this.repeatOneReloadAttempted = false
    }
    this.emit()
  }

  private onError = () => {
    // Whatever the recovery, this source produced no audio: clear the spinner so
    // a failed track never leaves the UI stuck on "loading". A reload or a skip
    // sets it again through loadCurrent.
    this.loading = false
    if (this.repeat === 'one') {
      // Attempt ONE reload on the pinned track. If the reload itself fires another error,
      // stop — never skip off the pinned track under repeat-one.
      if (!this.repeatOneReloadAttempted) {
        this.repeatOneReloadAttempted = true
        this.active.currentTime = 0
        void this.active.play()
        // playing stays true; let the next error (if any) fall through to the stop branch
        return
      }
      // Second failure: stop, do not advance
      this.playing = false
      this.emit()
      return
    }

    this.consecutiveErrors++
    if (this.consecutiveErrors >= 3) {
      // Backend-down storm: stop to prevent infinite skip loop
      this.playing = false
      this.consecutiveErrors = 0
      this.emit()
      return
    }

    // Skip the dead track and autoplay the next one
    this.advance(1, true)
  }

  private onEnded = () => {
    if (this.repeat === 'one') {
      this.active.currentTime = this.cropWindow().start / 1000
      void this.active.play()
      this.playing = true
      this.emit()
      return
    }
    this.advance(1, true)
  }

  /**
   * Turns loudness normalization on or off. Off simply pins the gain at unity —
   * the graph stays in place, because a MediaElementSource cannot be undone.
   */
  setNormalization(enabled: boolean) {
    if (this.normalization === enabled) return
    this.normalization = enabled
    if (!enabled) {
      if (this.gainNode) this.gainNode.gain.value = 1
      return
    }
    const t = this.getState().current
    if (t) void this.applyGainFor(t)
  }

  /** Builds the gain stage once and reuses it for every track. */
  private ensureGraph(): GainControl | null {
    if (this.gainNode) return this.gainNode
    this.gainNode = this.createGain()
    return this.gainNode
  }

  /** The real graph: AudioContext → MediaElementSource → GainNode → output. */
  private createWebAudioGain(): GainControl | null {
    if (typeof window === 'undefined') return null
    const Ctx = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
    if (!Ctx) return null
    const el = this.active as unknown as HTMLMediaElement
    // Test doubles are plain objects, not media elements; there is nothing to wrap.
    if (typeof HTMLMediaElement === 'undefined' || !(el instanceof HTMLMediaElement)) return null
    try {
      const ctx = new Ctx()
      const source = ctx.createMediaElementSource(el)
      const gain = ctx.createGain()
      source.connect(gain)
      gain.connect(ctx.destination)
      this.audioCtx = ctx
      return gain
    } catch {
      // A browser that refuses the graph (autoplay policy, already-wrapped
      // element) just means unmodified playback.
      return null
    }
  }

  /**
   * Sets the gain for one track. Applied asynchronously because the first
   * measurement of a file runs ffmpeg server-side; the track starts at unity
   * and settles to its level, rather than being held back on the network.
   */
  private async applyGainFor(track: Track) {
    if (!this.normalization || !track.id) return
    const gain = this.ensureGraph()
    if (!gain) return
    // The context starts suspended until a user gesture in most browsers.
    if (this.audioCtx?.state === 'suspended') void this.audioCtx.resume()

    let db = this.gainDbCache.get(track.id)
    if (db === undefined) {
      const fetched = await this.fetchGainDb(track.id).catch(() => null)
      db = fetched ?? 0
      this.gainDbCache.set(track.id, db)
    }
    // A track change during the fetch must not apply the wrong gain.
    if (this.getState().current?.id !== track.id) return
    if (!this.normalization) return
    gain.gain.value = Math.pow(10, db / 20)
  }

  subscribe(cb: (s: PlayerState) => void): () => void {
    this.listeners.add(cb)
    cb(this.getState())
    return () => this.listeners.delete(cb)
  }

  getState(): PlayerState {
    return {
      queue: [...this.queue],
      index: this.index,
      current: this.index >= 0 && this.index < this.queue.length ? this.queue[this.index] : null,
      playing: this.playing,
      currentTimeMs: this.currentTimeMs,
      durationMs: this.durationMs,
      bufferedMs: this.bufferedMs,
      loading: this.loading,
      volume: this.volume,
      shuffle: this.shuffle,
      repeat: this.repeat,
    }
  }

  private emit() {
    const s = this.getState()
    this.listeners.forEach((cb) => cb(s))
  }

  setQueue(tracks: Track[], startIndex = 0) {
    this.queue = tracks.slice()
    this.index = tracks.length ? Math.min(Math.max(startIndex, 0), tracks.length - 1) : -1
    this.rebuildShuffle()
    this.emit()
  }

  playTrackList(tracks: Track[], startIndex: number) {
    this.setQueue(tracks, startIndex)
    this.loadCurrent(true)
  }

  enqueue(track: Track) {
    this.queue = [...this.queue, track]
    if (this.index === -1) this.index = 0
    this.rebuildShuffle()
    this.emit()
  }

  removeAt(i: number) {
    if (i < 0 || i >= this.queue.length) return
    const wasCurrent = i === this.index
    this.queue = this.queue.filter((_, idx) => idx !== i)
    if (i < this.index) this.index--
    if (this.index >= this.queue.length) this.index = this.queue.length - 1
    this.rebuildShuffle()
    if (wasCurrent) this.loadCurrent(this.playing)
    this.emit()
  }

  moveItem(from: number, to: number) {
    if (from < 0 || from >= this.queue.length || to < 0 || to >= this.queue.length) return
    const currentId = this.index >= 0 ? this.queue[this.index]?.id : null
    const q = this.queue.slice()
    const [item] = q.splice(from, 1)
    q.splice(to, 0, item)
    this.queue = q
    if (currentId) {
      this.index = q.findIndex((t) => t.id === currentId)
    }
    this.rebuildShuffle()
    this.emit()
  }

  private loadCurrent(autoplay: boolean) {
    const t = this.getState().current
    if (!t) {
      this.playing = false
      this.emit()
      return
    }
    this.active.src = this.resolveSrc(t)
    this.active.load()
    this.loading = true
    this.currentTimeMs = 0
    // A crop starts playback inside the file. The assignment may be ignored
    // until metadata arrives, which onTime's forward-clamp then fixes.
    const cropStart = Math.max(0, t.cropStartMs ?? 0)
    this.active.currentTime = cropStart / 1000
    this.durationMs = t.cropEndMs && t.cropEndMs > cropStart ? t.cropEndMs - cropStart : 0
    if (this.normalization) {
      // Start at unity so a leftover gain from the previous track cannot leak in.
      if (this.gainNode) this.gainNode.gain.value = 1
      void this.applyGainFor(t)
    }
    if (autoplay) {
      void this.active.play()
      this.playing = true
    }
    this.preloadNext()
    this.emit()
  }

  private preloadNext() {
    const ni = this.peekNextIndex()
    if (ni < 0 || ni >= this.queue.length) return
    this.preload.src = this.resolveSrc(this.queue[ni])
    this.preload.load()
  }

  play() {
    if (this.index < 0 && this.queue.length) this.index = 0
    if (this.getState().current) {
      if (!this.active.src) this.loadCurrent(true)
      else {
        void this.active.play()
        this.playing = true
      }
    }
    this.emit()
  }

  pause() {
    this.active.pause()
    this.playing = false
    this.emit()
  }

  toggle() {
    if (this.playing) this.pause()
    else this.play()
  }

  private rebuildShuffle() {
    if (!this.shuffle) {
      this.shuffleOrder = []
      this.shufflePos = -1
      return
    }
    const idxs = this.queue.map((_, i) => i)
    // Fisher-Yates shuffle
    for (let i = idxs.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1))
      ;[idxs[i], idxs[j]] = [idxs[j], idxs[i]]
    }
    // ensure current track is first in the shuffle cycle
    if (this.index >= 0) {
      const at = idxs.indexOf(this.index)
      if (at > 0) [idxs[0], idxs[at]] = [idxs[at], idxs[0]]
    }
    this.shuffleOrder = idxs
    this.shufflePos = 0
  }

  private peekNextIndex(): number {
    if (this.queue.length === 0) return -1
    if (this.shuffle) {
      const np = this.shufflePos + 1
      if (np < this.shuffleOrder.length) return this.shuffleOrder[np]
      if (this.repeat === 'all') return this.shuffleOrder[0]
      return -1
    }
    const ni = this.index + 1
    if (ni < this.queue.length) return ni
    if (this.repeat === 'all') return 0
    return -1
  }

  private advance(dir: 1 | -1, fromEnded = false) {
    if (this.queue.length === 0) return
    if (this.shuffle) {
      let np = this.shufflePos + dir
      if (np >= this.shuffleOrder.length) {
        if (this.repeat === 'all') np = 0
        else {
          if (fromEnded) { this.playing = false; this.emit() }
          return
        }
      }
      if (np < 0) np = 0
      this.shufflePos = np
      this.index = this.shuffleOrder[np]
      this.loadCurrent(this.playing || fromEnded)
      return
    }
    let ni = this.index + dir
    if (ni >= this.queue.length) {
      if (this.repeat === 'all') ni = 0
      else {
        if (fromEnded) { this.playing = false; this.emit() }
        return
      }
    }
    if (ni < 0) ni = 0
    this.index = ni
    this.loadCurrent(this.playing || fromEnded)
  }

  playAt(index: number) {
    if (this.queue.length === 0 || index < 0 || index >= this.queue.length) return
    this.index = index
    if (this.shuffle) {
      // Align shufflePos so next/prev stay coherent from this index.
      const pos = this.shuffleOrder.indexOf(index)
      this.shufflePos = pos >= 0 ? pos : 0
    }
    this.loadCurrent(true)
  }

  next() {
    this.advance(1)
  }

  prev() {
    // restart current if >3s in, else go back
    if (this.currentTimeMs > 3000) {
      this.seekMs(0)
      return
    }
    this.advance(-1)
  }

  /** Seeks within the cropped window; ms is relative to the crop start. */
  seekMs(ms: number) {
    const clamped = Math.max(0, this.durationMs > 0 ? Math.min(ms, this.durationMs) : ms)
    this.active.currentTime = (this.cropWindow().start + clamped) / 1000
    this.currentTimeMs = clamped
    this.emit()
  }

  setVolume(v: number) {
    this.volume = Math.min(1, Math.max(0, v))
    this.active.volume = this.volume
    this.preload.volume = this.volume
    this.emit()
  }

  toggleShuffle() {
    this.shuffle = !this.shuffle
    this.rebuildShuffle()
    this.emit()
  }

  cycleRepeat() {
    this.repeat = this.repeat === 'off' ? 'all' : this.repeat === 'all' ? 'one' : 'off'
    this.emit()
  }
}

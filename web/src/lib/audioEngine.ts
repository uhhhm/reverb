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
  /**
   * Queue indices that will play after the current track, in the order they
   * will actually play. Under shuffle that is the remaining shuffle order, not
   * the tail of the queue — otherwise the last row of a playlist looks like the
   * end of playback even with most tracks still unplayed.
   */
  upNext: number[]
}

function realAudioFactory(): AudioElement {
  return new Audio() as unknown as AudioElement
}

const UP_NEXT_LIMIT = 20

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
  // Last raw media position seen by onTime, used to tell real stalling from a
  // spurious 'stalled'/'waiting': if the clock is still advancing, audio is
  // playing and the spinner must come down.
  private lastRawMs = -1

  // Playback-time loudness normalization. The file is never re-encoded: the
  // measured per-track gain is folded into the media element's own volume, so
  // the output level is always `volume * gainLinear`, clamped to the element's
  // 0..1 range.
  //
  // This deliberately does NOT use a Web Audio GainNode. In the desktop window
  // the page is served from the wails: scheme while audio has to be loaded from
  // the 127.0.0.1 listener (see mediaBase.ts), which makes the media element
  // cross-origin. A MediaElementSource over a cross-origin resource is tainted
  // and outputs silence, and the wrapping cannot be undone — so building that
  // graph muted playback for the rest of the session, whether normalization was
  // then left on or switched back off.
  //
  // The cost is that boosts are limited by the headroom the volume slider has
  // left: at full volume a track needing +6 dB stays at unity. Attenuation, the
  // common case for modern masters, is always exact.
  private normalization = false
  private gainLinear = 1
  private gainDbCache = new Map<string, number>()
  private fetchGainDb: (trackId: string) => Promise<number | null>

  // stream-error recovery
  private consecutiveErrors = 0
  private repeatOneReloadAttempted = false

  constructor(
    factory: () => AudioElement = realAudioFactory,
    resolveSrc: (t: Track) => string = (t) => streamUrlFor(t),
    fetchGainDb: (trackId: string) => Promise<number | null> = fetchTrackGainDb,
  ) {
    this.factory = factory
    this.resolveSrc = resolveSrc
    this.fetchGainDb = fetchGainDb
    this.active = this.factory()
    this.preload = this.factory()
    this.applyVolume()
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
    this.active.addEventListener('seeked', this.onLoaded)
    this.active.addEventListener('canplaythrough', this.onLoaded)
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

    // A proxied external stream drops and re-opens its upstream connection on a
    // seek, which fires 'stalled' even though the buffer keeps feeding the
    // element. Nothing further fires once playback simply continues, so the
    // advancing clock is what clears the spinner.
    if (rawMs !== this.lastRawMs) {
      this.lastRawMs = rawMs
      if (!this.active.paused) this.setLoading(false)
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
    // A newly attached source can come up at the element's own default rather
    // than the level the slider shows; re-assert it.
    this.applyVolume()
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
      this.gainLinear = 1
      this.applyVolume()
      return
    }
    const t = this.getState().current
    if (t) void this.applyGainFor(t)
  }

  /**
   * Sets the gain for one track. Applied asynchronously because the first
   * measurement of a file runs ffmpeg server-side; the track starts at unity
   * and settles to its level, rather than being held back on the network.
   */
  private async applyGainFor(track: Track) {
    if (!this.normalization || !track.id) return
    let db = this.gainDbCache.get(track.id)
    if (db === undefined) {
      const fetched = await this.fetchGainDb(track.id).catch(() => null)
      db = fetched ?? 0
      this.gainDbCache.set(track.id, db)
    }
    // A track change during the fetch must not apply the wrong gain.
    if (this.getState().current?.id !== track.id) return
    if (!this.normalization) return
    this.gainLinear = Math.pow(10, db / 20)
    this.applyVolume()
  }

  /**
   * Pushes the engine's level onto both elements. The engine's value is the
   * only truth: an element playing at some other level than the slider shows
   * would make the first slider drag jump the output.
   *
   * The preload element carries the plain volume — the gain belongs to the
   * track that is playing, and the next one gets its own on load.
   */
  private applyVolume() {
    this.active.volume = Math.min(1, Math.max(0, this.volume * this.gainLinear))
    this.preload.volume = this.volume
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
      upNext: this.upcomingIndices(UP_NEXT_LIMIT),
    }
  }

  /** The next `limit` queue indices in play order. */
  private upcomingIndices(limit: number): number[] {
    const out: number[] = []
    if (this.queue.length === 0 || this.index < 0) return out
    if (this.shuffle) {
      for (let p = this.shufflePos + 1; out.length < limit; p++) {
        if (p >= this.shuffleOrder.length) {
          if (this.repeat !== 'all') break
          p = -1
          continue
        }
        const i = this.shuffleOrder[p]
        if (i === this.index) break
        out.push(i)
      }
      return out
    }
    for (let i = this.index + 1; out.length < limit; i++) {
      if (i >= this.queue.length) {
        if (this.repeat !== 'all') break
        i = -1
        continue
      }
      if (i === this.index) break
      out.push(i)
    }
    return out
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
    this.applyVolume()
    this.loading = true
    this.lastRawMs = -1
    this.currentTimeMs = 0
    // A crop starts playback inside the file. The assignment may be ignored
    // until metadata arrives, which onTime's forward-clamp then fixes.
    const cropStart = Math.max(0, t.cropStartMs ?? 0)
    this.active.currentTime = cropStart / 1000
    this.durationMs = t.cropEndMs && t.cropEndMs > cropStart ? t.cropEndMs - cropStart : 0
    // Start at unity so a leftover gain from the previous track cannot leak in.
    this.gainLinear = 1
    this.applyVolume()
    if (this.normalization) void this.applyGainFor(t)
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
    this.preload.volume = this.volume
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
    if (this.shuffle) {
      // Move the picked track to right after the current position instead of
      // jumping to wherever it sat in the shuffle order — jumping would drop
      // every track between here and there from the cycle.
      const at = this.shuffleOrder.indexOf(index)
      if (at >= 0) this.shuffleOrder.splice(at, 1)
      const insert = Math.min(Math.max(this.shufflePos + (at >= 0 && at <= this.shufflePos ? 0 : 1), 0), this.shuffleOrder.length)
      this.shuffleOrder.splice(insert, 0, index)
      this.shufflePos = insert
    }
    this.index = index
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
    this.applyVolume()
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

import type { Track } from './types'
import { isExternalTrack, needsBackendSeek, streamUrlFor } from './trackRef'
import { fetchTrackGainDb } from './gainApi'
import { fetchTrackDurationMs } from './durationApi'

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
  /**
   * HTMLMediaElement.readyState / .error. Optional so a test stub need not
   * model them; see sourceLost, which is the only reader.
   */
  readyState?: number
  error?: unknown
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

// The largest forward step still read as playing rather than jumping.
// 'timeupdate' fires about every 250 ms; a stalled stream can stretch that, so
// this leaves room for a slow tick without admitting a seek.
const CONTINUOUS_TICK_MS = 2000

// How long a source that has failed is left before it is re-attached. A dropped
// connection is usually back within seconds, so the first retries are quick and
// then space out; the last delay repeats for as long as retrying continues.
const RETRY_DELAYS_MS = [1000, 2000, 5000, 10000, 30000]

// How many times a failing source is re-attached before the track is given up
// on and skipped. Only counts while the browser believes it is online — an
// offline device retries for as long as it takes, since there is nothing wrong
// with the track.
const MAX_ONLINE_RETRIES = 5

// How long a playing track may make no progress at all before its source is
// treated as failed. A connection dropping mid-stream frequently produces no
// 'error' event: the element fires 'waiting' and then waits forever.
const STALL_TIMEOUT_MS = 20000

export class AudioEngine {
  private factory: () => AudioElement
  private resolveSrc: (t: Track, startMs: number) => string
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
  // Largest end position claimed for the loaded track, and the furthest the
  // clock has actually reached in it; see effectiveEndMs. Both are reset on
  // every load and never during one.
  private claimedEndMs = 0
  private playedToMs = 0
  // Whether the loaded track has been seeked. Seeking a stream the browser has
  // no length for is a byte-offset guess, so everything the element reports
  // afterwards — its `duration`, and its clock — is offset by that guess rather
  // than read from the file. Neither counts as evidence of the length; see
  // effectiveEndMs and onTime.
  private seekedSinceLoad = false
  // The file position the loaded source begins at. Zero for the whole file; a
  // backend seek re-opens the stream partway in, and everything the element
  // reports is then relative to that point.
  private seekBaseMs = 0
  // The server's measured length for the loaded track, once it has arrived.
  // Unlike everything else this is decoded from the file, so it overrides the
  // claims rather than joining them.
  private measuredEndMs = 0
  private measuredCache = new Map<string, number | null>()
  private fetchDurationMs: (trackId: string) => Promise<number | null>

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
  // Whether the loaded source has already been re-attached once. One attempt
  // per load: a source that fails again after a fresh URL is genuinely dead.
  private reattachAttempted = false
  // A file position to apply once the re-attached source can accept it. Setting
  // currentTime on an element that has not loaded metadata yet is dropped.
  private pendingSeekMs = -1
  // How many times the loaded track's source has been re-attached, and the
  // timer for the next attempt. Reset by a load and by playback resuming.
  private retryAttempt = 0
  private retryTimer: ReturnType<typeof setTimeout> | null = null
  // True while a retry is being held back until the device is online again.
  // Playback intent is kept, so the track resumes by itself when the network
  // returns — after seconds or after hours.
  private awaitingNetwork = false
  // Watchdog for a source that stops producing audio without erroring.
  private stallTimer: ReturnType<typeof setTimeout> | null = null

  constructor(
    factory: () => AudioElement = realAudioFactory,
    resolveSrc: (t: Track, startMs: number) => string = (t, startMs) => streamUrlFor(t, startMs),
    fetchGainDb: (trackId: string) => Promise<number | null> = fetchTrackGainDb,
    fetchDurationMs: (trackId: string) => Promise<number | null> = fetchTrackDurationMs,
  ) {
    this.factory = factory
    this.resolveSrc = resolveSrc
    this.fetchGainDb = fetchGainDb
    this.fetchDurationMs = fetchDurationMs
    this.active = this.factory()
    this.preload = this.factory()
    this.applyVolume()
    this.bindActive()
    this.bindNetwork()
  }

  /**
   * Resumes a held-back retry as soon as the device is online again. Without
   * this an offline stretch longer than the backoff would leave the track
   * paused until the listener noticed and pressed play.
   */
  private bindNetwork() {
    const target = globalThis as unknown as {
      addEventListener?: (t: string, cb: () => void) => void
    }
    if (typeof target.addEventListener !== 'function') return
    target.addEventListener('online', this.onOnline)
  }

  private onOnline = () => {
    if (!this.awaitingNetwork) return
    this.awaitingNetwork = false
    this.retryAttempt = 0
    this.scheduleRetry(0)
  }

  /** Whether the device reports itself offline. Unknown counts as online. */
  private offline(): boolean {
    const nav = (globalThis as unknown as { navigator?: { onLine?: boolean } }).navigator
    return nav?.onLine === false
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

  /**
   * The playable length of the current track, in file positions. A crop end
   * defines it outright; otherwise it comes from two kinds of source, which are
   * combined rather than ranked.
   *
   * Claims — the tag, and the element's own `duration` — are guesses. A tag can
   * understate a VBR file, and a browser estimating a stream that declares no
   * length revises `duration` as it buffers. Each errs by being too short, so
   * the largest claim so far is kept, latched for as long as the track is
   * loaded: the readout can only correct upward instead of drifting down, back
   * up, and jumping again on every loop. A measured length, decoded from the
   * file server-side, replaces the claims outright in either direction — it is
   * the one source that cannot disagree with the file.
   *
   * Playback is not a claim. However short the claims are, and however short a
   * measurement comes out — ffmpeg trims an MP3's encoder padding that the
   * browser plays, a trailing tag can decode as a moment of audio — a position
   * the clock has *played* to is proof the track runs at least that far. So it
   * is a floor under the answer, and the readout can never be overrun by the
   * audio it describes.
   *
   * Only played-through positions count, never a seek (see onTime). Seeking is
   * offered up to the length shown, so counting where a seek lands would let a
   * click near the end of an already-too-long bar cite that bar as its own
   * evidence and latch it for the rest of the track.
   */
  private effectiveEndMs(): number {
    const { end } = this.cropWindow()
    if (end > 0) return end
    let claimed = this.measuredEndMs
    if (claimed <= 0) {
      const meta = this.getState().current?.durationMs ?? 0
      // Once the track has been seeked the element's own duration is a reading
      // taken from the seek target, not from the file: seeking is offered up to
      // the length shown, so a jump near the end of an already-too-long bar
      // would have the element restate that bar and latch it for good. The
      // claims gathered before the first seek stand — unless there were none at
      // all, where the element's guess is still better than no readout.
      const trustElement = !this.seekedSinceLoad || Math.max(this.claimedEndMs, meta) <= 0
      const element =
        trustElement && Number.isFinite(this.active.duration)
          ? Math.round((this.active.duration || 0) * 1000)
          : 0
      this.claimedEndMs = Math.max(this.claimedEndMs, element, meta)
      claimed = this.claimedEndMs
    }
    return Math.max(claimed, this.playedToMs)
  }

  private onTime = () => {
    const { start, end } = this.cropWindow()
    const rawMs = this.seekBaseMs + Math.round((this.active.currentTime || 0) * 1000)

    // Playing before the crop start (a fresh load, or a seek that landed short)
    // jumps forward rather than letting the trimmed intro through.
    if (this.seekBaseMs === 0 && start > 0 && rawMs < start - 250) {
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

    // How far the clock moved since the last tick, or -1 with nothing to compare
    // against. A tick's worth of playback is a small forward step; a seek is a
    // jump, and a reload starts over.
    const advancedBy = this.lastRawMs < 0 ? -1 : rawMs - this.lastRawMs

    // A proxied external stream drops and re-opens its upstream connection on a
    // seek, which fires 'stalled' even though the buffer keeps feeding the
    // element. Nothing further fires once playback simply continues, so the
    // advancing clock is what clears the spinner.
    if (rawMs !== this.lastRawMs) {
      this.lastRawMs = rawMs
      // Audio is flowing: the source is healthy, so the stall watchdog stands
      // down and the next interruption starts its backoff from the top.
      this.clearStall()
      if (!this.active.paused) {
        this.retryAttempt = 0
        this.awaitingNetwork = false
        this.setLoading(false)
      }
    }

    this.currentTimeMs = Math.max(0, rawMs - start)
    // Audio played through past the claimed end is proof the track is longer —
    // but only audio played through from the load, never after a seek. Seeking
    // an unlabelled stream is a byte-offset guess: the decoder lands somewhere
    // other than where the clock then says it is, and the real audio remaining
    // after it carries that clock past the true end. Positions counted from
    // there would be measuring the seek's error, not the file.
    if (!this.seekedSinceLoad && advancedBy > 0 && advancedBy <= CONTINUOUS_TICK_MS) {
      this.playedToMs = Math.max(this.playedToMs, rawMs)
    }
    const effectiveEnd = this.effectiveEndMs()
    if (effectiveEnd > 0) {
      this.durationMs = Math.max(0, effectiveEnd - start)
      // A clock offset by a seek's guess can run past the end it was seeked
      // within. There the length is the better answer, so the position is
      // pinned to it rather than allowed to overrun the rail it is drawn on.
      // Un-seeked, the clock is the trustworthy one and extends the length
      // instead (see playedToMs above).
      if (this.seekedSinceLoad) this.currentTimeMs = Math.min(this.currentTimeMs, this.durationMs)
    }
    const b = this.active.buffered
    if (b && b.length > 0) {
      this.bufferedMs = Math.max(0, this.seekBaseMs + Math.round(b.end(b.length - 1) * 1000) - start)
    }
    this.emit()
  }

  private onWaiting = () => {
    this.setLoading(true)
    this.armStall()
  }

  /**
   * (Re)starts the no-progress watchdog. Armed whenever the element says it is
   * waiting for data and cleared by the clock advancing, so it only ever fires
   * on a source that has genuinely stopped.
   */
  private armStall() {
    this.clearStall()
    if (!this.playing) return
    this.stallTimer = setTimeout(() => {
      this.stallTimer = null
      if (!this.playing) return
      if (!this.recover()) this.abandonTrack()
    }, STALL_TIMEOUT_MS)
  }

  private clearStall() {
    if (this.stallTimer !== null) {
      clearTimeout(this.stallTimer)
      this.stallTimer = null
    }
  }

  private clearRetry() {
    if (this.retryTimer !== null) {
      clearTimeout(this.retryTimer)
      this.retryTimer = null
    }
    this.awaitingNetwork = false
  }

  /**
   * Re-attaches the current source after `delay`, keeping the spinner up in the
   * meantime — a connection coming back is a wait, not a failure.
   */
  private scheduleRetry(delay: number) {
    this.clearStall()
    if (this.retryTimer !== null) clearTimeout(this.retryTimer)
    this.setLoading(true)
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null
      this.reattach(this.currentTimeMs, this.playing)
    }, delay)
  }

  /**
   * Handles a source that has stopped producing audio, whether it said so with
   * an 'error' or simply went quiet.
   *
   * Offline, the track is not at fault and there is nothing to skip to that
   * would fare any better, so the retry waits for the network however long that
   * takes. Online, it backs off through a few attempts — enough to ride out a
   * connection that drops for seconds or minutes — and only then treats the
   * track as dead and lets the caller skip it.
   *
   * Returns true when a retry has been arranged and the caller should stop.
   */
  private recover(): boolean {
    if (this.offline()) {
      // Retried on a timer as well as on the event: a device can come back
      // online without the event ever firing.
      this.scheduleRetry(RETRY_DELAYS_MS[RETRY_DELAYS_MS.length - 1])
      this.awaitingNetwork = true
      return true
    }
    if (this.retryAttempt >= MAX_ONLINE_RETRIES) return false
    const delay = RETRY_DELAYS_MS[Math.min(this.retryAttempt, RETRY_DELAYS_MS.length - 1)]
    this.retryAttempt++
    this.scheduleRetry(delay)
    return true
  }

  private onLoaded = () => {
    // A newly attached source can come up at the element's own default rather
    // than the level the slider shows; re-assert it.
    this.applyVolume()
    // Cleared before the assignment: this handler also runs on 'seeked'.
    if (this.pendingSeekMs >= 0) {
      const target = this.pendingSeekMs
      this.pendingSeekMs = -1
      this.active.currentTime = target / 1000
    }
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
      this.reattachAttempted = false
      this.retryAttempt = 0
    }
    this.emit()
  }

  private onError = () => {
    // Whatever the recovery, this source produced no audio: clear the spinner so
    // a failed track never leaves the UI stuck on "loading". A retry or a skip
    // sets it again.
    this.loading = false
    const current = this.getState().current
    if (!current) {
      this.abandonTrack()
      return
    }

    // A network failure says nothing about the track, so it is waited out
    // rather than skipped past — skipping would just fail on the next track and
    // walk the queue while the connection is down.
    if ((this.offline() || this.networkError()) && this.recover()) return

    // Otherwise: re-attach the source once before giving up on the track. The
    // usual cause is not a dead track but a stale source — a proxied external
    // stream whose upstream URL has expired, or an element the browser dropped
    // while it sat paused — and re-attaching resolves a fresh URL and carries
    // the position over, so a resume is still a resume.
    if (!this.reattachAttempted) {
      this.reattachAttempted = true
      this.reattach(this.currentTimeMs, this.playing || this.repeat === 'one')
      return
    }

    this.abandonTrack()
  }

  /** Whether the element's failure was the network rather than the media. */
  private networkError(): boolean {
    const err = this.active.error as { code?: number } | null | undefined
    // MEDIA_ERR_NETWORK. A decode or unsupported-source error is the file's
    // problem and retrying it forever would be a loop.
    return err?.code === 2
  }

  /**
   * Gives up on the current track: skips to the next one, or stops. Reached
   * only once recovery has been tried and the source is taken to be dead.
   */
  private abandonTrack() {
    this.clearRetry()
    this.clearStall()
    this.loading = false

    if (this.repeat === 'one') {
      // Never skip off the pinned track: stop instead.
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

    // Skip the dead track and autoplay the next one. With nothing to skip to,
    // stop — leaving `playing` true would show a playing track that is silent.
    const before = this.index
    this.advance(1, true)
    if (this.index === before && this.playing) {
      this.playing = false
      this.emit()
    }
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
   * Replaces the track's assumed length with the server's measured one.
   *
   * Fetched rather than waited on: the first measurement of a file decodes it
   * server-side, and holding playback back on that would cost a second of
   * silence at the start of every new track. The tag's length carries the
   * readout until this lands.
   */
  private async applyMeasuredDurationFor(track: Track) {
    // Nothing to decode for a track that is not a local file.
    if (!track.id || isExternalTrack(track)) return
    let ms = this.measuredCache.get(track.id)
    if (ms === undefined) {
      ms = await this.fetchDurationMs(track.id).catch(() => null)
      this.measuredCache.set(track.id, ms)
    }
    // A track change during the fetch must not apply the wrong length.
    if (ms == null || ms <= 0 || this.getState().current?.id !== track.id) return
    this.measuredEndMs = ms
    const { start } = this.cropWindow()
    this.durationMs = Math.max(0, this.effectiveEndMs() - start)
    this.emit()
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
    this.clearRetry()
    this.clearStall()
    this.retryAttempt = 0
    this.active.src = this.resolveSrc(t, 0)
    this.active.load()
    this.applyVolume()
    this.loading = true
    this.lastRawMs = -1
    this.currentTimeMs = 0
    // A crop starts playback inside the file. The assignment may be ignored
    // until metadata arrives, which onTime's forward-clamp then fixes.
    const cropStart = Math.max(0, t.cropStartMs ?? 0)
    this.active.currentTime = cropStart / 1000
    // Only the new track's own numbers: the element still carries the previous
    // source's duration until it has loaded metadata for this one.
    const knownEnd = t.cropEndMs && t.cropEndMs > cropStart ? t.cropEndMs : (t.durationMs || 0)
    this.claimedEndMs = Math.max(0, knownEnd)
    this.playedToMs = 0
    this.seekedSinceLoad = false
    this.seekBaseMs = 0
    this.pendingSeekMs = -1
    this.reattachAttempted = false
    this.measuredEndMs = 0
    this.durationMs = Math.max(0, knownEnd - cropStart)
    void this.applyMeasuredDurationFor(t)
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
    this.preload.src = this.resolveSrc(this.queue[ni], 0)
    this.preload.load()
    this.preload.volume = this.volume
  }

  play() {
    if (this.index < 0 && this.queue.length) this.index = 0
    if (this.getState().current) {
      const retrying = this.retryTimer !== null || this.awaitingNetwork
      if (!this.active.src) this.loadCurrent(true)
      else if (retrying || this.sourceLost()) {
        // Pressing play is a request to try now, whatever the backoff says.
        this.clearRetry()
        this.reattach(this.currentTimeMs, true)
      }
      else {
        this.playing = true
        const started = this.active.play() as unknown as Promise<void> | undefined
        if (started && typeof started.catch === 'function') {
          started.catch((err: unknown) => {
            // A browser refusing to autoplay is not a broken source; anything
            // else means this element will not produce audio again.
            if ((err as { name?: string })?.name === 'NotAllowedError') return
            if (this.playing) this.reattach(this.currentTimeMs, true)
          })
        }
      }
    }
    this.emit()
  }

  /**
   * Whether the element is holding a source it can no longer play.
   *
   * A media element that has sat paused for hours can have its resource
   * released by the browser — it keeps the src but drops back to HAVE_NOTHING,
   * so pressing play leaves it silent at 0:00 with no error to react to. An
   * element carrying a MediaError is the same situation, reported.
   */
  private sourceLost(): boolean {
    if (this.active.error) return true
    return this.active.readyState === 0
  }

  /**
   * Re-attaches the current track's source and continues from `ms` (measured
   * from the crop start).
   *
   * The source is resolved again rather than reused, which is what matters for
   * an external track: its audio is proxied from an upstream URL that expires
   * on its own schedule, so the fresh resolve is the whole point of the reload.
   */
  private reattach(ms: number, autoplay: boolean) {
    const t = this.getState().current
    if (!t) return
    this.clearStall()
    const at = Math.max(0, ms)
    const target = this.cropWindow().start + at
    const backendSeek = target > 0 && needsBackendSeek(t)
    this.active.src = this.resolveSrc(t, backendSeek ? target : 0)
    this.active.load()
    this.applyVolume()
    this.seekBaseMs = backendSeek ? target : 0
    // A browser-seekable source is put back by position once it has metadata;
    // assigning now would be dropped.
    this.pendingSeekMs = backendSeek || target === 0 ? -1 : target
    this.currentTimeMs = at
    this.lastRawMs = -1
    this.playedToMs = 0
    // Resuming partway in is a seek: what the element reports about its length
    // from here is read from the seek target, not from the file.
    this.seekedSinceLoad = at > 0
    this.loading = true
    if (autoplay) {
      void this.active.play()
      this.playing = true
      // A source that comes up silent and never errors is caught by this.
      this.armStall()
    }
    this.emit()
  }

  pause() {
    // A retry scheduled for a track the listener has just paused would restart
    // it under them; play() arranges a fresh one.
    this.clearRetry()
    this.clearStall()
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
    const target = this.cropWindow().start + clamped
    const t = this.getState().current
    if (t && needsBackendSeek(t)) {
      // The browser cannot find this position in the file, so the stream is
      // re-opened at it instead. The element then plays from zero and its clock
      // is read through seekBaseMs. Seeking back to the start re-opens the whole
      // file the same way — the loaded source is a fragment, so its own zero is
      // wherever the last seek landed, not the track's beginning.
      this.seekBaseMs = target
      this.active.src = this.resolveSrc(t, target)
      this.active.load()
      this.applyVolume()
      if (this.playing) void this.active.play()
    } else {
      this.seekBaseMs = 0
      this.active.currentTime = target / 1000
    }
    this.currentTimeMs = clamped
    this.seekedSinceLoad = true
    // The next tick lands wherever this seek went, which is not a step from the
    // old position — there is nothing to measure it against.
    this.lastRawMs = -1
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

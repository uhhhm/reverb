import type { Track } from './types'
import { isExternalTrack, needsBackendSeek, streamUrlFor } from './trackRef'
import { fetchTrackGainDb } from './gainApi'
import { fetchTrackDurationMs } from './durationApi'
import { EMPTY_QUEUE, type QueueOrigin, type QueueState, type RepeatMode } from './playerApi'

export type { RepeatMode } from './playerApi'

/**
 * Where the engine reports the two moments playback itself decides the queue
 * should move on. The core owns the queue, so the engine only says what
 * happened, for the entry it was playing; the answer comes back through apply.
 */
export interface QueueHandler {
  /** The entry played to its end. */
  ended(entryId: string): void
  /** The entry could not be played and has been given up on. */
  skip(entryId: string): void
}

export interface ApplyOptions {
  /** Whether a newly started entry plays. Defaults to whether playback is on. */
  autoplay?: boolean
  /** The state answers an ended report, so a finished queue stops playback. */
  fromEnded?: boolean
}

export interface AudioElement {
  src: string
  currentTime: number
  duration: number
  volume: number
  paused: boolean
  ended?: boolean
  play(): Promise<void>
  pause(): void
  load(): void
  removeAttribute?(name: string): void
  buffered: { length: number; end(i: number): number; start(i: number): number }
  /**
   * HTMLMediaElement.readyState / .error. Optional so a minimal test stub need
   * not model resource loss or network errors.
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
  /** Who queued each entry, parallel to queue. */
  origins: QueueOrigin[]
}

function realAudioFactory(): AudioElement {
  return new Audio() as unknown as AudioElement
}

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
  // The source last handed to the preload element, so re-preloading the same
  // next track does not restart its download.
  private preloadedSrc = ''
  private loadedTrack: Track | null = null
  // Invalidates pending play promises on pause, seek, and source replacement.
  private playRequest = 0
  private finished = false
  private listeners = new Set<(s: PlayerState) => void>()

  // The core's queue as last applied, and its tracks. The engine plays the
  // current entry and preloads the first one up next; what those are is the
  // core's decision, never the engine's.
  private snap: QueueState = EMPTY_QUEUE
  private tracks: Track[] = []
  private queueHandler: QueueHandler | null = null
  // The entry the loaded track belongs to. Tracks arrive as fresh objects on
  // every apply, so the entry id, not object identity, says whether the
  // loaded audio is still the current entry.
  private loadedEntry = ''
  // The entry an end has already been reported for, so the ticks that follow
  // the end do not report it again while the answer is on its way.
  private endReported = ''
  private playing = false
  private currentTimeMs = 0
  private durationMs = 0
  private bufferedMs = 0
  private volume = 1

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
    this.active.addEventListener('playing', this.onPlaying)
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
    if (!this.loadedTrack || !this.getState().current || this.pendingSeekMs >= 0) return
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
    if (this.playing && end > 0 && rawMs >= end) {
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
      // Only forward playback proves health. A freshly opened fragment starts
      // at seekBaseMs even when it has not produced a single sample yet.
      const advancing = advancedBy > 0 || (advancedBy === -1 && rawMs > Math.max(start, this.seekBaseMs))
      if (!this.active.paused && this.playing && advancing) {
        this.markHealthy()
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
    if (!this.playing || !this.loadedTrack) return
    this.setLoading(true)
    if (this.stallTimer === null && this.retryTimer === null) this.armStall()
  }

  /**
   * Starts the no-progress watchdog on a play request and refreshes it when
   * audio advances. Repeated waiting events must not extend the deadline.
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
      if (this.playing) this.reattach(this.currentTimeMs, true)
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
    if (!this.loadedTrack) return
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

  private markHealthy() {
    this.consecutiveErrors = 0
    this.reattachAttempted = false
    this.retryAttempt = 0
    this.clearRetry()
    this.armStall()
  }

  private onPlaying = () => {
    if (!this.playing) return
    this.onLoaded()
    this.markHealthy()
  }

  private setLoading = (v: boolean) => {
    if (this.loading === v) return
    this.loading = v
    this.emit()
  }

  private onPlayState = () => {
    // A play event only reflects a request, not successful audio. Likewise an
    // error can pause the element without cancelling the listener's intent.
    if (!this.playing && !this.active.paused) {
      this.active.pause()
    } else if (this.active.paused && !this.active.ended && !this.loading && !this.active.error && this.retryTimer === null) {
      this.playing = false
    }
    this.emit()
  }

  private onError = () => {
    // Whatever the recovery, this source produced no audio: clear the spinner so
    // a failed track never leaves the UI stuck on "loading". A retry or a skip
    // sets it again.
    this.loading = false
    const current = this.getState().current
    if (!current || !this.playing) {
      this.clearRetry()
      this.clearStall()
      this.emit()
      return
    }

    // A network failure says nothing about the track, so it is waited out
    // rather than skipped past — skipping would just fail on the next track and
    // walk the queue while the connection is down.
    const errorCode = (this.active.error as { code?: number } | null)?.code
    // Unsupported-source also covers an HTTP request failing before metadata;
    // while offline it is not proof of a bad file. A decode error is.
    const brokenMedia = errorCode === 3
    if (!brokenMedia && (this.offline() || this.networkError()) && this.recover()) return

    // Otherwise: re-attach the source once before giving up on the track. The
    // usual cause is not a dead track but a stale source — a proxied external
    // stream whose upstream URL has expired, or an element the browser dropped
    // while it sat paused — and re-attaching resolves a fresh URL and carries
    // the position over, so a resume is still a resume.
    if (!this.reattachAttempted) {
      this.reattachAttempted = true
      this.reattach(this.currentTimeMs, true)
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

    const next = this.snap.upNext[0]
    if (this.snap.repeat === 'one' || next === undefined || next === this.snap.index || !this.queueHandler) {
      // Nothing else to try. Keep the position for an explicit retry, and
      // stop the element too (a watchdog can arrive while it is still active).
      this.pause()
      return
    }

    this.consecutiveErrors++
    if (this.consecutiveErrors >= 3) {
      // Backend-down storm: stop to prevent infinite skip loop
      this.consecutiveErrors = 0
      this.pause()
      return
    }

    // Skip the dead track; the core's answer starts the next one.
    this.queueHandler.skip(this.loadedEntry)
  }

  private onEnded = () => {
    if (!this.playing || !this.loadedTrack || this.endReported === this.loadedEntry) return
    if (!this.queueHandler) {
      this.finishPlayback()
      return
    }
    this.endReported = this.loadedEntry
    this.queueHandler.ended(this.loadedEntry)
  }

  /** Sets where the engine reports ends and skips. */
  setQueueHandler(h: QueueHandler | null) {
    this.queueHandler = h
  }

  /**
   * Plays the core's queue. A new playId means the current entry starts from
   * its beginning; otherwise whatever is playing carries on, and only what is
   * preloaded next can change.
   */
  apply(state: QueueState, opts: ApplyOptions = {}) {
    const prevPlayId = this.snap.playId
    this.snap = state
    this.tracks = state.entries.map((e) => e.track as unknown as Track)
    if (!this.getState().current) {
      this.unload()
      this.emit()
      return
    }
    if (state.playId !== prevPlayId) {
      this.loadCurrent(opts.autoplay ?? this.playing)
      return
    }
    if (opts.fromEnded && state.finished && this.loadedEntry === this.currentEntry()) {
      this.finishPlayback()
      return
    }
    if (this.active.src) this.preloadNext()
    this.emit()
  }

  /** The current entry's id, or '' with nothing to play. */
  private currentEntry(): string {
    return this.snap.entries[this.snap.index]?.id ?? ''
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
    const index = this.snap.index
    return {
      queue: [...this.tracks],
      index,
      current: index >= 0 && index < this.tracks.length ? this.tracks[index] : null,
      playing: this.playing,
      currentTimeMs: this.currentTimeMs,
      durationMs: this.durationMs,
      bufferedMs: this.bufferedMs,
      loading: this.loading,
      volume: this.volume,
      shuffle: this.snap.shuffle,
      repeat: this.snap.repeat,
      upNext: [...this.snap.upNext],
      origins: this.snap.entries.map((e) => e.origin),
    }
  }

  private emit() {
    const s = this.getState()
    this.listeners.forEach((cb) => cb(s))
  }

  private release(audio: AudioElement) {
    audio.pause()
    if (audio.removeAttribute) audio.removeAttribute('src')
    else audio.src = ''
    audio.load()
  }

  private unload() {
    this.playRequest++
    this.clearRetry()
    this.clearStall()
    this.loadedTrack = null
    this.loadedEntry = ''
    this.endReported = ''
    this.playing = false
    this.loading = false
    this.finished = false
    this.pendingSeekMs = -1
    this.seekBaseMs = 0
    this.currentTimeMs = 0
    this.durationMs = 0
    this.bufferedMs = 0
    this.preloadedSrc = ''
    this.release(this.active)
    this.release(this.preload)
  }

  private loadCurrent(autoplay: boolean) {
    const t = this.getState().current
    if (!t) {
      this.unload()
      this.emit()
      return
    }
    this.clearRetry()
    this.clearStall()
    this.playRequest++
    this.loadedTrack = t
    this.loadedEntry = this.currentEntry()
    this.endReported = ''
    this.finished = false
    this.playing = autoplay
    this.loading = true
    this.retryAttempt = 0
    const cropStart = Math.max(0, t.cropStartMs ?? 0)
    const backendSeek = cropStart > 0 && needsBackendSeek(t)
    this.seekBaseMs = backendSeek ? Math.floor(cropStart / 1000) * 1000 : 0
    this.active.src = this.resolveSrc(t, backendSeek ? cropStart : 0)
    this.active.load()
    this.applyVolume()
    this.lastRawMs = -1
    this.currentTimeMs = 0
    this.bufferedMs = 0
    // A crop starts playback inside the file. The assignment may be ignored
    // until metadata arrives, which onTime's forward-clamp then fixes.
    this.active.currentTime = (cropStart - this.seekBaseMs) / 1000
    // Only the new track's own numbers: the element still carries the previous
    // source's duration until it has loaded metadata for this one.
    const knownEnd = t.cropEndMs && t.cropEndMs > cropStart ? t.cropEndMs : (t.durationMs || 0)
    this.claimedEndMs = Math.max(0, knownEnd)
    this.playedToMs = 0
    this.seekedSinceLoad = cropStart > 0
    this.pendingSeekMs = backendSeek && cropStart > this.seekBaseMs ? cropStart - this.seekBaseMs : -1
    this.reattachAttempted = false
    this.measuredEndMs = 0
    this.durationMs = Math.max(0, knownEnd - cropStart)
    void this.applyMeasuredDurationFor(t)
    // Start at unity so a leftover gain from the previous track cannot leak in.
    this.gainLinear = 1
    this.applyVolume()
    if (this.normalization) void this.applyGainFor(t)
    if (autoplay) {
      this.startPlayback()
    }
    this.preloadNext()
    this.emit()
  }

  private preloadNext() {
    const ni = this.snap.upNext[0]
    if (ni === undefined || ni < 0 || ni >= this.tracks.length) {
      if (this.preloadedSrc) this.release(this.preload)
      this.preloadedSrc = ''
      return
    }
    const src = this.resolveSrc(this.tracks[ni], 0)
    if (src === this.preloadedSrc) return
    this.preloadedSrc = src
    this.preload.src = src
    this.preload.load()
    this.preload.volume = this.volume
  }

  play() {
    if (this.getState().current) {
      const retrying = this.retryTimer !== null || this.awaitingNetwork
      if (this.loadedEntry !== this.currentEntry() || this.finished || !this.active.src) this.loadCurrent(true)
      else if (retrying || this.sourceLost()) {
        // Pressing play is a request to try now, whatever the backoff says.
        this.clearRetry()
        this.reattach(this.currentTimeMs, true)
      }
      else {
        this.startPlayback()
      }
    }
    this.emit()
  }

  private startPlayback() {
    this.playing = true
    const request = ++this.playRequest
    this.armStall()
    const started = this.active.play()
    void started?.catch((err: unknown) => {
      if (request !== this.playRequest || !this.playing) return
      const name = (err as { name?: string })?.name
      if (name === 'NotAllowedError' || name === 'AbortError') {
        this.loading = false
        this.pause()
      } else {
        this.onError()
      }
    })
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
    this.playRequest++
    this.playing = autoplay
    this.loading = true
    const at = Math.max(0, ms)
    const target = this.cropWindow().start + at
    const backendSeek = target > 0 && needsBackendSeek(t)
    this.active.src = this.resolveSrc(t, backendSeek ? target : 0)
    this.active.load()
    this.applyVolume()
    // Subsonic's timeOffset is in whole seconds. Seek the remaining fraction
    // inside the returned MP3 so the clock and crop stay aligned to the file.
    this.seekBaseMs = backendSeek ? Math.floor(target / 1000) * 1000 : 0
    // A browser-seekable source is put back by position once it has metadata;
    // assigning now would be dropped.
    this.pendingSeekMs = target > this.seekBaseMs ? target - this.seekBaseMs : -1
    this.currentTimeMs = at
    this.bufferedMs = 0
    this.lastRawMs = -1
    this.playedToMs = 0
    // Resuming partway in is a seek: what the element reports about its length
    // from here is read from the seek target, not from the file.
    this.seekedSinceLoad = target > 0
    if (autoplay) {
      this.startPlayback()
    }
    this.emit()
  }

  pause() {
    // A retry scheduled for a track the listener has just paused would restart
    // it under them; play() arranges a fresh one.
    this.clearRetry()
    this.clearStall()
    this.playRequest++
    this.playing = false
    this.active.pause()
    this.emit()
  }

  toggle() {
    if (this.playing) this.pause()
    else this.play()
  }

  private finishPlayback() {
    this.finished = true
    this.loading = false
    this.currentTimeMs = this.durationMs
    this.pause()
  }

  /** Seeks within the cropped window; ms is relative to the crop start. */
  seekMs(ms: number) {
    if (!Number.isFinite(ms) || !this.getState().current) return
    if (this.loadedEntry !== this.currentEntry()) this.loadCurrent(false)
    this.finished = false
    this.pendingSeekMs = -1
    const retrying = this.retryTimer !== null || this.awaitingNetwork
    this.clearRetry()
    const clamped = Math.max(0, this.durationMs > 0 ? Math.min(ms, this.durationMs) : ms)
    const target = this.cropWindow().start + clamped
    const t = this.getState().current
    if (t && (needsBackendSeek(t) || retrying || this.sourceLost())) {
      // The browser cannot find this position in the file, so the stream is
      // re-opened at it instead. The element then plays from zero and its clock
      // is read through seekBaseMs. Seeking back to the start re-opens the whole
      // file the same way — the loaded source is a fragment, so its own zero is
      // wherever the last seek landed, not the track's beginning.
      this.reattach(clamped, this.playing)
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

}

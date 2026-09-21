import { describe, expect, it, afterEach, beforeEach, vi } from 'vitest'
import { AudioEngine, type AudioElement } from './audioEngine'
import type { Track } from './types'
import { FakeQueue } from '../test/fakeQueue'

/**
 * The engine with a queue to play: FakeQueue stands in for the core, and these
 * methods drive the engine the way the player store does — send the listener's
 * action to the queue, then apply its answer. The queue's own behaviour is
 * covered by the core's tests (internal/api/player_test.go).
 */
class TestEngine extends AudioEngine {
  q = new FakeQueue()

  constructor(...args: ConstructorParameters<typeof AudioEngine>) {
    super(...args)
    this.setQueueHandler({
      ended: (id) => {
        if (id === this.q.current()) this.q.ended()
        this.apply(this.q.state(), { autoplay: true, fromEnded: true })
      },
      skip: (id) => {
        if (id === this.q.current()) this.q.next()
        this.apply(this.q.state(), { autoplay: true })
      },
    })
  }

  private keepPlaying() {
    return { autoplay: this.getState().playing }
  }
  playTrackList(tracks: Track[], index: number) {
    this.q.play(tracks, index)
    this.apply(this.q.state(), { autoplay: true })
  }
  enqueue(t: Track) {
    this.q.enqueue([t])
    this.apply(this.q.state())
  }
  removeAt(i: number) {
    this.q.remove([i])
    this.apply(this.q.state(), this.keepPlaying())
  }
  next() {
    this.q.next()
    this.apply(this.q.state(), this.keepPlaying())
  }
  prev() {
    this.q.previous()
    this.apply(this.q.state(), this.keepPlaying())
  }
  clear() {
    this.q.clear()
    this.apply(this.q.state())
  }
  cycleRepeat() {
    this.q.repeat = this.q.repeat === 'off' ? 'all' : this.q.repeat === 'all' ? 'one' : 'off'
    this.q.revision++
    this.apply(this.q.state())
  }
}

function track(id: string): Track {
  return {
    id,
    title: 'T' + id,
    albumId: 'al',
    album: 'Album',
    artistId: 'ar',
    artist: 'Artist',
    coverArtId: 'co',
    trackNumber: 1,
    discNumber: 1,
    durationMs: 1000,
    bitRate: 320,
    suffix: 'mp3',
    contentType: 'audio/mpeg',
  }
}

// fakeAudio is a minimal AudioElement stub: records play/pause, fires ended on demand.
class FakeAudio implements AudioElement {
  src = ''
  readyState = 4
  error: { code: number } | null = null
  currentTime = 0
  duration = 0
  volume = 1
  paused = true
  private listeners: Record<string, Array<() => void>> = {}
  buffered = { length: 0, end: () => 0, start: () => 0 }
  async play() {
    this.paused = false
  }
  pause() {
    this.paused = true
  }
  load() {}
  addEventListener(type: string, cb: () => void) {
    ;(this.listeners[type] ||= []).push(cb)
  }
  removeEventListener(type: string, cb: () => void) {
    this.listeners[type] = (this.listeners[type] || []).filter((f) => f !== cb)
  }
  fire(type: string) {
    ;(this.listeners[type] || []).forEach((cb) => cb())
  }
}

function newEngine(measured: (trackId: string) => Promise<number | null> = async () => null) {
  const audios: FakeAudio[] = []
  const engine = new TestEngine(() => {
    const a = new FakeAudio()
    audios.push(a)
    return a
  }, (t) => `mock://${t.id}`, async () => null, measured)
  return { engine, audios }
}

const list = [track('1'), track('2'), track('3')]

// Model the resource reset and play event as well as the clock. A play event
// means playback was requested; it does not prove that any audio was decoded.
class LifecycleAudio extends FakeAudio {
  override load() {
    this.currentTime = 0
    this.duration = 0
    this.paused = true
    this.error = null
  }
  override async play() {
    this.paused = false
    this.fire('play')
  }
}

describe('AudioEngine playback lifecycle regressions', () => {
  let engine: TestEngine
  let audios: LifecycleAudio[]
  beforeEach(() => {
    vi.useFakeTimers()
    audios = []
    engine = new TestEngine(() => {
      const a = new LifecycleAudio()
      audios.push(a)
      return a
    }, (t, ms) => `mock://${t.id}?t=${ms}`, async () => null, async () => null)
  })
  afterEach(() => {
    engine.clear()
    vi.useRealTimers()
  })

  it.each(['remove', 'replace'] as const)('stops the audio when %s empties the queue', (action) => {
    engine.playTrackList([track('1')], 0)
    if (action === 'remove') engine.removeAt(0)
    else engine.playTrackList([], 0)
    expect(engine.getState().current).toBeNull()
    expect(audios[0].paused).toBe(true)
    expect(engine.getState().loading).toBe(false)
  })

  it('loads the newly queued track after clearing the previous queue', () => {
    engine.playTrackList(list, 0)
    audios[0].currentTime = 0.5
    audios[0].fire('timeupdate')
    engine.clear()
    expect(engine.getState().currentTimeMs).toBe(0)
    expect(engine.getState().durationMs).toBe(0)
    engine.enqueue(track('new'))
    engine.play()
    expect(audios[0].src).toBe('mock://new?t=0')
  })

  it('stops audible playback at the final crop end and can replay from its start', () => {
    engine.playTrackList([{ ...track('1'), durationMs: 30000, cropStartMs: 5000, cropEndMs: 10000 }], 0)
    audios[0].currentTime = 10
    audios[0].fire('timeupdate')
    expect(engine.getState().playing).toBe(false)
    expect(audios[0].paused).toBe(true)
    engine.play()
    expect(audios[0].currentTime).toBe(5)
    expect(engine.getState().currentTimeMs).toBe(0)
  })

  it('repeat one restarts the whole track after a backend seek', () => {
    engine.playTrackList([{ ...track('1'), durationMs: 30000, suffix: 'opus', contentType: 'audio/ogg' }], 0)
    engine.cycleRepeat()
    engine.cycleRepeat()
    engine.seekMs(20000)
    audios[0].fire('ended')
    expect(audios[0].src).toBe('mock://1?t=0')
    audios[0].currentTime = 1
    audios[0].fire('timeupdate')
    expect(engine.getState().currentTimeMs).toBe(1000)
  })

  it('does not let a delayed error resume or skip a paused track, even with repeat one', () => {
    engine.playTrackList(list, 0)
    engine.cycleRepeat()
    engine.cycleRepeat()
    engine.pause()
    audios[0].error = { code: 3 }
    audios[0].fire('error')
    expect(audios[0].paused).toBe(true)
    expect(engine.getState().playing).toBe(false)
    expect(engine.getState().index).toBe(0)
  })

  it('bounds failed retries even when every play request fires a play event', () => {
    engine.playTrackList(list, 0)
    for (let i = 0; i < 24 && engine.getState().playing; i++) {
      audios[0].error = { code: 3 }
      audios[0].fire('error')
    }
    expect(engine.getState().playing).toBe(false)
  })

  it('cancels a scheduled reload once audio starts flowing again', () => {
    engine.playTrackList([{ ...track('1'), durationMs: 30000 }], 0)
    audios[0].error = { code: 2 }
    audios[0].fire('error')
    const reload = vi.spyOn(audios[0], 'load')
    audios[0].error = null
    audios[0].currentTime = 1
    audios[0].fire('timeupdate')
    audios[0].currentTime = 1.25
    audios[0].fire('timeupdate')
    vi.advanceTimersByTime(1000)
    expect(reload).not.toHaveBeenCalled()
  })

  it('recovers an initial load that never emits waiting or error', () => {
    engine.playTrackList(list, 0)
    const reload = vi.spyOn(audios[0], 'load')
    vi.advanceTimersByTime(21000)
    expect(reload).toHaveBeenCalled()
  })

  it('reports an autoplay refusal as paused instead of playing forever', async () => {
    const rejected = Promise.reject(new DOMException('blocked', 'NotAllowedError'))
    void rejected.catch(() => {})
    vi.spyOn(audios[0], 'play').mockReturnValueOnce(rejected)
    engine.playTrackList(list, 0)
    await Promise.resolve()
    expect(engine.getState().playing).toBe(false)
    expect(engine.getState().loading).toBe(false)
  })

  it('ignores a previous play rejection after skipping to a new source', async () => {
    engine.playTrackList(list, 0)
    engine.pause()
    let reject!: (error: Error) => void
    const pending = new Promise<void>((_, no) => { reject = no })
    void pending.catch(() => {})
    vi.spyOn(audios[0], 'play').mockReturnValueOnce(pending)
    engine.play()
    engine.next()
    const reload = vi.spyOn(audios[0], 'load')
    reject(new DOMException('source replaced', 'AbortError'))
    await Promise.resolve()
    expect(reload).not.toHaveBeenCalled()
    expect(engine.getState().current?.id).toBe('2')
  })

  it('keeps the recovery position while a reloaded stream reports zero before metadata', () => {
    engine.playTrackList([{ ...track('1'), durationMs: 60000 }], 0)
    audios[0].currentTime = 20
    audios[0].fire('timeupdate')
    audios[0].error = { code: 2 }
    audios[0].fire('error')
    vi.advanceTimersByTime(1000)
    audios[0].fire('timeupdate')
    expect(engine.getState().currentTimeMs).toBe(20000)
    audios[0].fire('canplay')
    expect(audios[0].currentTime).toBe(20)
  })

  it('still advances when the browser pauses at EOF before dispatching ended', () => {
    engine.playTrackList(list, 0)
    audios[0].fire('canplay')
    Object.assign(audios[0], { paused: true, ended: true })
    audios[0].fire('pause')
    audios[0].fire('ended')
    expect(engine.getState().index).toBe(1)
    expect(engine.getState().playing).toBe(true)
  })

  it('does not defer recovery forever when a stuck source keeps firing waiting', () => {
    engine.playTrackList(list, 0)
    const reload = vi.spyOn(audios[0], 'load')
    for (let i = 0; i < 21; i++) {
      audios[0].fire('waiting')
      vi.advanceTimersByTime(1000)
    }
    expect(reload).toHaveBeenCalled()
  })

  it('does not count a fragment clock reset as successful network recovery', () => {
    engine.playTrackList([{ ...track('1'), durationMs: 60000, suffix: 'opus', contentType: 'audio/ogg' }, track('2')], 0)
    engine.seekMs(20000)
    for (let i = 0; i < 8 && engine.getState().index === 0; i++) {
      audios[0].error = { code: 2 }
      audios[0].fire('error')
      vi.advanceTimersByTime(30000)
      audios[0].fire('timeupdate')
    }
    expect(engine.getState().index).toBe(1)
  })

  it('skips a corrupt local file while offline instead of waiting for internet', () => {
    const online = vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(false)
    try {
      engine.playTrackList(list, 0)
      for (let i = 0; i < 2; i++) {
        audios[0].error = { code: 3 }
        audios[0].fire('error')
      }
      expect(engine.getState().index).toBe(1)
    } finally {
      online.mockRestore()
    }
  })

  it('uses a backend seek for the initial crop of an Opus file', () => {
    engine.playTrackList([{ ...track('1'), durationMs: 60000, cropStartMs: 10000, suffix: 'opus', contentType: 'audio/ogg' }], 0)
    expect(audios[0].src).toBe('mock://1?t=10000')
    audios[0].currentTime = 1
    audios[0].fire('timeupdate')
    expect(engine.getState().currentTimeMs).toBe(1000)
  })

  it('accounts for the backend rounding seek offsets down to whole seconds', () => {
    engine.playTrackList([{ ...track('1'), durationMs: 60000, suffix: 'opus', contentType: 'audio/ogg' }], 0)
    engine.seekMs(20500)
    audios[0].fire('canplay')
    expect(audios[0].currentTime).toBe(0.5)
    audios[0].fire('timeupdate')
    expect(engine.getState().currentTimeMs).toBe(20500)
  })

  it('stops a lone failed track with repeat all without restarting its audio', () => {
    engine.playTrackList([track('1')], 0)
    engine.cycleRepeat()
    audios[0].fire('error')
    audios[0].fire('error')
    expect(engine.getState().playing).toBe(false)
    expect(audios[0].paused).toBe(true)
  })

  it('never publishes the previous track position as part of a new playlist', () => {
    engine.playTrackList([{ ...track('old'), durationMs: 60000 }], 0)
    audios[0].currentTime = 20
    audios[0].fire('timeupdate')
    const positions: number[] = []
    engine.subscribe((s) => {
      if (s.current?.id === 'new') positions.push(s.currentTimeMs)
    })
    engine.playTrackList([{ ...track('new'), durationMs: 40000 }], 0)
    expect(positions.length).toBeGreaterThan(0)
    expect(positions.every((ms) => ms === 0)).toBe(true)
  })

  it('seeking during a network retry reopens the source at the requested position', () => {
    engine.playTrackList([{ ...track('1'), durationMs: 60000 }], 0)
    audios[0].error = { code: 2 }
    audios[0].fire('error')
    const reload = vi.spyOn(audios[0], 'load')
    engine.seekMs(10000)
    expect(reload).toHaveBeenCalledOnce()
    audios[0].fire('canplay')
    expect(audios[0].currentTime).toBe(10)
    expect(engine.getState().playing).toBe(true)
  })
})

// The queue itself (next, previous, repeat, shuffle, jumping, reordering, up
// next) belongs to the core and is covered by internal/api/player_test.go.
// What stays here is how the engine plays the core's answers.
describe('AudioEngine playing the core queue', () => {
  let engine: TestEngine
  let audios: FakeAudio[]
  beforeEach(() => {
    ;({ engine, audios } = newEngine())
  })

  it('plays the current entry of a new list', () => {
    engine.playTrackList(list, 1)
    const s = engine.getState()
    expect(s.index).toBe(1)
    expect(s.current?.id).toBe('2')
    expect(s.playing).toBe(true)
    expect(audios[0].src).toBe('mock://2')
  })

  it('keeps playing through an answer that does not restart the current entry', () => {
    engine.playTrackList(list, 0)
    audios[0].currentTime = 5
    audios[0].fire('timeupdate')
    engine.enqueue(track('4'))
    expect(engine.getState().currentTimeMs).toBe(5000)
    expect(engine.getState().queue).toHaveLength(4)
  })

  it('restarts the current entry when the core says so, even if it is the same track', () => {
    engine.playTrackList(list, 0)
    engine.cycleRepeat()
    engine.cycleRepeat() // one
    audios[0].currentTime = 0.9
    audios[0].fire('timeupdate')
    audios[0].fire('ended')
    expect(engine.getState().index).toBe(0)
    expect(engine.getState().currentTimeMs).toBe(0)
    expect(engine.getState().playing).toBe(true)
  })

  it('moves on when the current track ends', () => {
    engine.playTrackList(list, 0)
    audios[0].fire('ended')
    expect(engine.getState().index).toBe(1)
    expect(audios[0].src).toBe('mock://2')
  })

  it('reports an end once, and only for the entry it was playing', () => {
    const ended: string[] = []
    engine.setQueueHandler({ ended: (id) => ended.push(id), skip: () => {} })
    engine.playTrackList(list, 0)
    audios[0].fire('ended')
    audios[0].fire('ended')
    expect(ended).toEqual([engine.q.state().entries[0].id])
  })

  it('stops when the core says the queue is finished', () => {
    engine.playTrackList(list, 2)
    audios[0].fire('ended')
    const s = engine.getState()
    expect(s.index).toBe(2)
    expect(s.playing).toBe(false)
    engine.play()
    expect(engine.getState().playing).toBe(true)
    expect(engine.getState().currentTimeMs).toBe(0)
  })

  it('preloads what the core says is up next', () => {
    engine.playTrackList(list, 0)
    expect(audios[1].src).toBe('mock://2')
    engine.q.move(2, 1)
    engine.apply(engine.q.state())
    expect(audios[1].src).toBe('mock://3')
  })

  it('resumes where it was after the queue is shown again', () => {
    engine.playTrackList(list, 0)
    audios[0].currentTime = 12
    audios[0].fire('timeupdate')
    engine.pause()
    // A fresh copy of the same queue: new objects, same entries and playId.
    engine.apply(engine.q.state())
    const reload = vi.spyOn(audios[0], 'load')
    engine.play()
    expect(reload).not.toHaveBeenCalled()
    expect(engine.getState().currentTimeMs).toBe(12000)
  })

  it('unloads when the queue empties', () => {
    engine.playTrackList(list, 0)
    engine.clear()
    expect(engine.getState().current).toBeNull()
    expect(engine.getState().playing).toBe(false)
    expect(audios[0].paused).toBe(true)
  })

  it('setVolume clamps 0..1 and notifies subscribers', () => {
    let notified = 0
    engine.subscribe(() => notified++)
    engine.setVolume(2)
    expect(engine.getState().volume).toBe(1)
    engine.setVolume(-1)
    expect(engine.getState().volume).toBe(0)
    expect(notified).toBeGreaterThan(0)
  })
})

describe('AudioEngine stream-error recovery', () => {
  let engine: TestEngine
  let audios: FakeAudio[]
  beforeEach(() => {
    ;({ engine, audios } = newEngine())
  })

  it('repeat=one + active error: stays on same track, does not infinite-loop', () => {
    engine.playTrackList(list, 1)
    engine.cycleRepeat() // off -> all
    engine.cycleRepeat() // all -> one
    expect(engine.getState().repeat).toBe('one')
    const indexBefore = engine.getState().index

    audios[0].fire('error')

    // index must NOT advance
    expect(engine.getState().index).toBe(indexBefore)
    // second error (simulating reload also failed) should stop, not loop
    audios[0].fire('error')
    expect(engine.getState().playing).toBe(false)
    expect(engine.getState().index).toBe(indexBefore)
  })

  it('repeat!==one + active error: re-attaches once, then skips to next track', () => {
    engine.playTrackList(list, 0)
    expect(engine.getState().repeat).toBe('off')

    audios[0].fire('error') // first failure re-attaches the same track
    expect(engine.getState().index).toBe(0)
    audios[0].fire('error')

    expect(engine.getState().index).toBe(1)
  })

  it('three consecutive active errors (no successful play between): engine stops instead of skipping indefinitely', () => {
    engine.playTrackList(list, 0)

    audios[0].fire('error') // re-attach attempt on index 0
    audios[0].fire('error') // consecutiveErrors=1, skips to index 1
    audios[0].fire('error') // re-attach attempt on index 1
    audios[0].fire('error') // consecutiveErrors=2, skips to index 2
    audios[0].fire('error') // re-attach attempt on index 2
    audios[0].fire('error') // consecutiveErrors=3, should STOP

    expect(engine.getState().playing).toBe(false)
  })

  it('successful playback resets the error counter before another track fails', () => {
    const longList = [track('a'), track('b'), track('c'), track('d'), track('e')]
    engine.playTrackList(longList, 0)

    audios[0].fire('error') // re-attach, then
    audios[0].fire('error') // consecutiveErrors=1, skips → index 1
    audios[0].fire('error') // re-attach, then
    audios[0].fire('error') // consecutiveErrors=2, skips → index 2

    // successful play resets counter
    audios[0].paused = false
    audios[0].fire('playing')

    // one more error → re-attach, then consecutiveErrors=1, should skip not stop
    audios[0].fire('error')
    audios[0].fire('error')

    // should have advanced (not stopped) since counter reset
    expect(engine.getState().index).toBe(3)
    expect(engine.getState().playing).toBe(true) // still playing (skipped to next)
  })
})

// The default resolver is what decides where audio comes from. A track carrying
// externalStream isn't in the library, so the library stream endpoint (which
// only knows library ids) would 404 — it must go to the external proxy instead.
describe('AudioEngine default source resolution', () => {
  function engineWithDefaultResolver() {
    const audios: FakeAudio[] = []
    const engine = new TestEngine(() => {
      const a = new FakeAudio()
      audios.push(a)
      return a
    })
    return { engine, audios }
  }

  it('streams a library track from the library endpoint', () => {
    const { engine, audios } = engineWithDefaultResolver()
    engine.playTrackList([track('lib-1')], 0)
    expect(audios[0].src).toContain('/api/v1/stream/lib-1')
  })

  it('streams an external track from the external proxy', () => {
    const { engine, audios } = engineWithDefaultResolver()
    const t = { ...track('deezer:dz-1'), externalStream: { source: 'deezer', externalId: 'dz-1' } }
    engine.playTrackList([t], 0)
    expect(audios[0].src).toContain('/api/v1/external/stream/deezer/dz-1')
  })
})

// An external track has no audio until the server has resolved it to a source,
// which takes seconds. Without a loading flag the UI has nothing to show and
// looks frozen.
describe('AudioEngine loading state', () => {
  it('is loading from the moment a track is loaded until it can play', () => {
    const { engine, audios } = newEngine()
    expect(engine.getState().loading).toBe(false)
    engine.playTrackList([track('1')], 0)
    expect(engine.getState().loading).toBe(true)
    audios[0].fire('canplay')
    expect(engine.getState().loading).toBe(false)
  })

  it('returns to loading when playback stalls mid-track', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([track('1')], 0)
    audios[0].fire('canplay')
    audios[0].fire('waiting')
    expect(engine.getState().loading).toBe(true)
    audios[0].fire('playing')
    expect(engine.getState().loading).toBe(false)
  })

  // A resolve that fails must not leave the spinner up forever. The first
  // failure re-attaches the source, which is itself a load; the second is the
  // one that gives up.
  it('clears loading once a source has failed for good', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([track('1')], 0)
    expect(engine.getState().loading).toBe(true)
    audios[0].fire('error')
    expect(engine.getState().loading).toBe(true)
    audios[0].fire('error')
    expect(engine.getState().loading).toBe(false)
  })

  // Seeking a proxied external stream tears down and re-opens the upstream
  // connection, which fires 'stalled' even though the buffer keeps feeding the
  // element. Playback simply continues, so no 'canplay'/'playing' follows and
  // only the advancing clock can bring the spinner back down.
  it('clears loading when the clock keeps advancing after a stall', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([track('1')], 0)
    audios[0].fire('canplay')
    audios[0].paused = false
    audios[0].fire('stalled')
    expect(engine.getState().loading).toBe(true)

    audios[0].currentTime = 42
    audios[0].fire('timeupdate')
    expect(engine.getState().loading).toBe(false)
  })

  it('notifies subscribers when loading changes', () => {
    const { engine, audios } = newEngine()
    const seen: boolean[] = []
    engine.subscribe((s) => seen.push(s.loading))
    engine.playTrackList([track('1')], 0)
    audios[0].fire('canplay')
    expect(seen).toContain(true)
    expect(seen[seen.length - 1]).toBe(false)
  })
})

// ── Loudness normalization ───────────────────────────────────────────────────
// The gain is folded into the media element's own volume at playback time; the
// file itself is never touched, so switching this off is instant.
describe('AudioEngine normalization', () => {
  function normEngine(gains: Record<string, number | null>) {
    const audios: FakeAudio[] = []
    const fetched: string[] = []
    const engine = new TestEngine(
      () => {
        const a = new FakeAudio()
        audios.push(a)
        return a
      },
      (t) => `mock://${t.id}`,
      async (id) => {
        fetched.push(id)
        return gains[id] ?? null
      },
    )
    return { engine, audios, fetched }
  }

  it('applies the measured gain for the playing track', async () => {
    const { engine, audios } = normEngine({ '1': -6 })
    engine.setNormalization(true)
    engine.playTrackList([track('1')], 0)
    await vi.waitFor(() => expect(audios[0].volume).toBeCloseTo(Math.pow(10, -6 / 20), 5))
  })

  // The element cannot go above unity, so a boost is limited by the headroom
  // the volume slider has left — and must never be clipped to silence.
  it('takes a boost only as far as the remaining headroom allows', async () => {
    const { engine, audios } = normEngine({ '1': 6 })
    engine.setNormalization(true)
    engine.setVolume(0.5)
    engine.playTrackList([track('1')], 0)
    await vi.waitFor(() => expect(audios[0].volume).toBeCloseTo(0.5 * Math.pow(10, 6 / 20), 5))
    engine.setVolume(1)
    expect(audios[0].volume).toBe(1)
  })

  // A track with no measurement plays untouched rather than at some guessed level.
  it('leaves the level untouched when the server has no measurement', async () => {
    const { engine, audios } = normEngine({ '1': null })
    engine.setNormalization(true)
    engine.playTrackList([track('1')], 0)
    await vi.waitFor(() => expect(audios[0].volume).toBe(1))
  })

  it('does not measure anything while normalization is off', async () => {
    const { engine, fetched } = normEngine({ '1': 6 })
    engine.playTrackList([track('1')], 0)
    await Promise.resolve()
    expect(fetched).toEqual([])
  })

  it('restores the plain volume the moment normalization is switched off', async () => {
    const { engine, audios } = normEngine({ '1': -6 })
    engine.setNormalization(true)
    engine.playTrackList([track('1')], 0)
    await vi.waitFor(() => expect(audios[0].volume).toBeLessThan(1))
    engine.setNormalization(false)
    expect(audios[0].volume).toBe(1)
  })

  // Gain belongs to the track that measured it: the next track starts at unity
  // and settles to its own level.
  it('does not leak a track gain into the next track', async () => {
    const { engine, audios } = normEngine({ '1': -12, '2': null })
    engine.setNormalization(true)
    engine.playTrackList([track('1'), track('2')], 0)
    await vi.waitFor(() => expect(audios[0].volume).toBeLessThan(1))
    engine.next()
    expect(audios[0].volume).toBe(1)
  })

  // Measuring runs ffmpeg server-side on first play, so it must be cached —
  // re-measuring on every replay would stall the track each time.
  it('measures each track once', async () => {
    const { engine, fetched } = normEngine({ '1': 3, '2': 3 })
    engine.setNormalization(true)
    engine.playTrackList([track('1'), track('2')], 0)
    await vi.waitFor(() => expect(fetched).toEqual(['1']))
    engine.next()
    await vi.waitFor(() => expect(fetched).toEqual(['1', '2']))
    engine.prev()
    await vi.waitFor(() => expect(fetched).toEqual(['1', '2']))
  })
})

// ── Cropping ────────────────────────────────────────────────────────────────
// A crop is a playback window over an untouched file, so the engine plays the
// window and reports times relative to it.
describe('AudioEngine crop', () => {
  // Playing through: 'timeupdate' ticks a quarter-second at a time, which is
  // what separates played audio from a seek.
  function playThrough(audio: FakeAudio, fromSec: number, toSec: number) {
    for (let t = fromSec; t < toSec; t += 0.25) {
      audio.currentTime = t
      audio.fire('timeupdate')
    }
    audio.currentTime = toSec
    audio.fire('timeupdate')
  }

  function cropped(overrides: Partial<Track>): Track {
    return { ...track('1'), durationMs: 200000, ...overrides }
  }

  it('starts playback at the crop start', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([cropped({ cropStartMs: 30000 })], 0)
    expect(audios[0].currentTime).toBe(30)
  })

  it('reports position and duration relative to the crop window', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([cropped({ cropStartMs: 30000, cropEndMs: 90000 })], 0)
    audios[0].duration = 200
    audios[0].currentTime = 45
    audios[0].fire('timeupdate')
    expect(engine.getState().currentTimeMs).toBe(15000)
    expect(engine.getState().durationMs).toBe(60000)
  })

  it('seeks relative to the crop start', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([cropped({ cropStartMs: 30000, cropEndMs: 90000 })], 0)
    audios[0].duration = 200
    audios[0].fire('timeupdate')
    engine.seekMs(10000)
    expect(audios[0].currentTime).toBe(40)
  })

  // Reaching the crop end is the end of the track, so the queue advances.
  it('advances at the crop end instead of playing the trimmed outro', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([cropped({ cropEndMs: 60000 }), track('2')], 0)
    audios[0].duration = 200
    audios[0].currentTime = 61
    audios[0].fire('timeupdate')
    expect(engine.getState().current?.id).toBe('2')
  })

  // The file still holds the trimmed intro, so playback that lands before the
  // start must jump forward rather than let it through.
  it('skips forward when playback lands before the crop start', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([cropped({ cropStartMs: 30000 })], 0)
    audios[0].duration = 200
    audios[0].currentTime = 0
    audios[0].fire('timeupdate')
    expect(audios[0].currentTime).toBe(30)
    expect(engine.getState().currentTimeMs).toBe(0)
  })

  // A stream that declares no length makes the browser revise `duration` as it
  // buffers. Every revision is a lower bound, so the readout keeps the longest
  // one instead of following the drift back down.
  it('never shrinks the duration as the element revises its own', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([cropped({})], 0)
    expect(engine.getState().durationMs).toBe(200000)
    for (const [d, expected] of [[252, 252000], [247, 252000], [269, 269000]]) {
      audios[0].duration = d
      audios[0].fire('durationchange')
      expect(engine.getState().durationMs).toBe(expected)
    }
  })

  // A tag that understates a VBR file is disproved by playing past it.
  it('extends the duration when playback passes the claimed end', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([cropped({ durationMs: 239000 })], 0)
    expect(engine.getState().durationMs).toBe(239000)
    playThrough(audios[0], 238, 247)
    expect(engine.getState().durationMs).toBe(247000)
  })

  // The latch belongs to one loaded track, not to the engine.
  it('resets the latched duration on the next track', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([cropped({}), cropped({ id: '2', durationMs: 90000 })], 0)
    audios[0].duration = 300
    audios[0].fire('durationchange')
    expect(engine.getState().durationMs).toBe(300000)
    engine.next()
    expect(engine.getState().durationMs).toBe(90000)
  })

  // The server decodes the file, so its length is the one that cannot disagree
  // with what is heard — it replaces the tag's claim in either direction.
  it('takes the measured length over a tag that overstates the file', async () => {
    const { engine } = newEngine(async () => 180000)
    engine.playTrackList([cropped({ durationMs: 391000 })], 0)
    expect(engine.getState().durationMs).toBe(391000)
    await vi.waitFor(() => expect(engine.getState().durationMs).toBe(180000))
  })

  it('takes the measured length over a tag that understates the file', async () => {
    const { engine } = newEngine(async () => 247000)
    engine.playTrackList([cropped({ durationMs: 239000 })], 0)
    await vi.waitFor(() => expect(engine.getState().durationMs).toBe(247000))
  })

  // The measurement covers the whole file; the rail spans the crop window.
  it('reports the measured length relative to the crop start', async () => {
    const { engine } = newEngine(async () => 180000)
    engine.playTrackList([cropped({ cropStartMs: 30000 })], 0)
    await vi.waitFor(() => expect(engine.getState().durationMs).toBe(150000))
  })

  // A measurement can still fall short of what the browser plays: ffmpeg trims
  // an MP3's encoder padding, a trailing tag can decode as a moment of audio.
  // The clock passing it is proof, and the readout has to follow.
  it('extends past the measured length when playback passes it', async () => {
    const { engine, audios } = newEngine(async () => 180000)
    engine.playTrackList([cropped({})], 0)
    await vi.waitFor(() => expect(engine.getState().durationMs).toBe(180000))
    playThrough(audios[0], 179.5, 180.4)
    expect(engine.getState().durationMs).toBe(180400)
  })

  // Seeking is offered up to the length shown, so a seek that lands near the end
  // of an already-too-long bar must not be able to cite that bar as evidence and
  // latch it — only played-through audio counts.
  it('does not treat a seek destination as proof of the length', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([cropped({ durationMs: 200000 })], 0)
    audios[0].duration = 269
    audios[0].fire('durationchange')
    expect(engine.getState().durationMs).toBe(269000)

    // Click near the end of the (inflated) rail, then let it play on from there.
    engine.seekMs(265000)
    playThrough(audios[0], 265, 266)
    expect(engine.getState().durationMs).toBe(269000)
  })

  // Ogg/Opus is what a downloaded track usually is, and a browser cannot find a
  // position in one: it maps the time onto a byte offset through an assumed
  // bitrate and lands seconds away. The backend is asked to start the audio at
  // the position instead, and the element's clock is read from there.
  describe('backend seeking', () => {
    function opusEngine() {
      const audios: FakeAudio[] = []
      const starts: number[] = []
      const engine = new TestEngine(
        () => {
          const a = new FakeAudio()
          audios.push(a)
          return a
        },
        (t, startMs) => {
          starts.push(startMs)
          return `mock://${t.id}?t=${startMs}`
        },
        async () => null,
        async () => 137853,
      )
      return { engine, audios, starts }
    }

    const opus = (o: Partial<Track> = {}): Track => ({
      ...track('1'),
      durationMs: 137853,
      suffix: 'opus',
      contentType: 'audio/ogg',
      ...o,
    })

    it('re-opens the stream at the position instead of seeking in place', () => {
      const { engine, audios, starts } = opusEngine()
      engine.playTrackList([opus()], 0)
      engine.seekMs(130000)
      expect(starts).toContain(130000)
      expect(audios[0].src).toBe('mock://1?t=130000')
      expect(audios[0].currentTime).toBe(0)
    })

    it('reports the position from the point the stream was re-opened at', () => {
      const { engine, audios } = opusEngine()
      engine.playTrackList([opus()], 0)
      engine.seekMs(130000)
      audios[0].currentTime = 4
      audios[0].fire('timeupdate')
      expect(engine.getState().currentTimeMs).toBe(134000)
    })

    it('offsets the re-opened stream by the crop start', () => {
      const { engine, starts } = opusEngine()
      engine.playTrackList([opus({ cropStartMs: 30000 })], 0)
      engine.seekMs(10000)
      expect(starts).toContain(40000)
    })

    // The loaded source is a fragment, so its own zero is wherever the last seek
    // landed — going back to the start has to re-open the whole file.
    it('re-opens the whole file when seeking back to the start', () => {
      const { engine, audios } = opusEngine()
      engine.playTrackList([opus()], 0)
      engine.seekMs(130000)
      engine.seekMs(0)
      expect(audios[0].src).toBe('mock://1?t=0')
      audios[0].currentTime = 2
      audios[0].fire('timeupdate')
      expect(engine.getState().currentTimeMs).toBe(2000)
    })

    it('keeps playing across a seek that re-opens the stream', () => {
      const { engine, audios } = opusEngine()
      engine.playTrackList([opus()], 0)
      engine.seekMs(130000)
      expect(audios[0].paused).toBe(false)
    })

    // A format the browser can index seeks in place: no re-fetch, no transcode.
    it('seeks an mp3 in place', () => {
      const { engine, audios, starts } = opusEngine()
      engine.playTrackList([opus({ suffix: 'mp3', contentType: 'audio/mpeg' })], 0)
      engine.seekMs(60000)
      expect(starts).toEqual([0])
      expect(audios[0].currentTime).toBe(60)
    })

    // The next track loads whole again.
    it('drops the offset on the next load', () => {
      const { engine, audios } = opusEngine()
      engine.playTrackList([opus(), opus({ id: '2' })], 0)
      engine.seekMs(130000)
      engine.next()
      expect(audios[0].src).toBe('mock://2?t=0')
      audios[0].currentTime = 3
      audios[0].fire('timeupdate')
      expect(engine.getState().currentTimeMs).toBe(3000)
    })
  })

  // Seeking a stream with no length is a byte-offset guess: the decoder lands
  // short of where the clock then claims to be, and the real audio still ahead
  // of it carries that clock past the end. Counting those positions measured
  // the seek's error, growing a 2:17 track to 2:30 as it played out.
  it('does not extend the length from playback after a seek', async () => {
    const { engine, audios } = newEngine(async () => 137000)
    engine.playTrackList([cropped({ durationMs: 137000 })], 0)
    await vi.waitFor(() => expect(engine.getState().durationMs).toBe(137000))

    engine.seekMs(130000)
    playThrough(audios[0], 130, 145)
    expect(engine.getState().durationMs).toBe(137000)
  })

  // And the position it reports cannot overrun the rail it is drawn on.
  it('pins a post-seek clock that runs past the end to the length', async () => {
    const { engine, audios } = newEngine(async () => 137000)
    engine.playTrackList([cropped({ durationMs: 137000 })], 0)
    await vi.waitFor(() => expect(engine.getState().durationMs).toBe(137000))

    engine.seekMs(130000)
    playThrough(audios[0], 130, 145)
    expect(engine.getState().currentTimeMs).toBe(137000)
  })

  // A browser estimating an unlabelled stream re-derives `duration` after a
  // seek, from wherever the decoder now is — which is the seek target, offered
  // out of the readout itself. Nothing about the file was learned, so the
  // readout must not move.
  it('ignores an element duration revised after a seek', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([cropped({ durationMs: 200000 })], 0)
    audios[0].duration = 200
    audios[0].fire('durationchange')

    engine.seekMs(195000)
    audios[0].duration = 269
    audios[0].fire('durationchange')
    expect(engine.getState().durationMs).toBe(200000)
  })

  // With nothing else to go on, a post-seek estimate still beats no readout.
  it('still takes the element duration after a seek when nothing is known', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([cropped({ durationMs: 0 })], 0)
    engine.seekMs(5000)
    audios[0].duration = 247
    audios[0].fire('durationchange')
    expect(engine.getState().durationMs).toBe(247000)
  })

  // The distrust belongs to one loaded track.
  it('trusts the element again on the next track', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([cropped({ durationMs: 200000 }), cropped({ id: '2', durationMs: 90000 })], 0)
    engine.seekMs(195000)
    engine.next()
    audios[0].duration = 269
    audios[0].fire('durationchange')
    expect(engine.getState().durationMs).toBe(269000)
  })

  // An external track has no file on disk to decode.
  it('does not ask for a measurement of an external track', () => {
    const asked: string[] = []
    const { engine } = newEngine(async (id) => { asked.push(id); return null })
    engine.playTrackList([{ ...cropped({}), externalStream: { source: 'deezer', externalId: 'x' } }], 0)
    expect(asked).toEqual([])
  })

  // Without any known length the element is all there is.
  it('falls back to the element duration when the track has none', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([cropped({ durationMs: 0 })], 0)
    audios[0].duration = 247
    audios[0].fire('durationchange')
    expect(engine.getState().durationMs).toBe(247000)
  })

  it('leaves an uncropped track alone', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([cropped({})], 0)
    audios[0].duration = 200
    audios[0].currentTime = 45
    audios[0].fire('timeupdate')
    expect(engine.getState().currentTimeMs).toBe(45000)
    expect(engine.getState().durationMs).toBe(200000)
  })
})

// ── Volume ──────────────────────────────────────────────────────────────────
// The engine's volume is the only truth: the slider shows it, so any element
// playing at some other level makes the first drag jump the output.
describe('AudioEngine volume', () => {
  it('pins both elements to the engine volume at construction', () => {
    const { audios } = newEngine()
    expect(audios.map((a) => a.volume)).toEqual([1, 1])
  })

  it('re-asserts the volume on the element when a new source is loaded', () => {
    const { engine, audios } = newEngine()
    engine.setVolume(0.4)
    // An element that comes up at its own default rather than the engine's.
    audios[0].volume = 1
    engine.playTrackList(list, 0)
    expect(audios[0].volume).toBe(0.4)
  })

  it('re-asserts the volume when the source becomes playable', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList(list, 0)
    engine.setVolume(0.3)
    audios[0].volume = 0.9
    audios[0].fire('canplay')
    expect(audios[0].volume).toBe(0.3)
  })

  it('keeps the preloaded next track at the same volume', () => {
    const { engine, audios } = newEngine()
    engine.setVolume(0.5)
    engine.playTrackList(list, 0)
    expect(audios[1].volume).toBe(0.5)
  })
})

// A track played from a search result is proxied from an upstream URL that
// expires, and a media element left paused for hours can have its resource
// dropped by the browser. Either way pressing play again must resume the track,
// not sit silent at 0:00.
describe('AudioEngine stale source recovery', () => {
  function external(id: string): Track {
    return { ...track(id), externalStream: { source: 'deezer', externalId: id } }
  }

  it('re-attaches and resumes when the element lost its resource while paused', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([external('x')], 0)
    const a = audios[0]

    // play through to 60s, then pause
    a.currentTime = 60
    a.fire('timeupdate')
    engine.pause()
    expect(engine.getState().currentTimeMs).toBe(60000)

    // hours later: the browser has released the media resource
    a.readyState = 0
    a.currentTime = 0
    const srcBefore = a.src
    a.src = ''
    a.src = srcBefore

    engine.play()

    expect(engine.getState().playing).toBe(true)
    expect(a.paused).toBe(false)
    expect(engine.getState().currentTimeMs).toBe(60000)

    // the position is applied once the fresh source can accept it
    a.readyState = 4
    a.fire('canplay')
    expect(a.currentTime).toBe(60)
  })

  it('recovers a mid-track error by re-attaching at the same position', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([external('x')], 0)
    const a = audios[0]
    a.currentTime = 30
    a.fire('timeupdate')

    a.fire('error')

    expect(engine.getState().index).toBe(0)
    expect(engine.getState().playing).toBe(true)
    expect(engine.getState().currentTimeMs).toBe(30000)
  })

  it('stops rather than staying "playing" when a lone dead track cannot be skipped', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([external('x')], 0)
    audios[0].fire('error') // re-attach
    audios[0].fire('error') // give up
    expect(engine.getState().playing).toBe(false)
  })
})

// Wi-Fi dropping out is not the track's fault: the player must wait it out and
// pick the track back up, whether the outage lasts seconds or hours.
describe('AudioEngine network interruptions', () => {
  function external(id: string): Track {
    return { ...track(id), externalStream: { source: 'deezer', externalId: id } }
  }
  function setOnline(v: boolean) {
    Object.defineProperty(globalThis.navigator, 'onLine', { value: v, configurable: true })
  }

  beforeEach(() => {
    vi.useFakeTimers()
    setOnline(true)
  })
  afterEach(() => {
    vi.useRealTimers()
    setOnline(true)
  })

  it('retries the same track instead of skipping while offline, however long it lasts', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([external('x'), external('y')], 0)
    const a = audios[0]
    a.currentTime = 40
    a.fire('timeupdate')

    setOnline(false)
    a.fire('error')
    a.fire('error')
    a.fire('error')

    // hours of retries, still on the same track and still trying
    vi.advanceTimersByTime(4 * 60 * 60 * 1000)
    expect(engine.getState().index).toBe(0)
    expect(engine.getState().playing).toBe(true)
    expect(engine.getState().loading).toBe(true)

    // the network returns
    setOnline(true)
    globalThis.dispatchEvent(new Event('online'))
    vi.advanceTimersByTime(10)
    expect(a.paused).toBe(false)
    expect(engine.getState().currentTimeMs).toBe(40000)
  })

  it('backs off and resumes across a short online blip', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([external('x'), external('y')], 0)
    const a = audios[0]
    a.currentTime = 10
    a.fire('timeupdate')

    a.error = { code: 2 } // MEDIA_ERR_NETWORK
    a.fire('error')
    expect(engine.getState().index).toBe(0)

    vi.advanceTimersByTime(1000)
    expect(a.paused).toBe(false)

    // audio flows again
    a.error = null
    a.fire('canplay')
    a.currentTime = 10.5
    a.fire('timeupdate')
    expect(engine.getState().index).toBe(0)
    expect(engine.getState().loading).toBe(false)
  })

  it('gives up on a track whose network errors never stop, once online', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([external('x'), external('y')], 0)
    const a = audios[0]
    a.error = { code: 2 }

    for (let i = 0; i < 8; i++) {
      a.fire('error')
      vi.advanceTimersByTime(60000)
    }

    expect(engine.getState().index).toBe(1)
  })

  it('does not retry a decode error forever — that is the file, not the network', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([external('x'), external('y')], 0)
    const a = audios[0]
    a.error = { code: 3 } // MEDIA_ERR_DECODE

    a.fire('error') // one re-attach
    a.fire('error')

    expect(engine.getState().index).toBe(1)
  })

  it('recovers a source that goes silent without erroring', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([external('x')], 0)
    const a = audios[0]
    a.currentTime = 20
    a.fire('timeupdate')
    a.fire('waiting')
    a.paused = true

    vi.advanceTimersByTime(20000) // stall watchdog
    vi.advanceTimersByTime(1000) // first backoff

    expect(engine.getState().index).toBe(0)
    expect(a.paused).toBe(false)
    expect(engine.getState().currentTimeMs).toBe(20000)
  })

  it('pausing cancels a pending retry', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([external('x')], 0)
    const a = audios[0]
    a.error = { code: 2 }
    a.fire('error')
    engine.pause()

    vi.advanceTimersByTime(60000)
    expect(a.paused).toBe(true)
    expect(engine.getState().playing).toBe(false)
  })
})

import { describe, expect, it, beforeEach, vi } from 'vitest'
import { AudioEngine, type AudioElement } from './audioEngine'
import type { Track } from './types'

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

function newEngine() {
  const audios: FakeAudio[] = []
  const engine = new AudioEngine(() => {
    const a = new FakeAudio()
    audios.push(a)
    return a
  }, (t) => `mock://${t.id}`)
  return { engine, audios }
}

const list = [track('1'), track('2'), track('3')]

describe('AudioEngine queue + transport', () => {
  let engine: AudioEngine
  let audios: FakeAudio[]
  beforeEach(() => {
    ;({ engine, audios } = newEngine())
  })

  it('plays a track list from an index', () => {
    engine.playTrackList(list, 1)
    const s = engine.getState()
    expect(s.index).toBe(1)
    expect(s.current?.id).toBe('2')
    expect(s.playing).toBe(true)
  })

  it('next advances and wraps only with repeat all', () => {
    engine.playTrackList(list, 2)
    engine.next() // at last track, repeat off → NO-OP (playing stays true, index unchanged)
    expect(engine.getState().playing).toBe(true)
    expect(engine.getState().index).toBe(2)

    engine.cycleRepeat() // off -> all
    engine.playTrackList(list, 2)
    engine.next()
    expect(engine.getState().index).toBe(0) // wrapped
  })

  it('prev goes back, clamps at start', () => {
    engine.playTrackList(list, 1)
    engine.prev()
    expect(engine.getState().index).toBe(0)
    engine.prev()
    expect(engine.getState().index).toBe(0)
  })

  it('prev restarts current track when >3s in', () => {
    engine.playTrackList(list, 1)
    audios[0].currentTime = 5 // active element; >3s in
    audios[0].fire('timeupdate')
    expect(engine.getState().currentTimeMs).toBeGreaterThan(3000)
    engine.prev()
    const s = engine.getState()
    expect(s.index).toBe(1) // unchanged
    expect(s.currentTimeMs).toBe(0) // restarted
  })

  it('repeat one replays same index on track end', () => {
    engine.playTrackList(list, 0)
    engine.cycleRepeat() // off -> all
    engine.cycleRepeat() // all -> one
    expect(engine.getState().repeat).toBe('one')
    audios[0].fire('ended')
    expect(engine.getState().index).toBe(0)
    expect(engine.getState().playing).toBe(true)
  })

  it('ended advances to next track when repeat off', () => {
    engine.playTrackList(list, 0)
    audios[0].fire('ended')
    expect(engine.getState().index).toBe(1)
  })

  it('shuffle produces a permutation covering all tracks', () => {
    engine.playTrackList(list, 0)
    engine.toggleShuffle()
    const seen = new Set<string>()
    seen.add(engine.getState().current!.id)
    engine.next()
    seen.add(engine.getState().current!.id)
    engine.next()
    seen.add(engine.getState().current!.id)
    expect(seen.size).toBe(3) // all three visited, no repeats within a cycle
  })

  it('enqueue and removeAt mutate the queue', () => {
    engine.setQueue(list, 0)
    engine.enqueue(track('4'))
    expect(engine.getState().queue.length).toBe(4)
    engine.removeAt(3)
    expect(engine.getState().queue.length).toBe(3)
  })

  it('moveItem reorders and keeps current track index correct', () => {
    engine.playTrackList(list, 0) // current = '1'
    engine.moveItem(0, 2) // move current to the end
    const s = engine.getState()
    expect(s.current?.id).toBe('1')
    expect(s.index).toBe(2)
    expect(s.queue.map((t) => t.id)).toEqual(['2', '3', '1'])
  })

  it('playAt jumps to the given index and plays', () => {
    engine.playTrackList(list, 0)
    engine.playAt(2)
    const s = engine.getState()
    expect(s.index).toBe(2)
    expect(s.current?.id).toBe('3')
    expect(s.playing).toBe(true)
  })

  it('playAt is a no-op for out-of-range indices', () => {
    engine.playTrackList(list, 0)
    engine.playAt(99)
    expect(engine.getState().index).toBe(0) // unchanged
    engine.playAt(-1)
    expect(engine.getState().index).toBe(0) // unchanged
  })

  it('playAt aligns shufflePos so next stays coherent', () => {
    engine.playTrackList(list, 0)
    engine.toggleShuffle()
    engine.playAt(2)
    expect(engine.getState().index).toBe(2)
    expect(engine.getState().current?.id).toBe('3')
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
  let engine: AudioEngine
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

  it('repeat!==one + active error: skips to next track', () => {
    engine.playTrackList(list, 0)
    expect(engine.getState().repeat).toBe('off')

    audios[0].fire('error')

    expect(engine.getState().index).toBe(1)
  })

  it('three consecutive active errors (no successful play between): engine stops instead of skipping indefinitely', () => {
    engine.playTrackList(list, 0)

    audios[0].fire('error') // consecutiveErrors=1, skips to index 1
    audios[0].fire('error') // consecutiveErrors=2, skips to index 2
    audios[0].fire('error') // consecutiveErrors=3, should STOP

    expect(engine.getState().playing).toBe(false)
  })

  it('successful play resets counter: 5-track queue — error, error, play, error → skip not stop', () => {
    const longList = [track('a'), track('b'), track('c'), track('d'), track('e')]
    engine.playTrackList(longList, 0)

    audios[0].fire('error') // consecutiveErrors=1, skips → index 1
    audios[0].fire('error') // consecutiveErrors=2, skips → index 2

    // successful play resets counter
    audios[0].paused = false
    audios[0].fire('play')

    // one more error → consecutiveErrors=1, should skip not stop
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
    const engine = new AudioEngine(() => {
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

  // A resolve that fails must not leave the spinner up forever.
  it('clears loading when the source errors', () => {
    const { engine, audios } = newEngine()
    engine.playTrackList([track('1')], 0)
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
    const engine = new AudioEngine(
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

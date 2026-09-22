import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act } from '@testing-library/react'
import { engine, setQueueTransport, usePlayer } from './playerStore'
import { FakeQueue } from '../test/fakeQueue'
import { RADIO_AHEAD, radioFromTracks } from './radio'
import type { Track } from './types'

vi.mock('./recommendationsApi', () => ({ fetchRadio: vi.fn() }))
vi.mock('./libraryApi', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./libraryApi')>()),
  prewarmExternalStream: vi.fn(),
}))

import { fetchRadio } from './recommendationsApi'
import { prewarmExternalStream } from './libraryApi'

function track(id: string, artist = 'Artist', title = 'T' + id): Track {
  return {
    id, title, albumId: 'al', album: 'Album', artistId: 'ar', artist,
    coverArtId: 'co', trackNumber: 1, discNumber: 1, durationMs: 1000, bitRate: 320,
    suffix: 'mp3', contentType: 'audio/mpeg',
  }
}

function external(id: string, artist: string): Track {
  return { ...track(id, artist), externalStream: { source: 'deezer', externalId: id } }
}

/** Lets pending fetches and queue requests resolve and the store react to them. */
async function flush() {
  for (let i = 0; i < 3; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
    })
  }
}

/** Runs a store action and waits for the core's answer to be played. */
async function run(action: () => void) {
  act(action)
  await flush()
}

let core: FakeQueue

const ids = () => usePlayer.getState().queue.map((t) => t.id)
const artists = () => usePlayer.getState().queue.map((t) => t.artist)

function longestRun(names: string[]): number {
  let best = 0
  let run = 0
  names.forEach((n, i) => {
    run = i > 0 && names[i - 1] === n ? run + 1 : 1
    best = Math.max(best, run)
  })
  return best
}

describe('playerStore', () => {
  beforeEach(async () => {
    core = new FakeQueue()
    setQueueTransport(core.transport())
    await run(() => usePlayer.getState().clearQueue())
    vi.mocked(fetchRadio).mockReset()
    vi.mocked(prewarmExternalStream).mockReset()
  })

  it('plays what the core answers for a new list', async () => {
    await run(() => usePlayer.getState().playTrackList([track('1'), track('2')], 0))
    expect(usePlayer.getState().current?.id).toBe('1')
    expect(usePlayer.getState().queue.length).toBe(2)
    expect(usePlayer.getState().playing).toBe(true)
  })

  it('next updates the mirrored current', async () => {
    await run(() => usePlayer.getState().playTrackList([track('1'), track('2')], 0))
    await run(() => usePlayer.getState().next())
    expect(usePlayer.getState().current?.id).toBe('2')
  })

  it('next() with no next track (single-track queue, repeat off) leaves playing state unchanged', async () => {
    await run(() => usePlayer.getState().playTrackList([track('1')], 0))
    await run(() => usePlayer.getState().next())
    expect(usePlayer.getState().current?.id).toBe('1')
    expect(usePlayer.getState().playing).toBe(true)
  })

  it('sends requests in the order they were made', async () => {
    await run(() => usePlayer.getState().playTrackList([track('1')], 0))
    act(() => {
      usePlayer.getState().enqueue(track('2'))
      usePlayer.getState().enqueue(track('3'))
      usePlayer.getState().moveItem(2, 1)
    })
    await flush()
    expect(ids()).toEqual(['1', '3', '2'])
  })

  it('toggles and cycles from the queue the listener can see', async () => {
    act(() => {
      usePlayer.getState().toggleShuffle()
      usePlayer.getState().toggleShuffle()
      usePlayer.getState().cycleRepeat()
      usePlayer.getState().cycleRepeat()
    })
    await flush()
    expect(usePlayer.getState().shuffle).toBe(false)
    expect(usePlayer.getState().repeat).toBe('one')
  })

  it('previous restarts a track that is well under way instead of going back', async () => {
    await run(() => usePlayer.getState().playTrackList([track('1'), track('2')], 1))
    const seek = vi.spyOn(engine, 'seekMs')
    vi.spyOn(engine, 'getState').mockReturnValueOnce({ ...engine.getState(), currentTimeMs: 5000 })
    await run(() => usePlayer.getState().prev())
    expect(seek).toHaveBeenCalledWith(0)
    expect(usePlayer.getState().index).toBe(1)
    seek.mockRestore()

    await run(() => usePlayer.getState().prev())
    expect(usePlayer.getState().index).toBe(0)
  })

  it('removes and jumps through the core', async () => {
    await run(() => usePlayer.getState().playTrackList([track('1'), track('2'), track('3')], 0))
    await run(() => usePlayer.getState().removeAt(1))
    expect(ids()).toEqual(['1', '3'])
    await run(() => usePlayer.getState().jumpTo(1))
    expect(usePlayer.getState().current?.id).toBe('3')
    expect(usePlayer.getState().playing).toBe(true)
  })

  it('stops playing when the core cannot say what follows a finished track', async () => {
    await run(() => usePlayer.getState().playTrackList([track('1'), track('2')], 0))
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    setQueueTransport({ ...core.transport(), ended: () => Promise.reject(new Error('offline')) })
    await run(() => (engine as unknown as { onEnded(): void }).onEnded())
    expect(usePlayer.getState().playing).toBe(false)
    expect(usePlayer.getState().current?.id).toBe('1')
    warn.mockRestore()
  })

  it('keeps the queue it has when the core cannot be reached', async () => {
    await run(() => usePlayer.getState().playTrackList([track('1')], 0))
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    setQueueTransport({ ...core.transport(), enqueue: () => Promise.reject(new Error('offline')) })
    await run(() => usePlayer.getState().enqueue(track('2')))
    expect(ids()).toEqual(['1'])
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
  })

  it('continues sending changes after applying one answer throws', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const apply = vi.spyOn(engine, 'apply').mockImplementationOnce(() => {
      throw new Error('bad queue answer')
    })
    await run(() => usePlayer.getState().playTrackList([track('1')], 0))
    await run(() => usePlayer.getState().enqueue(track('2')))
    expect(ids()).toEqual(['1', '2'])
    expect(warn).toHaveBeenCalled()
    apply.mockRestore()
    warn.mockRestore()
  })
})

describe('Radio', () => {
  beforeEach(async () => {
    core = new FakeQueue()
    setQueueTransport(core.transport())
    await run(() => usePlayer.getState().clearQueue())
    vi.mocked(fetchRadio).mockReset()
    vi.mocked(prewarmExternalStream).mockReset()
  })

  it('plays the seed, then keeps a few tracks queued ahead, refilling as it goes', async () => {
    vi.mocked(fetchRadio)
      .mockResolvedValueOnce([track('r1', 'A'), track('r2', 'B'), track('r3', 'C'), track('r4', 'D')])
      .mockResolvedValueOnce([track('r5', 'E'), track('r6', 'F')])
      .mockResolvedValue([])
    await run(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    expect(usePlayer.getState().current?.id).toBe('seed')
    expect(usePlayer.getState().radio).toBe(true)
    expect(fetchRadio).toHaveBeenCalledWith([{ artist: 'S', title: 'Seed' }])

    await flush()
    expect(ids()).toEqual(['seed', 'r1', 'r2', 'r3'])
    expect(usePlayer.getState().upNext.length).toBe(RADIO_AHEAD)

    await run(() => usePlayer.getState().next())
    expect(ids()).toEqual(['seed', 'r1', 'r2', 'r3', 'r4'])

    // The pool is empty now, so the latest tracks seed the next request.
    await run(() => usePlayer.getState().next())
    expect(fetchRadio).toHaveBeenCalledTimes(2)
    expect(vi.mocked(fetchRadio).mock.calls[1][0]).toEqual([
      { artist: 'D', title: 'Tr4' }, { artist: 'C', title: 'Tr3' }, { artist: 'B', title: 'Tr2' },
    ])
    await flush()
    // Three ahead again; r6 waits in the pool until there is room.
    expect(ids()).toEqual(['seed', 'r1', 'r2', 'r3', 'r4', 'r5'])
    await run(() => usePlayer.getState().next())
    expect(ids()).toEqual(['seed', 'r1', 'r2', 'r3', 'r4', 'r5', 'r6'])
  })

  it('never plays one artist more than twice in a row', async () => {
    vi.mocked(fetchRadio).mockResolvedValueOnce([
      track('a1', 'A'), track('a2', 'A'), track('a3', 'A'), track('b1', 'B'), track('a4', 'A'), track('c1', 'C'),
    ]).mockResolvedValue([])
    await run(() => usePlayer.getState().startRadio({ lead: [track('seed', 'A', 'Seed')], seeds: [{ artist: 'A', title: 'Seed' }] }))
    await flush()
    expect(ids()).toEqual(['seed', 'a1', 'b1', 'a2'])

    // Moving straight on skips each track, which steers the session too; the
    // exact order under neutral listening is covered in radio.test.ts.
    for (let i = 0; i < 6; i++) await run(() => usePlayer.getState().next())
    await flush()
    expect(longestRun(artists())).toBeLessThanOrEqual(2)
  })

  it('never repeats a recording within a session', async () => {
    vi.mocked(fetchRadio).mockResolvedValueOnce([
      track('again', 'S', 'Seed'), track('r1', 'A', 'Song'), track('r1-other-source', 'A', 'song'), track('r2', 'B'),
    ]).mockResolvedValue([track('r1-later', 'A', 'Song'), track('r3', 'C')])
    await run(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    await flush()
    await flush()
    expect(ids()).toEqual(['seed', 'r1', 'r2', 'r3'])
  })

  it('plays manually queued tracks before Radio tracks', async () => {
    vi.mocked(fetchRadio).mockResolvedValueOnce([track('r1', 'A'), track('r2', 'B'), track('r3', 'C')]).mockResolvedValue([])
    await run(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    await flush()
    act(() => {
      usePlayer.getState().enqueue(track('m1', 'M'))
      usePlayer.getState().enqueue(track('m2', 'M'))
    })
    await flush()
    expect(ids()).toEqual(['seed', 'm1', 'm2', 'r1', 'r2', 'r3'])
  })

  it('ends when something else is played', async () => {
    vi.mocked(fetchRadio).mockResolvedValue([track('r1', 'A'), track('r2', 'B'), track('r3', 'C'), track('r4', 'D')])
    await run(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    await flush()
    await run(() => usePlayer.getState().playTrackList([track('x'), track('y')], 0))
    expect(usePlayer.getState().radio).toBe(false)

    await run(() => usePlayer.getState().next())
    await flush()
    expect(ids()).toEqual(['x', 'y'])
    expect(fetchRadio).toHaveBeenCalledTimes(1)
  })

  it('ends when the queue is cleared, ignoring a request still in flight', async () => {
    let resolve!: (t: Track[]) => void
    vi.mocked(fetchRadio).mockReturnValue(new Promise((r) => { resolve = r }))
    await run(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    await run(() => usePlayer.getState().clearQueue())
    resolve([track('r1', 'A')])
    await flush()
    expect(usePlayer.getState().radio).toBe(false)
    expect(ids()).toEqual([])
    expect(usePlayer.getState().playing).toBe(false)
  })

  it('resolves the next external tracks in advance', async () => {
    vi.mocked(fetchRadio).mockResolvedValueOnce([external('e1', 'A'), track('r2', 'B'), external('e3', 'C')]).mockResolvedValue([])
    await run(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    await flush()
    expect(prewarmExternalStream).toHaveBeenCalledTimes(1)
    expect(prewarmExternalStream).toHaveBeenCalledWith('deezer', 'e1', 'A', 'Te1')
    await run(() => usePlayer.getState().next())
    expect(prewarmExternalStream).toHaveBeenCalledWith('deezer', 'e3', 'C', 'Te3')
  })

  it('starts an artist Radio with the first recommended track', async () => {
    vi.mocked(fetchRadio).mockResolvedValueOnce([track('a1', 'Daft Punk'), track('b1', 'Justice'), track('c1', 'Air'), track('d1', 'Moby')])
      .mockResolvedValue([])
    await run(() => usePlayer.getState().startRadio({ lead: [], seeds: [{ artist: 'Daft Punk' }] }))
    expect(fetchRadio).toHaveBeenCalledWith([{ artist: 'Daft Punk' }])
    await flush()
    expect(ids()).toEqual(['a1', 'b1', 'c1', 'd1'])
    expect(usePlayer.getState().current?.id).toBe('a1')
  })

  it('ends an artist Radio that finds nothing to play', async () => {
    vi.mocked(fetchRadio).mockResolvedValue([])
    await run(() => usePlayer.getState().startRadio({ lead: [], seeds: [{ artist: 'Nobody' }] }))
    await flush()
    expect(usePlayer.getState().radio).toBe(false)
    expect(ids()).toEqual([])
  })

  it('turns shuffle and repeat off so it refills instead of looping', async () => {
    vi.mocked(fetchRadio).mockResolvedValue([])
    act(() => {
      usePlayer.getState().toggleShuffle()
      usePlayer.getState().cycleRepeat()
      usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] })
    })
    await flush()
    expect(usePlayer.getState().shuffle).toBe(false)
    expect(usePlayer.getState().repeat).toBe('off')
  })

  it('waits before retrying a failed request, with the same seeds', async () => {
    let now = 1_000_000
    const clock = vi.spyOn(Date, 'now').mockImplementation(() => now)
    vi.mocked(fetchRadio).mockRejectedValueOnce(new Error('offline')).mockResolvedValue([track('r1', 'A')])
    await run(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    await flush()
    await run(() => usePlayer.getState().setVolume(0.5)) // any state change
    await flush()
    expect(fetchRadio).toHaveBeenCalledTimes(1)
    expect(usePlayer.getState().radio).toBe(true)

    now += 10_001
    await run(() => usePlayer.getState().setVolume(0.6))
    await flush()
    // The retry reuses the failed seeds; later calls are ordinary refills.
    expect(vi.mocked(fetchRadio).mock.calls[1][0]).toEqual([{ artist: 'S', title: 'Seed' }])
    expect(ids()).toEqual(['seed', 'r1'])
    clock.mockRestore()
  })

  it('does not queue a reissue of a recording from an earlier batch', async () => {
    vi.mocked(fetchRadio)
      .mockResolvedValueOnce([track('r1', 'A', 'Song')])
      .mockResolvedValueOnce([track('r1-remaster', 'A', 'Song - 2011 Remaster'), track('r2', 'B', 'Other (Deluxe Edition)')])
      .mockResolvedValue([])
    await run(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    await flush()
    await flush()
    expect(ids()).toEqual(['seed', 'r1', 'r2'])
  })

  it('ends once nothing new comes back', async () => {
    vi.mocked(fetchRadio).mockResolvedValueOnce([track('r1', 'A')]).mockResolvedValue([])
    await run(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    for (let i = 0; i < 4; i++) await flush()
    expect(usePlayer.getState().radio).toBe(false)
    expect(ids()).toEqual(['seed', 'r1'])
  })

  it('leaves what it queued as ordinary queue once it ends by itself', async () => {
    vi.mocked(fetchRadio).mockResolvedValueOnce([track('r1', 'A')]).mockResolvedValue([])
    await run(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    for (let i = 0; i < 4; i++) await flush()
    expect(usePlayer.getState().radio).toBe(false)
    await run(() => usePlayer.getState().enqueue(track('m1', 'M')))
    expect(ids()).toEqual(['seed', 'r1', 'm1'])
  })

  it('seeds a list Radio from tracks spread across the list', () => {
    const list = Array.from({ length: 10 }, (_, i) => track(String(i), 'A', 'S' + i))
    list[0].mbid = 'recording-0'
    const start = radioFromTracks(list)
    expect(start.lead.map((t) => t.id)).toEqual(['0'])
    expect(start.seeds.map((s) => s.title)).toEqual(['S0', 'S2', 'S4', 'S6', 'S8'])
    expect(start.seeds[0].mbid).toBe('recording-0')
  })
})

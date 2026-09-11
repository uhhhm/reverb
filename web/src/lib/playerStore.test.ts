import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act } from '@testing-library/react'
import { usePlayer } from './playerStore'
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

/** Lets pending fetches resolve and the store react to them. */
async function flush() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0))
  })
}

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
  beforeEach(() => {
    act(() => usePlayer.getState().clearQueue())
    vi.mocked(fetchRadio).mockReset()
    vi.mocked(prewarmExternalStream).mockReset()
  })

  it('mirrors engine state into the store after playTrackList', () => {
    act(() => {
      usePlayer.getState().playTrackList([track('1'), track('2')], 0)
    })
    expect(usePlayer.getState().current?.id).toBe('1')
    expect(usePlayer.getState().queue.length).toBe(2)
  })

  it('next updates the mirrored current', () => {
    act(() => {
      usePlayer.getState().playTrackList([track('1'), track('2')], 0)
      usePlayer.getState().cycleRepeat() // off -> all so next wraps within 2 items
      usePlayer.getState().next()
      usePlayer.getState().cycleRepeat() // all -> one
      usePlayer.getState().cycleRepeat() // one -> off
    })
    expect(usePlayer.getState().current?.id).toBe('2')
  })

  it('next() with no next track (single-track queue, repeat off) leaves playing state unchanged', () => {
    act(() => {
      usePlayer.getState().playTrackList([track('1')], 0)
    })
    // Manually set playing to true via the engine (playTrackList triggers autoplay)
    // After playTrackList, engine emits playing: true
    // Now call next() — with repeat=off and a single track, there is no next
    act(() => {
      usePlayer.getState().next()
    })
    // playing should still be true (no desync) and current track unchanged
    expect(usePlayer.getState().current?.id).toBe('1')
    expect(usePlayer.getState().playing).toBe(true)
  })
})

describe('Radio', () => {
  beforeEach(() => {
    act(() => usePlayer.getState().clearQueue())
    vi.mocked(fetchRadio).mockReset()
    vi.mocked(prewarmExternalStream).mockReset()
  })

  it('plays the seed, then keeps a few tracks queued ahead, refilling as it goes', async () => {
    vi.mocked(fetchRadio)
      .mockResolvedValueOnce([track('r1', 'A'), track('r2', 'B'), track('r3', 'C'), track('r4', 'D')])
      .mockResolvedValueOnce([track('r5', 'E'), track('r6', 'F')])
      .mockResolvedValue([])
    act(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    expect(usePlayer.getState().current?.id).toBe('seed')
    expect(usePlayer.getState().radio).toBe(true)
    expect(fetchRadio).toHaveBeenCalledWith([{ artist: 'S', title: 'Seed' }])

    await flush()
    expect(ids()).toEqual(['seed', 'r1', 'r2', 'r3'])
    expect(usePlayer.getState().upNext.length).toBe(RADIO_AHEAD)

    act(() => usePlayer.getState().next())
    expect(ids()).toEqual(['seed', 'r1', 'r2', 'r3', 'r4'])

    // The pool is empty now, so the latest tracks seed the next request.
    act(() => usePlayer.getState().next())
    expect(fetchRadio).toHaveBeenCalledTimes(2)
    expect(vi.mocked(fetchRadio).mock.calls[1][0]).toEqual([
      { artist: 'D', title: 'Tr4' }, { artist: 'C', title: 'Tr3' }, { artist: 'B', title: 'Tr2' },
    ])
    await flush()
    // Three ahead again; r6 waits in the pool until there is room.
    expect(ids()).toEqual(['seed', 'r1', 'r2', 'r3', 'r4', 'r5'])
    act(() => usePlayer.getState().next())
    expect(ids()).toEqual(['seed', 'r1', 'r2', 'r3', 'r4', 'r5', 'r6'])
  })

  it('never plays one artist more than twice in a row', async () => {
    vi.mocked(fetchRadio).mockResolvedValueOnce([
      track('a1', 'A'), track('a2', 'A'), track('a3', 'A'), track('b1', 'B'), track('a4', 'A'), track('c1', 'C'),
    ]).mockResolvedValue([])
    act(() => usePlayer.getState().startRadio({ lead: [track('seed', 'A', 'Seed')], seeds: [{ artist: 'A', title: 'Seed' }] }))
    await flush()
    expect(ids()).toEqual(['seed', 'a1', 'b1', 'a2'])

    for (let i = 0; i < 6; i++) act(() => usePlayer.getState().next())
    await flush()
    expect(longestRun(artists())).toBeLessThanOrEqual(2)
    expect(ids()).toEqual(['seed', 'a1', 'b1', 'a2', 'a3', 'c1', 'a4'])
  })

  it('never repeats a recording within a session', async () => {
    vi.mocked(fetchRadio).mockResolvedValueOnce([
      track('again', 'S', 'Seed'), track('r1', 'A', 'Song'), track('r1-other-source', 'A', 'song'), track('r2', 'B'),
    ]).mockResolvedValue([track('r1-later', 'A', 'Song'), track('r3', 'C')])
    act(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    await flush()
    await flush()
    expect(ids()).toEqual(['seed', 'r1', 'r2', 'r3'])
  })

  it('plays manually queued tracks before Radio tracks', async () => {
    vi.mocked(fetchRadio).mockResolvedValueOnce([track('r1', 'A'), track('r2', 'B'), track('r3', 'C')]).mockResolvedValue([])
    act(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    await flush()
    act(() => {
      usePlayer.getState().enqueue(track('m1', 'M'))
      usePlayer.getState().enqueue(track('m2', 'M'))
    })
    expect(ids()).toEqual(['seed', 'm1', 'm2', 'r1', 'r2', 'r3'])
  })

  it('ends when something else is played', async () => {
    vi.mocked(fetchRadio).mockResolvedValue([track('r1', 'A'), track('r2', 'B'), track('r3', 'C'), track('r4', 'D')])
    act(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    await flush()
    act(() => usePlayer.getState().playTrackList([track('x'), track('y')], 0))
    expect(usePlayer.getState().radio).toBe(false)

    act(() => usePlayer.getState().next())
    await flush()
    expect(ids()).toEqual(['x', 'y'])
    expect(fetchRadio).toHaveBeenCalledTimes(1)
  })

  it('ends when the queue is cleared, ignoring a request still in flight', async () => {
    let resolve!: (t: Track[]) => void
    vi.mocked(fetchRadio).mockReturnValue(new Promise((r) => { resolve = r }))
    act(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    act(() => usePlayer.getState().clearQueue())
    resolve([track('r1', 'A')])
    await flush()
    expect(usePlayer.getState().radio).toBe(false)
    expect(ids()).toEqual([])
    expect(usePlayer.getState().playing).toBe(false)
  })

  it('resolves the next external tracks in advance', async () => {
    vi.mocked(fetchRadio).mockResolvedValueOnce([external('e1', 'A'), track('r2', 'B'), external('e3', 'C')]).mockResolvedValue([])
    act(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    await flush()
    expect(prewarmExternalStream).toHaveBeenCalledTimes(1)
    expect(prewarmExternalStream).toHaveBeenCalledWith('deezer', 'e1', 'A', 'Te1')
    act(() => usePlayer.getState().next())
    expect(prewarmExternalStream).toHaveBeenCalledWith('deezer', 'e3', 'C', 'Te3')
  })

  it('starts an artist Radio with the first recommended track', async () => {
    vi.mocked(fetchRadio).mockResolvedValueOnce([track('a1', 'Daft Punk'), track('b1', 'Justice'), track('c1', 'Air'), track('d1', 'Moby')])
      .mockResolvedValue([])
    act(() => usePlayer.getState().startRadio({ lead: [], seeds: [{ artist: 'Daft Punk' }] }))
    expect(fetchRadio).toHaveBeenCalledWith([{ artist: 'Daft Punk' }])
    await flush()
    expect(ids()).toEqual(['a1', 'b1', 'c1', 'd1'])
    expect(usePlayer.getState().current?.id).toBe('a1')
  })

  it('ends an artist Radio that finds nothing to play', async () => {
    vi.mocked(fetchRadio).mockResolvedValue([])
    act(() => usePlayer.getState().startRadio({ lead: [], seeds: [{ artist: 'Nobody' }] }))
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
    act(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    await flush()
    act(() => usePlayer.getState().setVolume(0.5)) // any state change
    await flush()
    expect(fetchRadio).toHaveBeenCalledTimes(1)
    expect(usePlayer.getState().radio).toBe(true)

    now += 10_001
    act(() => usePlayer.getState().setVolume(0.6))
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
    act(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    await flush()
    await flush()
    expect(ids()).toEqual(['seed', 'r1', 'r2'])
  })

  it('ends once nothing new comes back', async () => {
    vi.mocked(fetchRadio).mockResolvedValueOnce([track('r1', 'A')]).mockResolvedValue([])
    act(() => usePlayer.getState().startRadio({ lead: [track('seed', 'S', 'Seed')], seeds: [{ artist: 'S', title: 'Seed' }] }))
    for (let i = 0; i < 4; i++) await flush()
    expect(usePlayer.getState().radio).toBe(false)
    expect(ids()).toEqual(['seed', 'r1'])
  })

  it('seeds a list Radio from tracks spread across the list', () => {
    const list = Array.from({ length: 10 }, (_, i) => track(String(i), 'A', 'S' + i))
    const start = radioFromTracks(list)
    expect(start.lead.map((t) => t.id)).toEqual(['0'])
    expect(start.seeds.map((s) => s.title)).toEqual(['S0', 'S2', 'S4', 'S6', 'S8'])
  })
})

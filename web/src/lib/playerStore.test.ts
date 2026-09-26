import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act } from '@testing-library/react'
import { engine, setQueueTransport, usePlayer } from './playerStore'
import { FakeQueue } from '../test/fakeQueue'
import { radioFromTracks } from './radio'
import type { Track } from './types'

vi.mock('./libraryApi', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./libraryApi')>()),
  prewarmExternalStream: vi.fn(),
}))

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
describe('playerStore', () => {
  beforeEach(async () => {
    core = new FakeQueue()
    setQueueTransport(core.transport())
    await run(() => usePlayer.getState().clearQueue())
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

describe('core Radio', () => {
  // Failure cases: the core never hears how far a left track was played, so
  // it cannot tell a skip; or hears only after the move, about the wrong track.
  it('plays the core answer and reports the track being left before moving on', async () => {
    core = new FakeQueue()
    const transport = core.transport()
    const calls: string[] = []
    const startRadio = vi.fn(async () => {
      const state = await transport.play([track('seed'), external('next', 'B')], 0)
      return { ...state, radio: true }
    })
    const progress = vi.fn(async (sample: { entryId: string }) => {
      calls.push(`progress:${sample.entryId}`)
      return { ...await transport.get(), radio: true }
    })
    const next = async (entryId?: string) => {
      calls.push('next')
      return { ...await transport.next(entryId), radio: true }
    }
    setQueueTransport({ ...transport, startRadio, progress, next })
    await run(() => usePlayer.getState().startRadio(radioFromTracks([track('seed')])))
    expect(startRadio).toHaveBeenCalledWith(radioFromTracks([track('seed')]))
    expect(usePlayer.getState().radio).toBe(true)
    expect(ids()).toEqual(['seed', 'next'])
    const seedEntry = (await transport.get()).entries[0].id
    calls.length = 0
    await run(() => usePlayer.getState().next())
    expect(calls.slice(0, 2)).toEqual([`progress:${seedEntry}`, 'next'])
    expect(prewarmExternalStream).not.toHaveBeenCalled()
    await run(() => usePlayer.getState().playTrackList([track('chosen')], 0))
    expect(usePlayer.getState().radio).toBe(false)
  })
})

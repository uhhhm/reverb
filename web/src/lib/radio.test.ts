import { describe, expect, it } from 'vitest'
import type { PlayerState } from './audioEngine'
import { ARTIST_SKIP_LIMIT, RadioSession, type RadioHost, type RadioStart } from './radio'
import type { Track } from './types'

function track(id: string, artist: string, seedTitle?: string): Track {
  return {
    id, title: 'T' + id, albumId: 'al', album: 'Album', artistId: 'ar', artist,
    coverArtId: '', trackNumber: 1, discNumber: 1, durationMs: 200_000, bitRate: 0, suffix: '', contentType: '',
    ...(seedTitle ? { reason: { kind: 'similar' as const, artist: 'Seed Artist', title: seedTitle } } : {}),
  }
}

/** A player with the engine's queue semantics and nothing else. */
class FakePlayer implements RadioHost {
  queue: Track[] = []
  index = -1
  timeMs = 0
  session: RadioSession | null = null
  private batches: Track[][]

  constructor(batches: Track[][]) {
    this.batches = batches
  }

  fetch = async () => this.batches.shift() ?? []
  getState(): PlayerState {
    const current = this.queue[this.index] ?? null
    return {
      queue: this.queue, index: this.index, current, playing: true, currentTimeMs: this.timeMs,
      durationMs: current?.durationMs ?? 0, bufferedMs: 0, loading: false, volume: 1,
      shuffle: false, repeat: 'off', upNext: this.queue.map((_, i) => i).filter((i) => i > this.index),
    }
  }
  play(tracks: Track[]) {
    this.queue = [...tracks]
    this.index = 0
    this.timeMs = 0
    this.changed()
  }
  append(t: Track) {
    this.queue.push(t)
    this.changed()
  }
  remove(i: number) {
    this.queue.splice(i, 1)
    this.changed()
  }
  prewarm() {}
  ended() {}

  private changed() {
    this.session?.update()
  }
  /** Moves on to the next track, having heard the current one for heardMs. */
  next(heardMs = 0) {
    this.timeMs = heardMs
    this.changed()
    this.index++
    this.timeMs = 0
    this.changed()
  }
  /** Hears the current track to its end. */
  finish() {
    this.timeMs = this.queue[this.index].durationMs
    this.changed()
  }
  /** A track the listener queues to play next. */
  enqueueNext(t: Track) {
    this.queue.splice(this.index + 1, 0, t)
    this.changed()
  }
  currentId(): string {
    return this.queue[this.index].id
  }
  upcoming(): string[] {
    return this.queue.slice(this.index + 1).map((t) => t.id)
  }
}

const START: RadioStart = { lead: [track('seed', 'Seed Artist')], seeds: [{ artist: 'Seed Artist', title: 'Tseed' }] }
const SKIP = 10_000
const WHOLE = 200_000

async function startRadio(player: FakePlayer): Promise<RadioSession> {
  const session = new RadioSession(player, START)
  player.session = session
  session.begin()
  await new Promise((r) => setTimeout(r, 0))
  return session
}

/** Past half a track but short of its end: neither a skip nor a finish. */
const NEUTRAL = 150_000

describe('RadioSession queueing', () => {
  it('holds back a third track in a row by one artist until another plays', async () => {
    const player = new FakePlayer([[
      track('a1', 'A'), track('a2', 'A'), track('a3', 'A'), track('b1', 'B'), track('a4', 'A'), track('c1', 'C'),
    ]])
    player.session = new RadioSession(player, { lead: [track('seed', 'A')], seeds: [{ artist: 'A', title: 'Tseed' }] })
    player.session.begin()
    await new Promise((r) => setTimeout(r, 0))
    expect(player.upcoming()).toEqual(['a1', 'b1', 'a2'])

    for (let i = 0; i < 6 && player.index < player.queue.length - 1; i++) player.next(NEUTRAL)
    expect(player.queue.map((t) => t.id)).toEqual(['seed', 'a1', 'b1', 'a2', 'a3', 'c1', 'a4'])
  })
})

describe('RadioSession steering', () => {
  it(`stops an artist after ${ARTIST_SKIP_LIMIT} skips, keeping tracks the listener queued`, async () => {
    const player = new FakePlayer([[
      track('x1', 'X'), track('a1', 'A'), track('x2', 'X'), track('b1', 'B'),
      track('x3', 'X'), track('c1', 'C'), track('x4', 'X'), track('d1', 'D'), track('e1', 'E'),
    ]])
    await startRadio(player)
    expect(player.upcoming()).toEqual(['x1', 'a1', 'x2'])

    player.next(WHOLE) // the seed
    player.next(SKIP) // x1: the first skip of X
    player.enqueueNext(track('mine', 'X'))
    player.next(SKIP) // a1
    expect(player.currentId()).toBe('mine')
    player.next(SKIP) // the listener's X track: the second skip of X

    expect(player.upcoming().filter((id) => id.startsWith('x'))).toEqual([])
    player.enqueueNext(track('kept', 'X'))
    player.finish() // finishing re-ranks what Radio queued
    expect(player.upcoming()).toContain('kept')

    for (let i = 0; i < 10 && player.index < player.queue.length - 1; i++) player.next(WHOLE)
    const radioAfterBlock = player.queue.slice(player.queue.findIndex((t) => t.id === 'kept') + 1)
    expect(radioAfterBlock.map((t) => t.artist)).not.toContain('X')
  })

  it('pulls a finished artist ahead of the rest of the queue', async () => {
    const player = new FakePlayer([[track('a1', 'A'), track('b1', 'B'), track('c1', 'C'), track('d1', 'D'), track('a2', 'A')]])
    await startRadio(player)
    player.next(WHOLE)
    expect(player.currentId()).toBe('a1')
    player.finish()
    expect(player.upcoming()).toEqual(['a2', 'b1', 'c1'])
  })

  it('pushes down tracks recommended from the same seed as a skipped one', async () => {
    const player = new FakePlayer([[
      track('p1', 'P', 'Seed P'), track('q1', 'Q', 'Seed Q'), track('q2', 'R', 'Seed Q'),
      track('p2', 'S', 'Seed P'), track('w1', 'W', 'Seed W'),
    ]])
    await startRadio(player)
    player.next(WHOLE)
    player.next(SKIP) // p1
    expect(player.currentId()).toBe('q1')
    expect(player.upcoming()).toEqual(['q2', 'w1', 'p2'])
  })

  it('starts each session without the last one’s steering', async () => {
    const batch = () => [track('x1', 'X'), track('x2', 'X'), track('a1', 'A'), track('x3', 'X')]
    // The first session refills as it goes, so leave batches for the second.
    const player = new FakePlayer([batch(), batch(), batch(), batch()])
    const first = await startRadio(player)
    player.next(WHOLE)
    player.next(SKIP)
    player.next(SKIP)
    first.end()

    await startRadio(player)
    expect(player.upcoming()).toContain('x1')
  })
})

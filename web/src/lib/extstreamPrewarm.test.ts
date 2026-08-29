import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { prewarmTopResults, resetPrewarmed } from './extstreamPrewarm'
import type { ExternalResult } from './types'

function result(i: number): ExternalResult {
  return {
    source: 'deezer',
    externalId: `e${i}`,
    title: `Song ${i}`,
    artist: 'Artist',
    album: 'Album',
    durationMs: 1000,
  } as ExternalResult
}

function prewarmedIds(fetchMock: ReturnType<typeof vi.fn>): string[] {
  return fetchMock.mock.calls.map(([url]) => new URL(url as string, 'http://x').pathname.split('/')[6])
}

describe('external stream prewarming', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    resetPrewarmed()
    fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
  })
  afterEach(() => vi.unstubAllGlobals())

  // Each prewarm spawns a yt-dlp process. The point is to cover what the user is
  // plausibly about to click, not to resolve an entire result page.
  it('prewarms only the top few results', () => {
    prewarmTopResults([1, 2, 3, 4, 5, 6, 7].map(result))
    expect(prewarmedIds(fetchMock)).toEqual(['e1', 'e2', 'e3', 'e4'])
  })

  // Results re-render constantly as sources stream in; a track must not be
  // resolved again on every one.
  it('never prewarms the same track twice in a session', () => {
    prewarmTopResults([result(1), result(2)])
    prewarmTopResults([result(1), result(2), result(3)])
    expect(prewarmedIds(fetchMock)).toEqual(['e1', 'e2', 'e3'])
  })

  it('posts to the prewarm endpoint with the artist and title hints', () => {
    prewarmTopResults([result(1)])
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toContain('/api/v1/external/stream/deezer/e1/prewarm')
    expect(url).toContain('artist=Artist')
    expect(url).toContain('title=Song+1')
    expect(init).toEqual({ method: 'POST' })
  })
})

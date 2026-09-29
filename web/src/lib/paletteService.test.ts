import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { credentialsFor } from './paletteWorker'
import type { RGB } from './palette'
import { FakePaletteWorker, installFakePaletteWorker } from '../test/fakePaletteWorker'

describe('credentialsFor', () => {
  it('returns include for relative /api/... URLs (same-origin library covers)', () => {
    expect(credentialsFor('/api/v1/cover/abc123')).toBe('include')
  })

  it('returns include for any /-relative URL', () => {
    expect(credentialsFor('/cover/foo')).toBe('include')
  })

  it('returns omit for Spotify CDN URLs (cross-origin external covers)', () => {
    expect(credentialsFor('https://i.scdn.co/image/ab67616d0000b273abc')).toBe('omit')
  })

  it('returns omit for any absolute https cross-origin URL', () => {
    expect(credentialsFor('https://example.com/cover.jpg')).toBe('omit')
  })

  it('returns omit for malformed URLs', () => {
    expect(credentialsFor('not a url')).toBe('omit')
  })
})

describe('paletteService', () => {
  let getPalette: typeof import('./paletteService').getPalette
  const extract = vi.fn<(u: string) => Promise<RGB>>()

  beforeEach(async () => {
    extract.mockReset()
    installFakePaletteWorker(extract)
    ;({ getPalette } = await import('./paletteService'))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('extracts each cover once and serves repeats from the cache', async () => {
    extract.mockResolvedValue([10, 20, 30])

    expect(await getPalette('/cover/a')).toEqual([10, 20, 30])
    expect(await getPalette('/cover/a')).toEqual([10, 20, 30])
    expect(await getPalette('/cover/b')).toEqual([10, 20, 30])
    expect(FakePaletteWorker.requests).toEqual(['/cover/a', '/cover/b'])
  })

  it('shares one in-flight extraction for concurrent identical URLs', async () => {
    let release: (v: RGB) => void = () => {}
    extract.mockReturnValue(new Promise<RGB>((res) => { release = res }))

    const p1 = getPalette('/cover/x')
    const p2 = getPalette('/cover/x')
    release([1, 2, 3])

    expect(await Promise.all([p1, p2])).toEqual([[1, 2, 3], [1, 2, 3]])
    expect(FakePaletteWorker.requests).toEqual(['/cover/x'])
  })

  it('routes out-of-order worker replies to the cover that asked', async () => {
    const release: Record<string, (v: RGB) => void> = {}
    extract.mockImplementation((url) => new Promise<RGB>((res) => { release[url] = res }))

    const a = getPalette('/cover/a')
    const b = getPalette('/cover/b')
    release['/cover/b']([0, 0, 255])
    release['/cover/a']([255, 0, 0])

    expect(await a).toEqual([255, 0, 0])
    expect(await b).toEqual([0, 0, 255])
  })

  it('rejects a failed extraction and retries that cover on the next request', async () => {
    extract.mockRejectedValueOnce(new Error('decode failed')).mockResolvedValueOnce([7, 7, 7])

    await expect(getPalette('/cover/broken')).rejects.toThrow('decode failed')
    expect(await getPalette('/cover/broken')).toEqual([7, 7, 7])
    expect(FakePaletteWorker.requests).toEqual(['/cover/broken', '/cover/broken'])
  })
})

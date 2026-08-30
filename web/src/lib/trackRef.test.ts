import { describe, expect, it } from 'vitest'
import { decodeExternalId, dedupKey, dedupKeyForTrack, encodeExternalId, isExternalId, isExternalTrack, needsBackendSeek, normalize } from './trackRef'
import type { ExternalResult, Track } from './types'

function ext(p: Partial<ExternalResult>): ExternalResult {
  return { source: 's', externalId: 'e', title: 'T', artist: 'A', album: 'Al', durationMs: 1000, type: 'track', ...p }
}
function track(id: string, p: Partial<Track> = {}): Track {
  return { id, title: 'T', albumId: 'al', album: 'Al', artistId: 'ar', artist: 'A', coverArtId: 'co', trackNumber: 1, discNumber: 1, durationMs: 1000, bitRate: 320, suffix: 'mp3', contentType: 'audio/mpeg', ...p }
}

describe('encode/decode/isExternalId', () => {
  it('round-trips', () => {
    expect(encodeExternalId('deezer', '123')).toBe('deezer:123')
    expect(decodeExternalId('deezer:123')).toEqual({ source: 'deezer', externalId: '123' })
    expect(isExternalId('deezer:123')).toBe(true)
  })
  it('trims', () => {
    expect(encodeExternalId(' deezer ', ' 123 ')).toBe('deezer:123')
    expect(decodeExternalId(' deezer:123 ')).toEqual({ source: 'deezer', externalId: '123' })
  })
  it('rejects invalid', () => {
    expect(encodeExternalId('', '123')).toBe('')
    expect(encodeExternalId('deezer', '')).toBe('')
    expect(decodeExternalId('')).toBeNull()
    expect(decodeExternalId('deezer')).toBeNull()
    expect(decodeExternalId(':123')).toBeNull()
    expect(decodeExternalId('deezer:')).toBeNull()
    expect(decodeExternalId('trk_abc')).toBeNull()
    expect(decodeExternalId('al2f3c9d8e7b6a5')).toBeNull()
    expect(isExternalId('trk_abc')).toBe(false)
    expect(isExternalId('')).toBe(false)
  })
  it('externalId may contain colon', () => {
    expect(decodeExternalId('a:b:c')).toEqual({ source: 'a', externalId: 'b:c' })
    expect(isExternalId('a:b:c')).toBe(true)
  })
})

describe('isExternalTrack', () => {
  it('checks externalStream field', () => {
    expect(isExternalTrack(track('x'))).toBe(false)
    expect(isExternalTrack({ ...track('a:b'), externalStream: { source: 'deezer', externalId: '1' } })).toBe(true)
  })
})

describe('normalize mirrors Go matching.Normalize', () => {
  it('lowercases, folds diacritics, handles & and pt', () => {
    expect(normalize('Björk')).toBe('bjork')
    expect(normalize('Jóga')).toBe('joga')
    expect(normalize('Salt & Sea')).toBe('salt and sea')
    expect(normalize('Movement Pt. 1')).toBe('movement part 1')
    expect(normalize('Movement PT 1')).toBe('movement part 1')
  })
  it('strips feat groups symmetrically', () => {
    expect(normalize('Sunrise (feat. Aluna)')).toBe('sunrise')
    expect(normalize('Sunrise')).toBe('sunrise')
    expect(normalize('Sunrise (feat. Aluna)')).toBe(normalize('Sunrise'))
    expect(normalize('Echoes ft. K')).toBe('echoes')
    expect(normalize('Skyline featuring Mara')).toBe('skyline')
  })
  it('does not strip mid-word ft', () => {
    expect(normalize('Daft Punk')).toBe('daft punk')
    expect(normalize('Drift')).toBe('drift')
    expect(normalize('Gift')).toBe('gift')
  })
  it('keeps parentheses for qualifiers', () => {
    expect(normalize('Song (Live)')).toBe('song (live)')
    expect(normalize('Wanderer (Remaster 2011)')).toBe('wanderer (remaster 2011)')
    expect(normalize('Song - Live')).toBe('song live')
    expect(normalize('Song [Live]')).toBe('song live')
  })
  it('collapses whitespace and drops punctuation', () => {
    expect(normalize('  Hello,  World!! ')).toBe('hello world')
    expect(normalize('')).toBe('')
    expect(normalize('   ')).toBe('')
  })
  it('cyrillic preserved lowercased', () => {
    expect(normalize('Кукушка')).toBe('кукушка')
  })
})

describe('dedupKey', () => {
  it('prefers ISRC when present', () => {
    expect(dedupKey(ext({ isrc: 'USX1' }))).toBe('isrc:usx1')
  })
  it('falls back to normalized artist+title', () => {
    expect(dedupKey(ext({ artist: 'The Band', title: 'Song (feat. X)' }))).toBe(dedupKey(ext({ artist: 'The Band', title: 'Song' })))
  })
  it('daft punk word-boundary', () => {
    const key = dedupKey(ext({ artist: 'Daft Punk', title: 'Get Lucky' }))
    expect(key).toContain('daft punk')
  })
  it('separator prevents collision', () => {
    const k1 = dedupKey(ext({ artist: 'a', title: 'bc' }))
    const k2 = dedupKey(ext({ artist: 'ab', title: 'c' }))
    expect(k1).not.toBe(k2)
  })
  it('matches dedupKeyForTrack for same song', () => {
    expect(dedupKeyForTrack(track('x', { artist: 'Daft Punk', title: 'Get Lucky' }))).toBe(dedupKey(ext({ artist: 'Daft Punk', title: 'Get Lucky' })))
  })
  it('diacritic folded', () => {
    expect(dedupKey(ext({ artist: 'Björk', title: 'Jóga' }))).toBe('nf:bjork␟joga')
  })
})

// A browser cannot find a position in a container it has no index for: it maps
// the time onto a byte offset through an assumed bitrate and lands seconds away,
// so the backend is asked to start the audio there instead.
describe('needsBackendSeek', () => {
  const t = (o: Partial<Track>): Track =>
    ({ id: '1', title: 'T', artist: 'A', album: 'Al', ...o }) as Track

  it('is true for ogg/opus, whatever names it', () => {
    expect(needsBackendSeek(t({ suffix: 'opus', contentType: 'audio/ogg' }))).toBe(true)
    expect(needsBackendSeek(t({ suffix: 'ogg' }))).toBe(true)
    expect(needsBackendSeek(t({ contentType: 'audio/webm; codecs=opus' }))).toBe(true)
  })

  it('is false for the formats a browser seeks itself', () => {
    expect(needsBackendSeek(t({ suffix: 'mp3', contentType: 'audio/mpeg' }))).toBe(false)
    expect(needsBackendSeek(t({ suffix: 'm4a', contentType: 'audio/mp4' }))).toBe(false)
    expect(needsBackendSeek(t({ suffix: 'flac' }))).toBe(false)
  })

  // An external stream is not a file the backend can seek into.
  it('is false for an external track', () => {
    expect(
      needsBackendSeek(t({ suffix: 'opus', externalStream: { source: 'deezer', externalId: 'x' } })),
    ).toBe(false)
  })
})

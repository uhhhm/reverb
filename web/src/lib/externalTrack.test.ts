import { describe, expect, it } from 'vitest'
import { externalResultFromRef, externalTrackFromRef } from './externalTrack'
import type { ExternalTrackRef } from './types'

const ref: ExternalTrackRef = {
  source: 'spotify',
  externalId: 'm1',
  title: 'Treefingers',
  artist: 'Radiohead',
  album: 'Kid A',
  durationMs: 2000,
  isrc: 'GBAYE0000123',
}

describe('externalTrackFromRef', () => {
  it('builds a streamable Track with encoded id and externalStream', () => {
    const t = externalTrackFromRef(ref, { albumName: 'Kid A', albumArtist: 'Radiohead', trackNumber: 3 })
    expect(t.id).toBe('spotify:m1')
    expect(t.externalStream).toEqual({ source: 'spotify', externalId: 'm1' })
    expect(t.title).toBe('Treefingers')
    expect(t.artist).toBe('Radiohead')
    expect(t.album).toBe('Kid A')
    expect(t.trackNumber).toBe(3)
    expect(t.durationMs).toBe(2000)
    expect(t.isrc).toBe('GBAYE0000123')
  })

  it('falls back to album context when ref artist/album are absent', () => {
    const bare: ExternalTrackRef = { source: 'deezer', externalId: 'd1', title: 'Song', durationMs: 1000 }
    const t = externalTrackFromRef(bare, { albumName: 'Album', albumArtist: 'Artist', trackNumber: 1 })
    expect(t.artist).toBe('Artist')
    expect(t.album).toBe('Album')
    expect(t.isrc).toBeUndefined()
  })

  it('falls back to a display-only key when source/id are blank', () => {
    const bad: ExternalTrackRef = { source: '', externalId: '', title: 'Ghost', durationMs: 1000 }
    const t = externalTrackFromRef(bad)
    expect(t.id).toContain('ext:invalid')
    expect(t.id).not.toBe('')
    expect(t.externalStream).toEqual({ source: '', externalId: '' })
  })
  it('threads artistExternalId through when provided', () => {
    const t = externalTrackFromRef(ref, { artistExternalId: 'sp-artist-77' })
    expect(t.artistExternalId).toBe('sp-artist-77')
  })
})

describe('externalResultFromRef', () => {
  it('fills artist/album from fallbacks when ref omits them', () => {
    const bare: ExternalTrackRef = { source: 'spotify', externalId: 'x', title: 'T', durationMs: 5 }
    expect(externalResultFromRef(bare, 'Kid A', 'Radiohead')).toMatchObject({
      source: 'spotify',
      externalId: 'x',
      artist: 'Radiohead',
      album: 'Kid A',
      type: 'track',
    })
  })
})


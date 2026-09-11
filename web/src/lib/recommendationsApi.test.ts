import { describe, expect, it } from 'vitest'
import { recommendedTrackToTrack } from './recommendationsApi'
import type { RecommendedTrack } from './recommendationsApi'

describe('recommendedTrackToTrack', () => {
  it('plays an owned recommendation from the library by its backend id', () => {
    const owned: RecommendedTrack = {
      source: 'library',
      externalId: 'lib-7',
      canonicalId: 'trk_7',
      title: 'Around the World',
      artist: 'Daft Punk',
      album: '',
      durationMs: 429000,
      type: 'track',
      match: { status: 'in_library', libraryTrackId: 'lib-7', method: 'fuzzy', confidence: 0.9, albumId: 'al-1', artistId: 'ar-1', coverArtId: 'cov-1' },
    }
    const track = recommendedTrackToTrack(owned)
    expect(track.id).toBe('lib-7')
    expect(track.externalStream).toBeUndefined()
    expect(track).toMatchObject({ title: 'Around the World', artist: 'Daft Punk', albumId: 'al-1', artistId: 'ar-1', coverArtId: 'cov-1' })
  })

  it('streams anything else from its source', () => {
    const track = recommendedTrackToTrack({
      source: 'deezer',
      externalId: '3135556',
      title: 'D.A.N.C.E.',
      artist: 'Justice',
      album: 'Cross',
      durationMs: 242000,
      type: 'track',
      artistExternalId: '6404',
    })
    expect(track.externalStream).toEqual({ source: 'deezer', externalId: '3135556' })
    expect(track).toMatchObject({ title: 'D.A.N.C.E.', artist: 'Justice', album: 'Cross', artistExternalId: '6404' })
  })
})

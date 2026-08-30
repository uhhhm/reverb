import { describe, expect, it, vi, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useWaveformPeaks, usePeaks } from './peaksApi'
import type { Track } from './types'

vi.mock('@tanstack/react-query', () => ({ useQuery: vi.fn() }))
import { useQuery } from '@tanstack/react-query'

const peaks = Array.from({ length: 100 }, (_, i) => i / 100)

function track(overrides: Partial<Track> = {}): Track {
  return {
    id: '1', title: 'Song', albumId: 'al', album: 'Album', artistId: 'ar', artist: 'Artist',
    coverArtId: 'co', trackNumber: 1, discNumber: 1, durationMs: 200000, bitRate: 320,
    suffix: 'mp3', contentType: 'audio/mpeg', ...overrides,
  }
}

// Peaks describe the whole file, while a cropped track's rail spans only the
// crop window — an unsliced waveform would run ahead of the audio.
describe('useWaveformPeaks', () => {
  beforeEach(() => {
    vi.mocked(useQuery).mockReturnValue({ data: peaks } as ReturnType<typeof usePeaks>)
  })

  it('returns every peak for an uncropped track', () => {
    const { result } = renderHook(() => useWaveformPeaks(track()))
    expect(result.current).toHaveLength(100)
  })

  it('slices to the crop window', () => {
    // 20s..120s of a 200s file — the middle half of the peaks.
    const { result } = renderHook(() => useWaveformPeaks(track({ cropStartMs: 20000, cropEndMs: 120000 })))
    expect(result.current).toEqual(peaks.slice(10, 60))
  })

  it('runs a crop start to the end of the file', () => {
    const { result } = renderHook(() => useWaveformPeaks(track({ cropStartMs: 100000 })))
    expect(result.current).toEqual(peaks.slice(50))
  })

  // Without a file length there is nothing to slice against.
  it('leaves the peaks alone when the track has no known length', () => {
    const { result } = renderHook(() => useWaveformPeaks(track({ durationMs: 0, cropStartMs: 20000 })))
    expect(result.current).toHaveLength(100)
  })

  it('handles no track', () => {
    vi.mocked(useQuery).mockReturnValue({ data: undefined } as ReturnType<typeof usePeaks>)
    const { result } = renderHook(() => useWaveformPeaks(null))
    expect(result.current).toBeUndefined()
  })
})

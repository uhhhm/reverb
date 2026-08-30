import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { Track } from './types'
import { isExternalTrack } from './trackRef'

/**
 * Waveform peaks for the seek rail. Peaks are read from the file on disk, so a
 * track that isn't in the library has none — pass isExternal to skip a request
 * that can only 404 (and logs a Navidrome error each time).
 */
export function usePeaks(trackId: string | undefined, isExternal = false) {
  return useQuery({
    queryKey: ['peaks', trackId],
    queryFn: async () => {
      const response = await fetch(`/api/v1/library/track/${encodeURIComponent(trackId!)}/peaks`, { credentials: 'include' })
      if (response.status === 204) return null
      if (!response.ok) throw new Error(`peaks ${response.status}`)
      return (await response.json() as { peaks: number[] }).peaks
    },
    enabled: !!trackId && !isExternal,
    staleTime: Infinity,
    retry: false,
  })
}

/**
 * The peaks a seek rail should draw for a track. Peaks are read from the whole
 * file, but a cropped track's rail spans only the crop window — drawing every
 * peak would run the waveform ahead of the audio, so the slice matches what the
 * rail represents.
 */
export function useWaveformPeaks(track: Track | null | undefined): number[] | null | undefined {
  const all = usePeaks(track?.id, track ? isExternalTrack(track) : false).data
  const cropStartMs = track?.cropStartMs ?? 0
  const cropEndMs = track?.cropEndMs ?? 0
  const fileMs = track?.durationMs ?? 0
  return useMemo(() => {
    if (!all?.length || fileMs <= 0) return all
    const end = cropEndMs > 0 ? cropEndMs : fileMs
    if (cropStartMs <= 0 && end >= fileMs) return all
    const at = (ms: number) => Math.round((ms / fileMs) * all.length)
    return all.slice(Math.max(0, at(cropStartMs)), Math.min(all.length, at(end)))
  }, [all, cropStartMs, cropEndMs, fileMs])
}

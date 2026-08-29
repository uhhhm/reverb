import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './api'
import type { AudioQuality } from './audioQuality'

/**
 * A track's standing quality: the per-track override when it has one, otherwise
 * the global download_quality setting it falls back to.
 */
export interface TrackQuality {
  quality: AudioQuality
  overridden: boolean
  default: AudioQuality
}

export function getTrackQuality(trackId: string): Promise<TrackQuality> {
  return api.get<TrackQuality>(`/library/track/${encodeURIComponent(trackId)}/quality`)
}

/** An empty quality clears the override. */
export function setTrackQuality(trackId: string, quality: AudioQuality | ''): Promise<TrackQuality> {
  return api.put<TrackQuality>(`/library/track/${encodeURIComponent(trackId)}/quality`, { quality })
}

export function useTrackQuality(trackId: string | undefined) {
  return useQuery({
    queryKey: ['track-quality', trackId ?? ''],
    queryFn: () => getTrackQuality(trackId!),
    enabled: !!trackId,
  })
}

export function useSetTrackQuality() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ trackId, quality }: { trackId: string; quality: AudioQuality | '' }) =>
      setTrackQuality(trackId, quality),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ['track-quality', vars.trackId] })
    },
  })
}

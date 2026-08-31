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

/**
 * Every per-track override in one read, plus the tier an untouched track falls
 * back to. The Manage tracks page shows a standing quality on every row, which
 * one request per track would turn into an N+1.
 */
export interface TrackQualityIndex {
  default: AudioQuality
  /** Only overridden tracks appear, keyed by library track id. */
  overrides: Record<string, AudioQuality>
}

export function getTrackQualityIndex(): Promise<TrackQualityIndex> {
  return api.get<TrackQualityIndex>('/library/track-quality')
}

export function useTrackQualityIndex() {
  return useQuery({ queryKey: ['track-quality', 'index'], queryFn: getTrackQualityIndex })
}

export interface BatchQualityResult {
  applied: number
  errors?: Record<string, string>
}

/** An empty quality clears the override on every id. */
export function setTrackQualityBatch(
  trackIds: string[],
  quality: AudioQuality | '',
): Promise<BatchQualityResult> {
  return api.post<BatchQualityResult>('/library/quality/batch', { trackIds, quality })
}

export function useSetTrackQualityBatch() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ trackIds, quality }: { trackIds: string[]; quality: AudioQuality | '' }) =>
      setTrackQualityBatch(trackIds, quality),
    // Every per-track query shares the 'track-quality' prefix, so one
    // invalidation covers the index and any single-track reads on screen.
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['track-quality'] }),
  })
}

export function useSetTrackQuality() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ trackId, quality }: { trackId: string; quality: AudioQuality | '' }) =>
      setTrackQuality(trackId, quality),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ['track-quality', vars.trackId] })
      void qc.invalidateQueries({ queryKey: ['track-quality', 'index'] })
    },
  })
}

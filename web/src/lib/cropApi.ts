import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './api'

/** Playback boundaries in ms. Zero on both means the whole file. */
export interface CropPoints {
  startMs: number
  endMs: number
}

export function getTrackCrop(trackId: string): Promise<CropPoints> {
  return api.get<CropPoints>(`/library/track/${encodeURIComponent(trackId)}/crop`)
}

export function setTrackCrop(trackId: string, points: CropPoints): Promise<CropPoints> {
  return api.put<CropPoints>(`/library/track/${encodeURIComponent(trackId)}/crop`, points)
}

/** Uncrops a track. The file was never modified, so this restores it in full. */
export function clearTrackCrop(trackId: string): Promise<CropPoints> {
  return api.del<CropPoints>(`/library/track/${encodeURIComponent(trackId)}/crop`)
}

export function useTrackCrop(trackId: string | undefined) {
  return useQuery({
    queryKey: ['track-crop', trackId ?? ''],
    queryFn: () => getTrackCrop(trackId!),
    enabled: !!trackId,
  })
}

export function useSaveTrackCrop() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ trackId, points }: { trackId: string; points: CropPoints | null }) =>
      points ? setTrackCrop(trackId, points) : clearTrackCrop(trackId),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ['track-crop', vars.trackId] })
      // Crop points ride along on every track payload, so the lists showing
      // this track need to pick up the new window too.
      void qc.invalidateQueries({ queryKey: ['library'] })
    },
  })
}

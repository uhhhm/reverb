import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './api'
import type { AudioQuality } from './audioQuality'

export interface UpgradableTrack {
  jobId: string
  source: string
  externalId: string
  artist: string
  title: string
  album: string
  quality: AudioQuality
  canonicalId?: string
  libraryTrackId?: string
}

export interface UpgradeRequest {
  source?: string
  externalId?: string
  libraryTrackId?: string
  artist: string
  title: string
  album?: string
  quality: AudioQuality
  currentQuality?: AudioQuality | ''
}

export function upgradeDownload(body: UpgradeRequest): Promise<unknown> {
  return api.post('/downloads/upgrade', body)
}

export function listUpgradable(quality?: AudioQuality): Promise<UpgradableTrack[]> {
  const qs = quality ? `?quality=${encodeURIComponent(quality)}` : ''
  return api.get<UpgradableTrack[]>(`/downloads/upgradable${qs}`)
}

export function useUpgradable(quality?: AudioQuality) {
  return useQuery({
    queryKey: ['upgradable', quality ?? ''],
    queryFn: () => listUpgradable(quality),
  })
}

export function useUpgradeDownload() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (body: UpgradeRequest) => upgradeDownload(body),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['upgradable'] })
      void queryClient.invalidateQueries({ queryKey: ['downloads'] })
    },
  })
}

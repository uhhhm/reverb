import { useQuery } from '@tanstack/react-query'
import { api } from './api'

export interface OfflineSetEntry {
  playlistId: string
  enabled: boolean
  updatedAt: number
  playlistName?: string
}

export function listOfflineSet(): Promise<OfflineSetEntry[]> {
  return api.get<OfflineSetEntry[]>('/offline-set')
}

export function setOfflineSet(playlistId: string, enabled: boolean): Promise<OfflineSetEntry> {
  return api.put<OfflineSetEntry>(`/offline-set/${encodeURIComponent(playlistId)}`, { enabled })
}

export function useOfflineSet() {
  return useQuery({
    queryKey: ['offline-set'],
    queryFn: listOfflineSet,
  })
}

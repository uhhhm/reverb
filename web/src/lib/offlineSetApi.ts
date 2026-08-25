import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
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

export function removeOfflineSet(playlistId: string): Promise<{ ok: boolean }> {
  return api.del<{ ok: boolean }>(`/offline-set/${encodeURIComponent(playlistId)}`)
}

export function useOfflineSet() {
  return useQuery({
    queryKey: ['offline-set'],
    queryFn: listOfflineSet,
  })
}

export function useSetOfflineSet() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ playlistId, enabled }: { playlistId: string; enabled: boolean }) =>
      setOfflineSet(playlistId, enabled),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['offline-set'] })
    },
  })
}

export function useRemoveOfflineSet() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (playlistId: string) => removeOfflineSet(playlistId),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['offline-set'] })
    },
  })
}

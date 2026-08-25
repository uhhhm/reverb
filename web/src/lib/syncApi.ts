import { useQuery } from '@tanstack/react-query'
import { api } from './api'

export interface SyncStatus {
  revision: number
  deviceCount: number
}

export function getSyncStatus(): Promise<SyncStatus> {
  return api.get<SyncStatus>('/sync/status')
}

export function useSyncStatus() {
  return useQuery({
    queryKey: ['sync', 'status'],
    queryFn: getSyncStatus,
  })
}

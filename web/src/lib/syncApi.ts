import { useQuery } from '@tanstack/react-query'
import { api } from './api'
import type { components } from './generated/api'

export type SyncRound = components['schemas']['SyncRound']

export interface SyncStatus {
  round?: SyncRound | null
  revision: number
  deviceCount: number
}

export function getSyncStatus(): Promise<SyncStatus> {
  return api.get<SyncStatus>('/sync/status')
}

/**
 * Kicks off one device sync round. Returns as soon as the server accepts it —
 * progress and completion are retained in getSyncStatus as well as broadcast.
 */
export function triggerSync(): Promise<{ status: string; round?: SyncRound }> {
  return api.post<{ status: string; round?: SyncRound }>('/sync/trigger', {})
}

export function useSyncStatus() {
  return useQuery({
    queryKey: ['sync', 'status'],
    queryFn: getSyncStatus,
  })
}

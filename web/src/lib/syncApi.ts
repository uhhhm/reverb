import { useQuery } from '@tanstack/react-query'
import { api } from './api'

export interface SyncStatus {
  revision: number
  deviceCount: number
}

export function getSyncStatus(): Promise<SyncStatus> {
  return api.get<SyncStatus>('/sync/status')
}

/**
 * Kicks off one device sync round. Returns as soon as the server accepts it —
 * completion arrives over the WebSocket as sync.finished.
 */
export function triggerSync(): Promise<{ status: string }> {
  return api.post<{ status: string }>('/sync/trigger', {})
}

export function useSyncStatus() {
  return useQuery({
    queryKey: ['sync', 'status'],
    queryFn: getSyncStatus,
  })
}

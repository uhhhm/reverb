import { create } from 'zustand'
import type { SyncStatus } from './syncApi'

interface SyncState {
  status: SyncStatus | null
  lastSyncAt: number | null
  /** True between sync.started and sync.finished (device anti-entropy round). */
  syncing: boolean
  setStatus: (s: SyncStatus) => void
  setSyncing: (syncing: boolean) => void
}

export const useSyncStore = create<SyncState>((set) => ({
  status: null,
  lastSyncAt: null,
  syncing: false,
  setStatus: (s) => set({ status: s, lastSyncAt: Date.now() }),
  setSyncing: (syncing) =>
    set(syncing ? { syncing: true } : { syncing: false, lastSyncAt: Date.now() }),
}))

import { create } from 'zustand'
import type { SyncStatus } from './syncApi'

interface SyncState {
  status: SyncStatus | null
  lastSyncAt: number | null
  setStatus: (s: SyncStatus) => void
}

export const useSyncStore = create<SyncState>((set) => ({
  status: null,
  lastSyncAt: null,
  setStatus: (s) => set({ status: s, lastSyncAt: Date.now() }),
}))

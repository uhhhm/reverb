import { create } from 'zustand'
import type { OfflineSetEntry } from './offlineSetApi'

interface OfflineSetState {
  entries: Record<string, OfflineSetEntry>
  setEntries: (entries: OfflineSetEntry[]) => void
  upsert: (entry: OfflineSetEntry) => void
  remove: (playlistId: string) => void
  isEnabled: (playlistId: string) => boolean
}

export const useOfflineSetStore = create<OfflineSetState>((set, get) => ({
  entries: {},
  setEntries: (entries) =>
    set({
      entries: Object.fromEntries(entries.map((e) => [e.playlistId, e])),
    }),
  upsert: (entry) =>
    set((s) => ({
      entries: { ...s.entries, [entry.playlistId]: entry },
    })),
  remove: (playlistId) =>
    set((s) => {
      const copy = { ...s.entries }
      delete copy[playlistId]
      return { entries: copy }
    }),
  isEnabled: (playlistId) => get().entries[playlistId]?.enabled ?? false,
}))

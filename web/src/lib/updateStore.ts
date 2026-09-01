import { create } from 'zustand'
import { EMPTY_UPDATE_STATE, fetchUpdateState, type UpdateState } from './updateApi'

// dismissedKey remembers the version the user said "Later" to, so the prompt
// does not reappear on every launch for a build they have already declined.
// It is per-tag: the next release prompts again.
const dismissedKey = 'reverb:updateDismissed'

function readDismissed(): string {
  try {
    return window.localStorage.getItem(dismissedKey) ?? ''
  } catch {
    return ''
  }
}

interface UpdateStore {
  state: UpdateState
  // dismissed is the tag the user postponed, '' when nothing is postponed.
  dismissed: string
  // installing is true from pressing Restart until the app goes away.
  installing: boolean
  setState: (s: UpdateState) => void
  dismiss: () => void
  setInstalling: (v: boolean) => void
  refresh: () => Promise<void>
  // shouldPrompt reports whether the restart prompt belongs on screen: a build
  // is downloaded, verified and waiting, and the user has not postponed it.
  shouldPrompt: () => boolean
}

export const useUpdateStore = create<UpdateStore>((set, get) => ({
  state: EMPTY_UPDATE_STATE,
  dismissed: typeof window === 'undefined' ? '' : readDismissed(),
  installing: false,
  setState: (state) => set({ state }),
  setInstalling: (installing) => set({ installing }),
  dismiss: () => {
    const tag = get().state.staged
    try {
      window.localStorage.setItem(dismissedKey, tag)
    } catch {
      /* a browser refusing storage only costs us the memory of the choice */
    }
    set({ dismissed: tag })
  },
  refresh: async () => {
    try {
      set({ state: await fetchUpdateState() })
    } catch {
      // Server builds have no updater and answer 503. That is not an error the
      // user needs to see; it just means there is nothing to prompt about.
    }
  },
  shouldPrompt: () => {
    const { state, dismissed, installing } = get()
    if (installing) return true
    if (!state.staged) return false
    return state.staged !== dismissed
  },
}))

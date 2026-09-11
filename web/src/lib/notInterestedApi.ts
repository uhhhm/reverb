import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './api'
import { useToastStore } from './toastStore'
import type { components } from './generated/api'

export type NotInterestedMark = components['schemas']['NotInterestedMark']
export type NotInterestedRequest = components['schemas']['NotInterestedRequest']
type NotInterestedList = components['schemas']['NotInterestedList']

const MARKS_KEY = ['not-interested']

/** Every Not interested mark, most recent first. */
export function useNotInterested() {
  return useQuery({
    queryKey: MARKS_KEY,
    queryFn: () => api.get<NotInterestedList>('/not-interested'),
  })
}

/** A mark changes what may be recommended, so every recommendation list refetches. */
function useRefreshAfterMarking() {
  const qc = useQueryClient()
  return () => {
    for (const queryKey of [MARKS_KEY, ['similar-artists'], ['similar-tracks']]) {
      void qc.invalidateQueries({ queryKey })
    }
  }
}

/** Mark a track or artist Not interested on every paired device. */
export function useMarkNotInterested() {
  const refresh = useRefreshAfterMarking()
  const pushToast = useToastStore((s) => s.push)
  return useMutation({
    mutationFn: (body: NotInterestedRequest) => api.post<NotInterestedMark>('/not-interested', body),
    onSuccess: (mark) => {
      refresh()
      pushToast(`Reverb won't recommend ${mark.kind === 'artist' ? mark.artist : mark.title || mark.artist}`, 'success')
    },
    onError: () => pushToast('Could not mark that Not interested', 'error'),
  })
}

/** Undo a mark on every paired device. */
export function useUndoNotInterested() {
  const refresh = useRefreshAfterMarking()
  return useMutation({
    mutationFn: (key: string) => api.del<null>('/not-interested', { key }),
    onSuccess: refresh,
  })
}

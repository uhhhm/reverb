import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './api'

/** Progress of the bulk loudness measurement pass. */
export interface LoudnessBackfill {
  running: boolean
  total: number
  done: number
  skipped: number
  failed: number
  error?: string
  startedAt?: number
}

export function getLoudnessBackfill(): Promise<LoudnessBackfill> {
  return api.get<LoudnessBackfill>('/library/loudness/backfill')
}

export function startLoudnessBackfill(): Promise<LoudnessBackfill> {
  return api.post<LoudnessBackfill>('/library/loudness/backfill', {})
}

export function cancelLoudnessBackfill(): Promise<LoudnessBackfill> {
  return api.post<LoudnessBackfill>('/library/loudness/backfill/cancel', {})
}

/**
 * Polls only while a pass is running. Measuring is a long job with no realtime
 * event of its own, and polling a finished pass would be pure noise.
 */
export function useLoudnessBackfill() {
  return useQuery({
    queryKey: ['loudness', 'backfill'],
    queryFn: getLoudnessBackfill,
    refetchInterval: (q) => (q.state.data?.running ? 1000 : false),
  })
}

export function useStartLoudnessBackfill() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: startLoudnessBackfill,
    onSuccess: (data) => qc.setQueryData(['loudness', 'backfill'], data),
  })
}

export function useCancelLoudnessBackfill() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: cancelLoudnessBackfill,
    onSuccess: (data) => qc.setQueryData(['loudness', 'backfill'], data),
  })
}

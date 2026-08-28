import { useQuery } from '@tanstack/react-query'

/**
 * Waveform peaks for the seek rail. Peaks are read from the file on disk, so a
 * track that isn't in the library has none — pass isExternal to skip a request
 * that can only 404 (and logs a Navidrome error each time).
 */
export function usePeaks(trackId: string | undefined, isExternal = false) {
  return useQuery({
    queryKey: ['peaks', trackId],
    queryFn: async () => {
      const response = await fetch(`/api/v1/library/track/${encodeURIComponent(trackId!)}/peaks`, { credentials: 'include' })
      if (response.status === 204) return null
      if (!response.ok) throw new Error(`peaks ${response.status}`)
      return (await response.json() as { peaks: number[] }).peaks
    },
    enabled: !!trackId && !isExternal,
    staleTime: Infinity,
    retry: false,
  })
}

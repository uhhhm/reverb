import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './api'
import type { AudioQuality } from './audioQuality'

export interface UpgradableTrack {
  jobId: string
  source: string
  externalId: string
  artist: string
  title: string
  album: string
  quality: AudioQuality
  canonicalId?: string
  libraryTrackId?: string
}

export interface UpgradeRequest {
  source?: string
  externalId?: string
  libraryTrackId?: string
  artist: string
  title: string
  album?: string
  /** Omit to use the track's standing quality (its override, else the setting). */
  quality?: AudioQuality
  currentQuality?: AudioQuality | ''
  /** Also persist quality as this track's standing override. */
  setOverride?: boolean
}

export function upgradeDownload(body: UpgradeRequest): Promise<unknown> {
  return api.post('/downloads/upgrade', body)
}

export function listUpgradable(quality?: AudioQuality): Promise<UpgradableTrack[]> {
  const qs = quality ? `?quality=${encodeURIComponent(quality)}` : ''
  return api.get<UpgradableTrack[]>(`/downloads/upgradable${qs}`)
}

export function useUpgradable(quality?: AudioQuality) {
  return useQuery({
    queryKey: ['upgradable', quality ?? ''],
    queryFn: () => listUpgradable(quality),
  })
}

/**
 * Every track Reverb can re-fetch, without the tier filter. The per-track
 * quality picker needs these: a track already at the target tier is not
 * "upgradable", but it can still be re-fetched at a different one.
 */
export function listRefetchable(): Promise<UpgradableTrack[]> {
  return api.get<UpgradableTrack[]>('/downloads/upgradable?all=1')
}

export function useRefetchable() {
  return useQuery({
    queryKey: ['upgradable', 'all'],
    queryFn: listRefetchable,
  })
}

/**
 * Finds the re-fetchable entry for a library track.
 *
 * A row that knows its library track id is matched on that. Older download
 * history predates the column, so artist and title are the fallback — the same
 * pair the download itself was keyed on. Availability deliberately does not come
 * from the track's bitrate: the sources behind both downloaders serve ~130-160
 * kbps, so a low-bitrate file usually is not evidence that a better one exists.
 */
export function findRefetchable(
  rows: UpgradableTrack[] | undefined,
  track: { id: string; artist: string; title: string },
): UpgradableTrack | undefined {
  if (!track.id || !Array.isArray(rows)) return undefined
  const byId = rows.find((u) => !!u.libraryTrackId && u.libraryTrackId === track.id)
  if (byId) return byId
  const wantArtist = track.artist.trim().toLowerCase()
  const wantTitle = track.title.trim().toLowerCase()
  return rows.find(
    (u) => u.artist.trim().toLowerCase() === wantArtist && u.title.trim().toLowerCase() === wantTitle,
  )
}

/**
 * The same match as findRefetchable, prepared once for a whole page. A list of
 * hundreds of tracks against hundreds of history rows is a quadratic scan
 * otherwise.
 */
export function buildRefetchIndex(rows: UpgradableTrack[] | undefined): Map<string, UpgradableTrack> {
  const idx = new Map<string, UpgradableTrack>()
  if (!Array.isArray(rows)) return idx
  for (const u of rows) {
    // Name keys are inserted first so an id key always wins the collision.
    const name = `n:${u.artist.trim().toLowerCase()}\u001f${u.title.trim().toLowerCase()}`
    if (!idx.has(name)) idx.set(name, u)
  }
  for (const u of rows) {
    if (u.libraryTrackId) idx.set(`i:${u.libraryTrackId}`, u)
  }
  return idx
}

/** Looks one track up in a buildRefetchIndex map. */
export function refetchFor(
  idx: Map<string, UpgradableTrack>,
  track: { id: string; artist: string; title: string },
): UpgradableTrack | undefined {
  return (
    idx.get(`i:${track.id}`) ??
    idx.get(`n:${track.artist.trim().toLowerCase()}\u001f${track.title.trim().toLowerCase()}`)
  )
}

export function useUpgradeDownload() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (body: UpgradeRequest) => upgradeDownload(body),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['upgradable'] })
      void queryClient.invalidateQueries({ queryKey: ['downloads'] })
    },
  })
}

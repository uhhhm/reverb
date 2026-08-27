import { api } from './api'
import type { AudioQuality } from './audioQuality'

export interface ResolveResult {
  kind: string
  source: string
  externalId: string
  title: string
  artist: string
  album: string
  coverUrl?: string
  url: string
}

export interface AddResult {
  resolve: ResolveResult
  job?: unknown
  playlistId?: string
  catalogId?: string
}

export function resolveLink(url: string): Promise<ResolveResult> {
  return api.post<ResolveResult>('/links/resolve', { url })
}

export function addFromLink(
  url: string,
  opts?: { playlistId?: string; download?: boolean; quality?: AudioQuality },
): Promise<AddResult> {
  const body: Record<string, unknown> = { url }
  if (opts?.playlistId !== undefined) body.playlistId = opts.playlistId
  if (opts?.download !== undefined) body.download = opts.download
  if (opts?.quality !== undefined) body.quality = opts.quality
  return api.post<AddResult>('/links/add', body)
}

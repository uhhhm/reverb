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

export interface Chapter {
  title: string
  startSec: number
  endSec: number
}

/** Per-link download options. YouTube only — see the /links/add handler. */
export interface LinkOptions {
  startTime?: string
  endTime?: string
  splitChapters?: boolean
}

export interface AddResult {
  resolve: ResolveResult
  job?: unknown
  /** Present when one link expanded into several downloads (a chapter split). */
  jobs?: unknown[]
  playlistId?: string
  catalogId?: string
}

export function resolveLink(url: string): Promise<ResolveResult> {
  return api.post<ResolveResult>('/links/resolve', { url })
}

/** Chapters of a source video, for previewing what a split would produce. */
export function listChapters(url: string): Promise<Chapter[]> {
  return api.post<Chapter[]>('/links/chapters', { url })
}

export function addFromLink(
  url: string,
  opts?: { playlistId?: string; download?: boolean; quality?: AudioQuality } & LinkOptions,
): Promise<AddResult> {
  const body: Record<string, unknown> = { url }
  if (opts?.playlistId !== undefined) body.playlistId = opts.playlistId
  if (opts?.download !== undefined) body.download = opts.download
  if (opts?.quality !== undefined) body.quality = opts.quality
  if (opts?.startTime) body.startTime = opts.startTime
  if (opts?.endTime) body.endTime = opts.endTime
  if (opts?.splitChapters) body.splitChapters = opts.splitChapters
  return api.post<AddResult>('/links/add', body)
}

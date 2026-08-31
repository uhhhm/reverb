import { API_BASE, api } from './api'
import type { TrackName } from './libraryApi'

/**
 * Bulk editing of library metadata.
 *
 * Nothing here writes to the music files. Reverb records renames and uploaded
 * artwork as display overrides and applies them when the library is read back,
 * so Navidrome and any other Subsonic client keep showing the original tags.
 */

/**
 * One track rename. A field left out keeps what the track already has, so
 * renaming only titles never clears an artist or album rename underneath.
 */
export interface TrackRenameItem extends Partial<TrackName> {
  id: string
}

export interface EntityRenameItem {
  id: string
  name: string
}

export interface BatchRenameRequest {
  tracks?: TrackRenameItem[]
  albums?: EntityRenameItem[]
  artists?: EntityRenameItem[]
}

export interface BatchResult {
  applied: number
  /** Per-item failures, keyed by id. One bad id does not discard the rest. */
  errors?: Record<string, string>
}

/** The most items one batch request accepts, matching the server's limit. */
export const BATCH_LIMIT = 500

export function renameAlbum(id: string, name: string): Promise<EntityRenameItem> {
  return api.put<EntityRenameItem>(`/library/album/${encodeURIComponent(id)}/name`, { id, name })
}

export function renameArtist(id: string, name: string): Promise<EntityRenameItem> {
  return api.put<EntityRenameItem>(`/library/artist/${encodeURIComponent(id)}/name`, { id, name })
}

export function batchRename(req: BatchRenameRequest): Promise<BatchResult> {
  return api.post<BatchResult>('/library/rename/batch', req)
}

/** What an uploaded cover can be attached to. Artists carry no artwork of their own. */
export type CoverTargetKind = 'album' | 'track'

export interface CoverTarget {
  kind: CoverTargetKind
  id: string
}

export interface CoverUploadResult extends BatchResult {
  /** The id the uploaded image is now served under, of the form `custom:<hash>.<ext>`. */
  coverArtId?: string
}

function targetParam(t: CoverTarget): string {
  return `${t.kind}:${t.id}`
}

/**
 * Uploads one image and applies it to every target. The server addresses blobs
 * by content hash, so applying one image to fifty albums stores it once —
 * which is what makes the batch case cheap.
 *
 * This goes through fetch directly rather than the JSON client: the body is
 * multipart, and letting the browser set its own boundary is the only reliable
 * way to build one.
 */
export async function uploadCovers(file: File, targets: CoverTarget[]): Promise<CoverUploadResult> {
  const form = new FormData()
  form.append('image', file)
  for (const t of targets) form.append('target', targetParam(t))
  const res = await fetch(`${API_BASE}/library/covers`, {
    method: 'POST',
    credentials: 'include',
    body: form,
  })
  const text = await res.text()
  const body = text ? (JSON.parse(text) as CoverUploadResult & { error?: string }) : null
  if (!res.ok) throw new Error(body?.error ?? `upload failed (${res.status})`)
  return body as CoverUploadResult
}

/** Removes uploaded artwork, so the library backend's own art shows again. */
export function clearCovers(targets: CoverTarget[]): Promise<BatchResult> {
  return api.del<BatchResult>('/library/covers', { targets: targets.map(targetParam) })
}

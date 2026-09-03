import type { ExternalResult, ExternalTrackRef, Track } from './types'
import { encodeExternalId } from './trackRef'

export interface ExternalTrackOpts {
  albumName?: string
  albumArtist?: string
  trackNumber?: number
  artistExternalId?: string
}

/**
 * Build a streamable Track from an ExternalTrackRef so a not-in-library row
 * plays straight from the source instead of downloading first. Mirrors the
 * synthetic Track built in Search.tsx — id is a display key, audio resolves
 * via Track.externalStream.
 */
export function externalTrackFromRef(ref: ExternalTrackRef, opts: ExternalTrackOpts = {}): Track {
  // encodeExternalId returns '' on blank source/id (mirrors trackRef); fall
  // back to a display-only key like Search does so malformed refs never
  // collide with asTrack() rows (also id '') in per-row index lookups.
  const id = encodeExternalId(ref.source, ref.externalId)
    || `ext:invalid:${ref.source}:${ref.externalId}`
  return {
    id,
    title: ref.title,
    albumId: '',
    album: ref.album ?? opts.albumName ?? '',
    artistId: '',
    artist: ref.artist ?? opts.albumArtist ?? '',
    coverArtId: '',
    trackNumber: opts.trackNumber ?? 0,
    discNumber: 0,
    durationMs: ref.durationMs,
    bitRate: 0,
    suffix: '',
    contentType: '',
    ...(ref.isrc ? { isrc: ref.isrc } : {}),
    ...(opts.artistExternalId ? { artistExternalId: opts.artistExternalId } : {}),
    externalStream: { source: ref.source, externalId: ref.externalId },
  }
}

/** Build an ExternalResult from a ref for DownloadAction / prewarm callers. */
export function externalResultFromRef(
  ref: ExternalTrackRef,
  fallbackAlbum = '',
  fallbackArtist = '',
): ExternalResult {
  return {
    source: ref.source,
    externalId: ref.externalId,
    title: ref.title,
    artist: ref.artist ?? fallbackArtist,
    album: ref.album ?? fallbackAlbum,
    durationMs: ref.durationMs,
    isrc: ref.isrc,
    type: 'track',
  }
}


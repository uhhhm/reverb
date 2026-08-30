import type { ExternalResult, Track } from './types'
import { externalStreamUrl, streamUrl } from './libraryApi'

// Separator joining dedup fields — unit separator, cannot appear in normalized text.
const SEP = '␟'

// Mirrors internal/matching/normalize.go diacriticFold.
const DIACRITIC_FOLD: Record<string, string> = {
  á: 'a', à: 'a', â: 'a', ä: 'a', ã: 'a', å: 'a', ā: 'a',
  é: 'e', è: 'e', ê: 'e', ë: 'e', ē: 'e',
  í: 'i', ì: 'i', î: 'i', ï: 'i', ī: 'i',
  ó: 'o', ò: 'o', ô: 'o', ö: 'o', õ: 'o', ø: 'o', ō: 'o',
  ú: 'u', ù: 'u', û: 'u', ü: 'u', ū: 'u',
  ç: 'c', ñ: 'n', ý: 'y', ÿ: 'y', ß: 's',
}

function foldDiacritics(s: string): string {
  let out = ''
  for (const ch of s) {
    out += DIACRITIC_FOLD[ch] ?? ch
  }
  return out
}

const featRe = /\s*[([]?\s*\b(feat\.?|featuring|ft\.?)\b.*$/i
const ptRe = /\bpt\.?\b/g
const dropRe = /[^\p{L}\p{N}\s()]+/gu
const wsRe = /\s+/g

/** Mirrors matching.Normalize: pure, deterministic, symmetric. */
export function normalize(s: string): string {
  // Mirrors internal/matching/normalize.go: ToLower then foldDiacritics so Á→á→a.
  s = s.toLowerCase()
  s = foldDiacritics(s)
  s = s.replace(featRe, '')
  s = s.replace(/&/g, ' and ')
  s = s.replace(ptRe, 'part')
  s = s.replace(dropRe, ' ')
  s = s.replace(wsRe, ' ')
  return s.trim()
}

export function encodeExternalId(source: string, externalId: string): string {
  source = source.trim()
  externalId = externalId.trim()
  if (!source || !externalId) return ''
  return `${source}:${externalId}`
}

export function decodeExternalId(id: string): { source: string; externalId: string } | null {
  id = id.trim()
  const i = id.indexOf(':')
  if (i <= 0 || i === id.length - 1) return null
  const source = id.slice(0, i).trim()
  const externalId = id.slice(i + 1).trim()
  if (!source || !externalId) return null
  if (source.includes(':') || source.includes(' ')) return null
  return { source, externalId }
}

export function isExternalId(id: string): boolean {
  return decodeExternalId(id) !== null
}

export function isExternalTrack(track: Track): boolean {
  return !!track.externalStream
}

/** Returns the correct stream URL for any track, library or external. */
export function streamUrlFor(track: Track, startMs = 0): string {
  if (track.externalStream) {
    return externalStreamUrl(
      track.externalStream.source,
      track.externalStream.externalId,
      track.artist,
      track.title,
    )
  }
  return streamUrl(track.id, startMs)
}

/**
 * Whether seeking this track has to be done by the backend rather than by the
 * browser.
 *
 * A browser seeks a file it cannot index by mapping the position onto a byte
 * offset through an assumed bitrate. For Ogg/Opus — what a downloaded track
 * usually is — that lands seconds away from where the clock then claims to be,
 * so the track plays on past its own end, or stops before it. The backend can
 * start the audio at the position instead, which it knows exactly.
 *
 * MP3 and the MP4 family carry what a browser needs to seek them itself, and
 * are left to: seeking in place keeps the original file and costs nothing.
 */
export function needsBackendSeek(track: Track): boolean {
  if (track.externalStream) return false
  const fmt = `${track.suffix ?? ''} ${track.contentType ?? ''}`.toLowerCase()
  return /ogg|opus|vorbis|webm|matroska/.test(fmt)
}

/**
 * Cross-source dedup key for an external result. Mirrors trackref.DedupKey in Go.
 * ISRC is authoritative when present (lowercased). Otherwise normalized artist+title.
 */
export function dedupKey(r: ExternalResult): string {
  const isrc = r.isrc?.trim()
  if (isrc) return `isrc:${isrc.toLowerCase()}`
  return `nf:${normalize(r.artist)}${SEP}${normalize(r.title)}`
}

/** Same key for a library track, so library and external forms compare equal. */
export function dedupKeyForTrack(t: Track): string {
  const isrc = t.isrc?.trim()
  if (isrc) return `isrc:${isrc.toLowerCase()}`
  return `nf:${normalize(t.artist)}${SEP}${normalize(t.title)}`
}

export const dedupSep = SEP

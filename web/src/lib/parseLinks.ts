/**
 * Split a paste into individual links. Users paste one per line, but also
 * space- or comma-separated, so all three are accepted. Duplicates are dropped
 * so a double-paste does not download the same track twice.
 */
export function parseLinks(raw: string): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const token of raw.split(/[\s,]+/)) {
    const t = token.trim()
    if (!t || seen.has(t)) continue
    seen.add(t)
    out.push(t)
  }
  return out
}

/**
 * True for links yt-dlp can trim or split. Only YouTube sources carry a
 * timeline Reverb can address — a Spotify track has no section to cut.
 */
export function isYouTubeLink(url: string): boolean {
  try {
    const host = new URL(url).hostname.replace(/^www\./, '')
    return host === 'youtube.com' || host === 'm.youtube.com' || host === 'music.youtube.com' || host === 'youtu.be'
  } catch {
    return false
  }
}

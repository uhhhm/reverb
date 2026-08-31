/**
 * Download quality tiers. A tier is a CEILING, not a target: the audio sources
 * behind both downloaders (YouTube / YouTube Music) serve roughly 130-160 kbps,
 * so a higher tier never invents detail — it only stops Reverb from transcoding
 * DOWN. Picking "High" on a 143 kbps source keeps the original stream untouched.
 */
export type AudioQuality = 'low' | 'medium' | 'high' | 'best'

export const AUDIO_QUALITIES: {
  value: AudioQuality
  label: string
  hint: string
  /** The tier's bitrate ceiling in kbps; null for "best", which never re-encodes. */
  kbps: number | null
}[] = [
  { value: 'low', label: 'Low', hint: 'Smallest files', kbps: 128 },
  { value: 'medium', label: 'Medium', hint: 'A middle ground', kbps: 192 },
  { value: 'high', label: 'High', hint: 'Capped by what the source actually serves', kbps: 320 },
  { value: 'best', label: 'Best', hint: 'Keeps the source audio exactly as it came', kbps: null },
]

export const DEFAULT_AUDIO_QUALITY: AudioQuality = 'high'

export function qualityLabel(q: string): string {
  return AUDIO_QUALITIES.find((x) => x.value === q)?.label ?? q
}

/**
 * The label a picker shows, carrying the tier's ceiling: "High (up to 320 kbps)".
 * A tier name alone does not say what it will produce, and the numbers are what
 * a user actually reasons about when trading quality against disk.
 */
export function qualityOptionLabel(q: string): string {
  const tier = AUDIO_QUALITIES.find((x) => x.value === q)
  if (!tier) return q
  return tier.kbps === null ? `${tier.label} (no re-encode)` : `${tier.label} (up to ${tier.kbps} kbps)`
}

/** A file's measured bitrate, or "" when the library does not report one. */
export function formatBitrate(kbps: number | undefined): string {
  return kbps && kbps > 0 ? `${Math.round(kbps)} kbps` : ''
}

/** The lowest tier that could have produced a file at this bitrate. */
export function qualityForBitrate(kbps: number): AudioQuality | '' {
  if (!kbps || kbps <= 0) return ''
  if (kbps <= 128) return 'low'
  if (kbps <= 192) return 'medium'
  return 'high'
}

const RANK: Record<AudioQuality, number> = { low: 1, medium: 2, high: 3, best: 4 }

/** True when `target` is a strictly higher tier than `current`. */
export function isUpgrade(current: AudioQuality | '', target: AudioQuality): boolean {
  if (!current) return true
  return RANK[target] > RANK[current]
}

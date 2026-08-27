/**
 * Download quality tiers. A tier is a CEILING, not a target: the audio sources
 * behind both downloaders (YouTube / YouTube Music) serve roughly 130-160 kbps,
 * so a higher tier never invents detail — it only stops Reverb from transcoding
 * DOWN. Picking "High" on a 143 kbps source keeps the original stream untouched.
 */
export type AudioQuality = 'low' | 'medium' | 'high' | 'best'

export const AUDIO_QUALITIES: { value: AudioQuality; label: string; hint: string }[] = [
  { value: 'low', label: 'Low', hint: 'Up to 128 kbps mp3 — smallest files' },
  { value: 'medium', label: 'Medium', hint: 'Up to 192 kbps mp3' },
  { value: 'high', label: 'High', hint: 'Up to 320 kbps mp3, capped by what the source serves' },
  { value: 'best', label: 'Best', hint: 'Keep the source audio as-is, never re-encoded' },
]

export const DEFAULT_AUDIO_QUALITY: AudioQuality = 'high'

export function qualityLabel(q: string): string {
  return AUDIO_QUALITIES.find((x) => x.value === q)?.label ?? q
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

import { Select } from '../ui'
import { useSettings, useUpdateSettings } from '../../lib/settingsApi'
import { AUDIO_QUALITIES, DEFAULT_AUDIO_QUALITY, type AudioQuality } from '../../lib/audioQuality'

/** Downloads tab panel — default audio quality for every new download. */
export function DownloadsSection() {
  const settings = useSettings()
  const updateSettings = useUpdateSettings()
  const current = settings.data?.downloadQuality ?? DEFAULT_AUDIO_QUALITY
  const hint = AUDIO_QUALITIES.find((q) => q.value === current)?.hint

  return (
    <div className="space-y-0 divide-y divide-border-subtle">
      <div className="flex items-start gap-5 py-5">
        <div className="min-w-0 flex-1">
          <div className="text-sm font-bold text-text-primary">Default audio quality</div>
          <div className="mt-0.5 text-xs text-text-secondary">
            Applied to every new download. A tier is a ceiling, not a target — the sources Reverb
            downloads from serve around 130–160 kbps, so a higher tier never invents detail, it just
            stops Reverb from re-encoding downwards.
          </div>
          {hint && <div className="mt-1 text-xs text-text-muted">{hint}</div>}
        </div>
        <div className="flex-none">
          <Select
            label="Default audio quality"
            value={current}
            options={AUDIO_QUALITIES.map((q) => ({ value: q.value, label: q.label }))}
            onChange={(v) => updateSettings.mutate({ downloadQuality: v as AudioQuality })}
          />
        </div>
      </div>
    </div>
  )
}

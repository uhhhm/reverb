import { Toggle } from '../ui'
import { useSettings, useUpdateSettings } from '../../lib/settingsApi'

/** Audio tab panel — playback-time loudness normalization. */
export function AudioSection() {
  const settings = useSettings()
  const updateSettings = useUpdateSettings()

  return (
    <div className="space-y-0 divide-y divide-border-subtle">
      <div className="flex items-center gap-5 py-5">
        <div className="min-w-0 flex-1">
          <div className="text-sm font-bold text-text-primary">Normalize volume</div>
          <div className="mt-0.5 text-xs text-text-secondary">
            Play every track at a similar loudness, so a quiet album doesn&apos;t disappear after a
            loud one. Reverb measures each track the first time you play it and adjusts the volume
            during playback — your files are never re-encoded, and turning this off restores the
            original loudness immediately.
          </div>
        </div>
        <div className="flex-none">
          <Toggle
            checked={settings.data?.audioNormalization ?? false}
            label="Normalize volume"
            onChange={(v) => updateSettings.mutate({ audioNormalization: v })}
          />
        </div>
      </div>
    </div>
  )
}

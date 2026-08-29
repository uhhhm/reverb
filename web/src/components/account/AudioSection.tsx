import { Toggle, Button } from '../ui'
import { useSettings, useUpdateSettings } from '../../lib/settingsApi'
import {
  useLoudnessBackfill,
  useStartLoudnessBackfill,
  useCancelLoudnessBackfill,
} from '../../lib/loudnessApi'

/** Audio tab panel — playback-time loudness normalization. */
export function AudioSection() {
  const settings = useSettings()
  const updateSettings = useUpdateSettings()
  const backfill = useLoudnessBackfill()
  const startBackfill = useStartLoudnessBackfill()
  const cancelBackfill = useCancelLoudnessBackfill()

  const state = backfill.data
  const measured = (state?.done ?? 0) + (state?.skipped ?? 0)

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

      <div className="flex items-start gap-5 py-5">
        <div className="min-w-0 flex-1">
          <div className="text-sm font-bold text-text-primary">Measure everything now</div>
          <div className="mt-0.5 text-xs text-text-secondary">
            Tracks are normally measured the first time you play them, so the very first play of a
            track is when the adjustment kicks in. Run this to measure your whole library up front
            instead. It uses a lot of CPU while it runs, tracks already measured are skipped, and
            you can stop it at any time without losing what it has done.
          </div>
          {state?.running && (
            <div role="status" className="mt-2 text-xs text-text-secondary">
              Measured {measured} of {state.total}
              {state.failed > 0 && ` · ${state.failed} could not be measured`}
            </div>
          )}
          {!state?.running && state?.error && (
            <div role="alert" className="mt-2 text-xs text-error">
              {state.error}
            </div>
          )}
          {!state?.running && !state?.error && (state?.startedAt ?? 0) > 0 && (
            <div className="mt-2 text-xs text-text-muted">
              Finished — measured {state?.done ?? 0}, skipped {state?.skipped ?? 0}
              {(state?.failed ?? 0) > 0 && `, ${state?.failed} could not be measured`}
            </div>
          )}
        </div>
        <div className="flex-none">
          {state?.running ? (
            <Button
              variant="secondary"
              size="sm"
              onClick={() => cancelBackfill.mutate()}
              disabled={cancelBackfill.isPending}
            >
              Stop
            </Button>
          ) : (
            <Button
              variant="secondary"
              size="sm"
              onClick={() => startBackfill.mutate()}
              disabled={startBackfill.isPending}
            >
              Measure library
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}

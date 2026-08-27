import { useState } from 'react'
import { useDocumentTitle } from '../lib/useDocumentTitle'
import { useSettings } from '../lib/settingsApi'
import { useUpgradable, useUpgradeDownload, type UpgradableTrack } from '../lib/upgradeApi'
import { useToastStore } from '../lib/toastStore'
import { AUDIO_QUALITIES, DEFAULT_AUDIO_QUALITY, qualityLabel, type AudioQuality } from '../lib/audioQuality'
import { Button, EmptyState, Checkbox } from '../components/ui'

/**
 * Bulk quality upgrade: every download Reverb made below the target tier, with
 * select-and-upgrade. It lists Reverb's own download history rather than the
 * whole library, because a file Reverb did not fetch has no known source to
 * re-fetch it from.
 */
export default function UpgradeQuality() {
  useDocumentTitle('Upgrade quality')
  const { data: settings } = useSettings()
  const configured = settings?.downloadQuality ?? DEFAULT_AUDIO_QUALITY
  const [target, setTarget] = useState<AudioQuality | ''>('')
  const effective = target || configured

  const { data: tracks, isLoading } = useUpgradable(effective)
  const upgrade = useUpgradeDownload()
  const pushToast = useToastStore((s) => s.push)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [queued, setQueued] = useState<Set<string>>(new Set())

  const rows = tracks ?? []
  const pending = rows.filter((t) => !queued.has(t.jobId))
  const allSelected = pending.length > 0 && pending.every((t) => selected.has(t.jobId))

  function toggle(id: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function toggleAll() {
    setSelected(allSelected ? new Set() : new Set(pending.map((t) => t.jobId)))
  }

  async function upgradeSelected() {
    const targets = pending.filter((t) => selected.has(t.jobId))
    if (targets.length === 0) return
    let ok = 0
    for (const t of targets) {
      try {
        await upgrade.mutateAsync({
          source: t.source,
          externalId: t.externalId,
          artist: t.artist,
          title: t.title,
          album: t.album,
          quality: effective,
          currentQuality: t.quality,
        })
        ok++
        setQueued((prev) => new Set(prev).add(t.jobId))
      } catch {
        // Keep going: one failure should not abandon the rest of the batch.
      }
    }
    setSelected(new Set())
    pushToast(
      ok === targets.length
        ? `Queued ${ok} upgrade${ok === 1 ? '' : 's'}`
        : `Queued ${ok} of ${targets.length} upgrades`,
      ok === targets.length ? 'success' : 'error',
    )
  }

  return (
    <div className="max-w-4xl space-y-6 pb-8">
      <header>
        <h1 className="text-3xl font-black tracking-tight text-text-primary">Upgrade quality</h1>
        <p className="mt-1 text-sm text-text-secondary">
          Downloads sitting below your target tier. Upgrading re-downloads the track and replaces the
          existing file.
        </p>
      </header>

      <div className="flex flex-wrap items-end gap-3 rounded-lg border border-border-subtle bg-raised p-4">
        <div className="space-y-1">
          <label htmlFor="target-quality" className="text-sm font-semibold text-text-primary">
            Target quality
          </label>
          <select
            id="target-quality"
            aria-label="Target quality"
            value={effective}
            onChange={(e) => setTarget(e.target.value as AudioQuality)}
            className="appearance-none rounded-md border border-border-subtle bg-input px-3 py-2 text-sm text-text-primary outline-none focus:border-accent focus:ring-1 focus:ring-accent"
          >
            {AUDIO_QUALITIES.map((q) => (
              <option key={q.value} value={q.value}>
                {q.label}
              </option>
            ))}
          </select>
        </div>
        <div className="flex-1" />
        <Button
          variant="primary"
          size="sm"
          aria-label="Upgrade selected"
          disabled={selected.size === 0 || upgrade.isPending}
          onClick={() => void upgradeSelected()}
        >
          {upgrade.isPending ? 'Queueing…' : `Upgrade selected (${selected.size})`}
        </Button>
      </div>

      {isLoading ? (
        <p className="text-sm text-text-secondary">Loading…</p>
      ) : pending.length === 0 ? (
        <EmptyState
          icon="check"
          title="Nothing to upgrade"
          hint={`Every download is already at ${qualityLabel(effective)} or better.`}
        />
      ) : (
        <div className="rounded-lg border border-border-subtle bg-raised">
          <div className="flex items-center gap-3 border-b border-border-subtle px-4 py-3">
            <Checkbox label="Select all" checked={allSelected} onChange={toggleAll} />
            <span className="text-sm font-semibold text-text-primary">
              {pending.length} below {qualityLabel(effective)}
            </span>
          </div>
          <ul>
            {pending.map((t: UpgradableTrack) => (
              <li key={t.jobId} className="flex items-center gap-3 border-b border-border-subtle px-4 py-3 last:border-b-0">
                <Checkbox
                  label={`Select ${t.title}`}
                  checked={selected.has(t.jobId)}
                  onChange={() => toggle(t.jobId)}
                />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-semibold text-text-primary">{t.title}</div>
                  <div className="truncate text-xs text-text-secondary">{t.artist}</div>
                </div>
                <span className="flex-none text-xs font-semibold text-text-muted">
                  {qualityLabel(t.quality)}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

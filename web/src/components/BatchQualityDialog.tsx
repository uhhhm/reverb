import { useState } from 'react'
import { Button, Modal } from './ui'
import { AUDIO_QUALITIES, qualityLabel, qualityOptionLabel, type AudioQuality } from '../lib/audioQuality'
import { useSetTrackQualityBatch } from '../lib/trackQualityApi'
import { useUpgradeDownload, type UpgradableTrack } from '../lib/upgradeApi'
import { useToastStore } from '../lib/toastStore'
import type { Track } from '../lib/types'

/** One selected track, paired with the download history entry that can re-fetch it. */
export interface QualitySubject {
  track: Track
  refetch?: UpgradableTrack
}

interface BatchQualityDialogProps {
  subjects: QualitySubject[] | null
  onClose: () => void
  /** Called after something was applied, so the caller can drop its selection. */
  onApplied?: () => void
}

/** The sentinel for "no override — follow the global default again". */
const FOLLOW_DEFAULT = ''

/**
 * Audio quality across a selection.
 *
 * Two separate things, because they answer different questions. The standing
 * quality is what Reverb will use whenever it fetches these tracks again, and
 * applies to every selected track. Re-downloading now replaces the files that
 * exist, and can only reach tracks Reverb fetched itself — a file it did not
 * download has no known source to fetch again.
 */
export function BatchQualityDialog({ subjects, onClose, onApplied }: BatchQualityDialogProps) {
  const [choice, setChoice] = useState<AudioQuality | typeof FOLLOW_DEFAULT>('high')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const setBatch = useSetTrackQualityBatch()
  const upgrade = useUpgradeDownload()
  const pushToast = useToastStore((s) => s.push)

  if (!subjects || subjects.length === 0) return null

  // Re-fetching at the tier a file already has would burn a download to produce
  // the same file, so those are not counted as work to do.
  const refetchable = subjects.filter((s) => s.refetch && s.refetch.quality !== choice)
  const count = subjects.length

  async function handleSave() {
    if (busy || !subjects) return
    setBusy(true)
    setError(null)
    try {
      await setBatch.mutateAsync({
        trackIds: subjects.map((s) => s.track.id),
        quality: choice,
      })
      pushToast(
        choice === FOLLOW_DEFAULT
          ? `${count} track${count === 1 ? '' : 's'} follow the default quality again`
          : `${count} track${count === 1 ? '' : 's'} set to ${qualityLabel(choice)}`,
        'success',
      )
      onApplied?.()
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't save the quality for these tracks")
      setBusy(false)
    }
  }

  async function handleRedownload() {
    if (busy || choice === FOLLOW_DEFAULT || refetchable.length === 0) return
    setBusy(true)
    setError(null)
    let ok = 0
    for (const s of refetchable) {
      try {
        await upgrade.mutateAsync({
          source: s.refetch!.source,
          externalId: s.refetch!.externalId,
          libraryTrackId: s.refetch!.libraryTrackId ?? s.track.id,
          artist: s.track.artist,
          title: s.track.title,
          album: s.track.album,
          quality: choice,
          currentQuality: s.refetch!.quality,
        })
        ok++
      } catch {
        // Keep going: one failure should not abandon the rest of the batch.
      }
    }
    pushToast(
      ok === refetchable.length
        ? `Queued ${ok} re-download${ok === 1 ? '' : 's'} at ${qualityLabel(choice)}`
        : `Queued ${ok} of ${refetchable.length} re-downloads`,
      ok === refetchable.length ? 'success' : 'error',
    )
    onApplied?.()
    onClose()
  }

  return (
    <Modal
      open
      onClose={onClose}
      testId="batch-quality-dialog"
      title={count === 1 ? 'Audio quality' : `Audio quality for ${count} tracks`}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="secondary"
            onClick={() => void handleRedownload()}
            disabled={busy || choice === FOLLOW_DEFAULT || refetchable.length === 0}
          >
            {`Re-download ${refetchable.length}`}
          </Button>
          <Button variant="primary" onClick={() => void handleSave()} disabled={busy}>
            {busy ? 'Applying…' : 'Save'}
          </Button>
        </>
      }
    >
      <fieldset className="space-y-1">
        <legend className="sr-only">Quality</legend>
        {AUDIO_QUALITIES.map((q) => (
          <label
            key={q.value}
            className="flex cursor-pointer items-start gap-2 rounded-lg px-2 py-1.5 hover:bg-raised-hover"
          >
            <input
              type="radio"
              name="batch-quality"
              value={q.value}
              checked={choice === q.value}
              onChange={() => setChoice(q.value)}
              className="mt-1"
            />
            <span className="min-w-0">
              <span className="block text-sm font-semibold text-text-primary">
                {qualityOptionLabel(q.value)}
              </span>
              <span className="block text-xs leading-snug text-text-muted">{q.hint}</span>
            </span>
          </label>
        ))}
        <label className="flex cursor-pointer items-start gap-2 rounded-lg px-2 py-1.5 hover:bg-raised-hover">
          <input
            type="radio"
            name="batch-quality"
            value=""
            checked={choice === FOLLOW_DEFAULT}
            onChange={() => setChoice(FOLLOW_DEFAULT)}
            className="mt-1"
          />
          <span className="min-w-0">
            <span className="block text-sm font-semibold text-text-primary">Follow the default</span>
            <span className="block text-xs leading-snug text-text-muted">
              Clears the per-track setting, so these tracks use whatever Settings says
            </span>
          </span>
        </label>
      </fieldset>

      <p className="text-sm text-text-secondary">
        <span className="font-semibold text-text-primary">Save</span> sets what Reverb uses the next
        time it fetches these tracks — it does not touch the files you already have.
      </p>
      <p className="text-sm text-text-secondary">
        <span className="font-semibold text-text-primary">Re-download</span> replaces the existing
        files now.{' '}
        {refetchable.length === 0
          ? 'None of the selected tracks can be re-fetched at this tier — either Reverb did not download them, or they are already at it.'
          : `${refetchable.length} of ${count} can be re-fetched; the rest are already at this tier or were not downloaded by Reverb.`}
      </p>

      {error && (
        <p role="alert" className="text-sm text-error">
          {error}
        </p>
      )}
    </Modal>
  )
}

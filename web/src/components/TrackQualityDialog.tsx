import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Button, Icon, Checkbox } from './ui'
import { AUDIO_QUALITIES, qualityLabel, qualityOptionLabel, type AudioQuality } from '../lib/audioQuality'
import { useTrackQuality, useSetTrackQuality } from '../lib/trackQualityApi'
import { useTrackUpgrade } from '../lib/useTrackUpgrade'
import { useToastStore } from '../lib/toastStore'
import type { Track } from '../lib/types'

interface Props {
  track: Track
  onClose: () => void
}

/**
 * Per-track audio quality. Two distinct things live here, because they answer
 * different questions: the standing quality (what Reverb uses whenever it
 * fetches this track again) and a re-download now at a chosen tier. The tier
 * may be lower than the current file — a deliberate downgrade to save space is
 * as valid as an upgrade.
 */
export function TrackQualityDialog({ track, onClose }: Props) {
  const panelRef = useRef<HTMLDivElement>(null)
  const { data: standing } = useTrackQuality(track.id)
  const setQuality = useSetTrackQuality()
  const refetch = useTrackUpgrade(track)
  const pushToast = useToastStore((s) => s.push)

  const [choice, setChoice] = useState<AudioQuality | ''>('')
  const [remember, setRemember] = useState(false)
  const selected: AudioQuality = choice || standing?.quality || refetch.target

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  function onSave() {
    if (!track.id) return
    setQuality.mutate(
      { trackId: track.id, quality: selected },
      {
        onSuccess: () => {
          pushToast(`“${track.title}” will use ${qualityLabel(selected)}`, 'success')
          onClose()
        },
        onError: () => pushToast('Could not save the quality for this track', 'error'),
      },
    )
  }

  function onClear() {
    if (!track.id) return
    setQuality.mutate(
      { trackId: track.id, quality: '' },
      {
        onSuccess: () => {
          pushToast(`“${track.title}” follows the default quality again`, 'success')
          onClose()
        },
        onError: () => pushToast('Could not clear the quality for this track', 'error'),
      },
    )
  }

  function onRedownload() {
    refetch.runAt(selected, { setOverride: remember })
    onClose()
  }

  return createPortal(
    <>
      <div className="fixed inset-0 z-40 bg-black/40" aria-hidden="true" onClick={onClose} />
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-label={`Audio quality for ${track.title}`}
        className="fixed left-1/2 top-1/2 z-50 w-96 max-w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 rounded-xl border border-border-subtle bg-raised shadow-pop"
      >
        <div className="flex items-center justify-between px-4 pt-4 pb-2">
          <p className="text-sm font-bold text-text-primary">Audio quality</p>
          <button
            type="button"
            aria-label="Close"
            onClick={onClose}
            className="inline-grid h-7 w-7 place-items-center rounded-lg text-text-muted transition-colors hover:bg-raised-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
          >
            <Icon name="x" className="text-sm" />
          </button>
        </div>

        <div className="px-4 pb-4 space-y-3">
          <p className="text-xs text-text-secondary">
            {refetch.current
              ? `This file was fetched at ${qualityLabel(refetch.current)}.`
              : 'The tier this file was fetched at is unknown.'}
            {standing?.overridden === false && ` Default is ${qualityLabel(standing.default)}.`}
          </p>

          <fieldset className="space-y-1">
            <legend className="sr-only">Quality</legend>
            {AUDIO_QUALITIES.map((q) => (
              <label
                key={q.value}
                className="flex cursor-pointer items-start gap-2 rounded-lg px-2 py-1.5 hover:bg-raised-hover"
              >
                <input
                  type="radio"
                  name="track-quality"
                  value={q.value}
                  checked={selected === q.value}
                  onChange={() => setChoice(q.value)}
                  className="mt-1"
                />
                <span className="min-w-0">
                  <span className="block text-sm font-semibold text-text-primary">{qualityOptionLabel(q.value)}</span>
                  <span className="block text-xs leading-snug text-text-muted">{q.hint}</span>
                </span>
              </label>
            ))}
          </fieldset>

          {refetch.available && (
            <Checkbox
              label="Always use this quality for this track"
              checked={remember}
              onChange={() => setRemember((v) => !v)}
            />
          )}

          <div className="flex flex-wrap items-center gap-2 pt-1">
            <Button variant="primary" size="sm" onClick={onSave} disabled={setQuality.isPending}>
              Save
            </Button>
            {refetch.available && (
              <Button
                variant="secondary"
                size="sm"
                onClick={onRedownload}
                disabled={refetch.isPending || refetch.current === selected}
              >
                {refetch.current === selected
                  ? 'Already at this quality'
                  : `Re-download at ${qualityLabel(selected)}`}
              </Button>
            )}
            {standing?.overridden && (
              <Button variant="ghost" size="sm" onClick={onClear} disabled={setQuality.isPending}>
                Use default
              </Button>
            )}
          </div>

          {!refetch.available && (
            <p className="text-xs text-text-muted">
              Reverb has no record of where this file came from, so it cannot re-fetch it — the
              quality above applies if it is ever downloaded again.
            </p>
          )}
        </div>
      </div>
    </>,
    document.body,
  )
}

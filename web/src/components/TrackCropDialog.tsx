import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Button, Icon } from './ui'
import { usePeaks } from '../lib/peaksApi'
import { useTrackCrop, useSaveTrackCrop } from '../lib/cropApi'
import { usePlayer } from '../lib/playerStore'
import { useToastStore } from '../lib/toastStore'
import { formatDuration } from '../lib/types'
import type { Track } from '../lib/types'

interface Props {
  track: Track
  onClose: () => void
}

type Handle = 'start' | 'end'

/**
 * Sets the part of a track that plays. Nothing is re-encoded — the boundaries
 * are stored and applied during playback, so uncropping restores the track in
 * full and a crop can be redrawn as many times as you like.
 */
export function TrackCropDialog({ track, onClose }: Props) {
  const duration = track.durationMs || 0
  const { data: saved } = useTrackCrop(track.id)
  const save = useSaveTrackCrop()
  const peaks = usePeaks(track.id).data
  const playTrackList = usePlayer((s) => s.playTrackList)
  const pushToast = useToastStore((s) => s.push)

  const railRef = useRef<HTMLDivElement>(null)
  const [startMs, setStartMs] = useState(0)
  const [endMs, setEndMs] = useState(0)
  const [loaded, setLoaded] = useState(false)

  // Adopt the stored crop once it arrives, then leave the user's edits alone.
  // Adjusting during render (rather than in an effect) avoids a frame where the
  // handles sit at 0 and the user could grab the wrong position.
  if (!loaded && saved) {
    setLoaded(true)
    setStartMs(saved.startMs)
    setEndMs(saved.endMs || duration)
  }

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  const effectiveEnd = endMs > 0 ? endMs : duration
  const pct = (ms: number) => (duration > 0 ? Math.min(100, Math.max(0, (ms / duration) * 100)) : 0)

  function msAt(clientX: number): number {
    const rect = railRef.current?.getBoundingClientRect()
    if (!rect || rect.width <= 0 || duration <= 0) return 0
    const ratio = Math.min(1, Math.max(0, (clientX - rect.left) / rect.width))
    return Math.round(ratio * duration)
  }

  // Dragging past the rail's edges has to keep tracking, so the move/up
  // listeners live on window rather than the handle.
  function startDrag(handle: Handle, e: React.MouseEvent) {
    if (duration <= 0) return
    e.preventDefault()
    const apply = (clientX: number) => {
      const ms = msAt(clientX)
      if (handle === 'start') setStartMs(Math.min(ms, effectiveEnd - 1000))
      else setEndMs(Math.max(ms, startMs + 1000))
    }
    apply(e.clientX)
    const onMove = (ev: MouseEvent) => apply(ev.clientX)
    const onUp = () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
  }

  function nudge(handle: Handle, deltaMs: number) {
    if (handle === 'start') setStartMs((v) => Math.max(0, Math.min(v + deltaMs, effectiveEnd - 1000)))
    else setEndMs((v) => Math.min(duration, Math.max(v + deltaMs, startMs + 1000)))
  }

  function onHandleKey(handle: Handle, e: React.KeyboardEvent) {
    if (e.key === 'ArrowRight') {
      e.preventDefault()
      nudge(handle, e.shiftKey ? 5000 : 500)
    } else if (e.key === 'ArrowLeft') {
      e.preventDefault()
      nudge(handle, e.shiftKey ? -5000 : -500)
    }
  }

  const cropped = startMs > 0 || effectiveEnd < duration

  function onSave() {
    save.mutate(
      {
        trackId: track.id,
        points: cropped ? { startMs, endMs: effectiveEnd >= duration ? 0 : effectiveEnd } : null,
      },
      {
        onSuccess: () => {
          pushToast(cropped ? `Cropped “${track.title}”` : `Removed the crop on “${track.title}”`, 'success')
          onClose()
        },
        onError: () => pushToast('Could not save the crop', 'error'),
      },
    )
  }

  function onUncrop() {
    save.mutate(
      { trackId: track.id, points: null },
      {
        onSuccess: () => {
          pushToast(`Removed the crop on “${track.title}”`, 'success')
          onClose()
        },
        onError: () => pushToast('Could not remove the crop', 'error'),
      },
    )
  }

  return createPortal(
    <>
      <div className="fixed inset-0 z-40 bg-black/40" aria-hidden="true" onClick={onClose} />
      <div
        role="dialog"
        aria-modal="true"
        aria-label={`Crop ${track.title}`}
        className="fixed left-1/2 top-1/2 z-50 w-[32rem] max-w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 rounded-xl border border-border-subtle bg-raised shadow-pop"
      >
        <div className="flex items-center justify-between px-4 pt-4 pb-2">
          <p className="text-sm font-bold text-text-primary">Crop track</p>
          <button
            type="button"
            aria-label="Close"
            onClick={onClose}
            className="inline-grid h-7 w-7 place-items-center rounded-lg text-text-muted transition-colors hover:bg-raised-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
          >
            <Icon name="x" className="text-sm" />
          </button>
        </div>

        <div className="space-y-4 px-4 pb-4">
          <p className="text-xs text-text-secondary">
            Choose the part that plays. Your file is never modified — you can move the handles again
            or remove the crop at any time.
          </p>

          {/* Rail with the kept window highlighted and a handle at each edge. */}
          <div ref={railRef} className="relative h-16 select-none rounded-lg bg-surface px-0">
            {peaks?.length ? (
              <div className="absolute inset-0 flex items-center gap-px px-0" data-testid="crop-waveform">
                {peaks.map((peak, i) => {
                  const at = duration * (i / peaks.length)
                  const inside = at >= startMs && at <= effectiveEnd
                  return (
                    <div
                      key={i}
                      className={inside ? 'flex-1 rounded-full bg-accent' : 'flex-1 rounded-full bg-border-subtle'}
                      style={{ minHeight: '2px', height: `${Math.max(8, peak * 100)}%` }}
                    />
                  )
                })}
              </div>
            ) : (
              <>
                <div className="absolute inset-y-6 inset-x-0 rounded bg-border-subtle" />
                <div
                  className="absolute inset-y-6 rounded bg-accent"
                  style={{ left: `${pct(startMs)}%`, width: `${Math.max(0, pct(effectiveEnd) - pct(startMs))}%` }}
                />
              </>
            )}

            <div
              role="slider"
              aria-label="Crop start"
              aria-valuemin={0}
              aria-valuemax={duration}
              aria-valuenow={startMs}
              tabIndex={0}
              onMouseDown={(e) => startDrag('start', e)}
              onKeyDown={(e) => onHandleKey('start', e)}
              style={{ left: `${pct(startMs)}%` }}
              className="absolute top-0 h-full w-2 -translate-x-1/2 cursor-ew-resize rounded bg-text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
            />
            <div
              role="slider"
              aria-label="Crop end"
              aria-valuemin={0}
              aria-valuemax={duration}
              aria-valuenow={effectiveEnd}
              tabIndex={0}
              onMouseDown={(e) => startDrag('end', e)}
              onKeyDown={(e) => onHandleKey('end', e)}
              style={{ left: `${pct(effectiveEnd)}%` }}
              className="absolute top-0 h-full w-2 -translate-x-1/2 cursor-ew-resize rounded bg-text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
            />
          </div>

          <div className="flex items-center justify-between text-xs tabular-nums text-text-secondary">
            <span>Starts at {formatDuration(startMs)}</span>
            <span>Plays {formatDuration(Math.max(0, effectiveEnd - startMs))}</span>
            <span>Ends at {formatDuration(effectiveEnd)}</span>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <Button variant="primary" size="sm" onClick={onSave} disabled={save.isPending}>
              Save crop
            </Button>
            <Button
              variant="secondary"
              size="sm"
              aria-label="Play this track"
              onClick={() => playTrackList([track], 0)}
            >
              Play
            </Button>
            {saved && (saved.startMs > 0 || saved.endMs > 0) && (
              <Button variant="ghost" size="sm" onClick={onUncrop} disabled={save.isPending}>
                Uncrop
              </Button>
            )}
          </div>
        </div>
      </div>
    </>,
    document.body,
  )
}

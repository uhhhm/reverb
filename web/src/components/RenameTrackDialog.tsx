import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Button } from './ui/Button'
import { renameTrack } from '../lib/libraryApi'
import type { Track } from '../lib/types'

interface RenameTrackDialogProps {
  /** The track to rename. Null closes the dialog. */
  track: Track | null
  onClose: () => void
}

const FOCUSABLE = 'button, [href], input, [tabindex]:not([tabindex="-1"])'

/**
 * Renames a library track. Reverb stores the new name as a display override and
 * never rewrites file tags, so the name is unchanged in Navidrome and any other
 * Subsonic client. Clearing a field restores the library's own name.
 */
export function RenameTrackDialog({ track, onClose }: RenameTrackDialogProps) {
  const panelRef = useRef<HTMLDivElement>(null)
  const qc = useQueryClient()

  const [title, setTitle] = useState('')
  const [artist, setArtist] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const open = track !== null
  const trackId = track?.id ?? ''

  // Seed the fields from the track each time the dialog opens.
  useEffect(() => {
    if (!track) return
    /* eslint-disable react-hooks/set-state-in-effect -- intentional: reset form fields when dialog reopens */
    setTitle(track.title)
    setArtist(track.artist)
    setBusy(false)
    setError(null)
    /* eslint-enable react-hooks/set-state-in-effect */
  }, [track])

  // Focus trap + Esc close
  useEffect(() => {
    if (!open) return
    const previouslyFocused = document.activeElement as HTMLElement | null
    const panel = panelRef.current
    if (panel) {
      panel.querySelectorAll<HTMLElement>(FOCUSABLE)[0]?.focus()
    }

    function handleKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        onClose()
        return
      }
      if (e.key === 'Tab' && panelRef.current) {
        const focusable = Array.from(
          panelRef.current.querySelectorAll<HTMLElement>(FOCUSABLE),
        ).filter((el) => !el.hasAttribute('disabled'))
        if (focusable.length === 0) return
        const first = focusable[0]
        const last = focusable[focusable.length - 1]
        if (e.shiftKey) {
          if (document.activeElement === first) {
            e.preventDefault()
            last.focus()
          }
        } else if (document.activeElement === last) {
          e.preventDefault()
          first.focus()
        }
      }
    }

    document.addEventListener('keydown', handleKey)
    return () => {
      document.removeEventListener('keydown', handleKey)
      previouslyFocused?.focus()
    }
  }, [open, onClose])

  if (!open) return null

  async function handleSave() {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await renameTrack(trackId, { title: title.trim(), artist: artist.trim() })
      // Every list that can show this track re-reads from the library.
      await qc.invalidateQueries({ queryKey: ['library'] })
      await qc.invalidateQueries({ queryKey: ['album-detail'] })
      await qc.invalidateQueries({ queryKey: ['synced-playlist'] })
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't rename this track")
      setBusy(false)
    }
  }

  return (
    <>
      <div
        data-testid="rename-track-backdrop"
        className="fixed inset-0 z-40 bg-canvas/80 backdrop-blur-sm"
        aria-hidden="true"
        onClick={onClose}
      />

      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="rename-track-title"
        className="fixed inset-0 z-50 flex items-center justify-center p-4"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="w-full max-w-md rounded-xl border border-border-subtle bg-raised shadow-pop animate-scale-in">
          <div className="space-y-5 p-6">
            <h2
              id="rename-track-title"
              className="text-lg font-extrabold tracking-tight text-text-primary"
            >
              Rename track
            </h2>

            <div className="space-y-1.5">
              <label htmlFor="rename-title" className="block text-sm font-semibold text-text-primary">
                Title
              </label>
              <input
                id="rename-title"
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    void handleSave()
                  }
                }}
                disabled={busy}
                className="w-full rounded-lg border border-border-subtle bg-input px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent disabled:opacity-50"
              />
            </div>

            <div className="space-y-1.5">
              <label htmlFor="rename-artist" className="block text-sm font-semibold text-text-primary">
                Artist
              </label>
              <input
                id="rename-artist"
                value={artist}
                onChange={(e) => setArtist(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    void handleSave()
                  }
                }}
                disabled={busy}
                className="w-full rounded-lg border border-border-subtle bg-input px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent disabled:opacity-50"
              />
            </div>

            <p className="text-xs text-text-muted">
              This changes the name in Reverb only — your files and Navidrome are left
              untouched. Clear a field to go back to the original name.
            </p>

            {error && (
              <p role="alert" className="text-sm text-error">
                {error}
              </p>
            )}

            <div className="flex items-center justify-end gap-3 pt-1">
              <Button variant="ghost" onClick={onClose}>
                Cancel
              </Button>
              <Button
                variant="primary"
                onClick={() => void handleSave()}
                disabled={busy}
                aria-label={busy ? 'Saving…' : 'Save name'}
              >
                {busy ? 'Saving…' : 'Save'}
              </Button>
            </div>
          </div>
        </div>
      </div>
    </>
  )
}

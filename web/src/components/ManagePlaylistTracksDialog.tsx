import { useEffect, useMemo, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Button, Cover, Icon } from './ui'
import { useSongs, coverUrl, trackCoverUrl } from '../lib/libraryApi'
import { addSyncedTrack, removeSyncedTrack } from '../lib/syncedPlaylistApi'
import type { SyncedTrackEntry } from '../lib/syncedPlaylistApi'
import { useToastStore } from '../lib/toastStore'
import type { AlbumDetailTrack, Track } from '../lib/types'

interface ManagePlaylistTracksDialogProps {
  playlistId: string
  /** Current playlist contents — library entries seed the initial selection. */
  tracks: AlbumDetailTrack[]
  onClose: () => void
}

const FOCUSABLE = 'button, [href], input, [tabindex]:not([tabindex="-1"])'

/** Library entries are keyed by the library track id; imported entries from
 *  Spotify/Deezer are not selectable here and are left untouched on save. */
function libraryIdsIn(tracks: AlbumDetailTrack[]): Set<string> {
  const ids = new Set<string>()
  for (const t of tracks) {
    if (t.key?.source === 'library' && t.key.externalId) ids.add(t.key.externalId)
  }
  return ids
}

function toEntry(track: Track): SyncedTrackEntry {
  return {
    source: 'library',
    externalId: track.id,
    title: track.title,
    artist: track.artist,
    album: track.album,
    isrc: track.isrc,
    durationMs: track.durationMs,
    coverArtId: track.coverArtId || undefined,
  }
}

function matches(track: Track, needle: string): boolean {
  return `${track.title} ${track.artist} ${track.album}`.toLowerCase().includes(needle)
}

/**
 * ManagePlaylistTracksDialog — picks the library tracks that belong in a managed
 * playlist. Rows toggle freely and nothing is written until Save, which applies
 * the difference against what the playlist held when the dialog opened.
 */
export function ManagePlaylistTracksDialog({
  playlistId,
  tracks,
  onClose,
}: ManagePlaylistTracksDialogProps) {
  const panelRef = useRef<HTMLDivElement>(null)
  const qc = useQueryClient()
  const { data: songs, isLoading } = useSongs()

  const initial = useMemo(() => libraryIdsIn(tracks), [tracks])
  const [selected, setSelected] = useState<Set<string>>(initial)
  const [query, setQuery] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Focus trap + Esc close (mirrors RenameTrackDialog).
  useEffect(() => {
    const previouslyFocused = document.activeElement as HTMLElement | null
    panelRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE)[0]?.focus()

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
  }, [onClose])

  const needle = query.trim().toLowerCase()
  const visible = useMemo(() => {
    const all = songs ?? []
    return needle ? all.filter((t) => matches(t, needle)) : all
  }, [songs, needle])

  const added = [...selected].filter((sid) => !initial.has(sid))
  const removed = [...initial].filter((sid) => !selected.has(sid))
  const dirty = added.length > 0 || removed.length > 0

  function toggle(trackId: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(trackId)) next.delete(trackId)
      else next.add(trackId)
      return next
    })
  }

  async function handleSave() {
    if (busy || !dirty) return
    setBusy(true)
    setError(null)
    const byId = new Map((songs ?? []).map((t) => [t.id, t]))
    try {
      for (const trackId of removed) {
        await removeSyncedTrack(playlistId, 'library', trackId)
      }
      // Appended in library order so the playlist gains them predictably.
      for (const trackId of added) {
        const track = byId.get(trackId)
        if (track) await addSyncedTrack(playlistId, toEntry(track))
      }
      await qc.invalidateQueries({ queryKey: ['synced-playlist', playlistId] })
      await qc.invalidateQueries({ queryKey: ['synced-playlists'] })
      onClose()
    } catch {
      setError("Couldn't save these changes — some tracks may not have been applied.")
      useToastStore.getState().push("Couldn't update this playlist", 'error')
      setBusy(false)
    }
  }

  return (
    <>
      <div
        data-testid="manage-tracks-backdrop"
        className="fixed inset-0 z-40 bg-canvas/80 backdrop-blur-sm"
        aria-hidden="true"
        onClick={onClose}
      />

      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="manage-tracks-title"
        className="fixed inset-0 z-50 flex items-center justify-center p-4"
      >
        <div className="flex max-h-[80vh] w-full max-w-lg flex-col rounded-xl border border-border-subtle bg-raised shadow-pop animate-scale-in">
          <div className="space-y-3 border-b border-border-subtle p-5">
            <h2
              id="manage-tracks-title"
              className="text-lg font-extrabold tracking-tight text-text-primary"
            >
              Manage tracks
            </h2>
            <input
              type="search"
              value={query}
              placeholder="Search your library"
              aria-label="Search your library"
              onChange={(e) => setQuery(e.target.value)}
              className="w-full rounded-lg border border-border-subtle bg-input px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
            />
          </div>

          <ul className="min-h-0 flex-1 overflow-y-auto p-2" role="list">
            {isLoading && (
              <li className="px-3 py-3 text-sm text-text-muted">Loading your library…</li>
            )}
            {!isLoading && visible.length === 0 && (
              <li className="px-3 py-3 text-sm text-text-muted">
                {needle ? 'No tracks match that search.' : 'Your library has no tracks yet.'}
              </li>
            )}
            {visible.map((track) => {
              const checked = selected.has(track.id)
              return (
                <li key={track.id}>
                  <button
                    type="button"
                    role="checkbox"
                    aria-checked={checked}
                    disabled={busy}
                    onClick={() => toggle(track.id)}
                    className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left transition-colors hover:bg-raised-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    <span
                      aria-hidden="true"
                      className={`grid h-5 w-5 flex-none place-items-center rounded border ${
                        checked
                          ? 'border-accent bg-accent text-on-accent'
                          : 'border-border-subtle text-transparent'
                      }`}
                    >
                      <Icon name="check" className="text-xs" />
                    </span>
                    <Cover
                      src={trackCoverUrl(track, 80)}
                      fallbackSrc={track.albumId ? coverUrl(track.albumId, 80) : undefined}
                      alt=""
                      size={40}
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-semibold text-text-primary">
                        {track.title}
                      </span>
                      <span className="block truncate text-xs text-text-muted">
                        {track.artist}
                        {track.album ? ` · ${track.album}` : ''}
                      </span>
                    </span>
                  </button>
                </li>
              )
            })}
          </ul>

          <div className="space-y-3 border-t border-border-subtle p-5">
            {error && (
              <p role="alert" className="text-sm text-error">
                {error}
              </p>
            )}
            <div className="flex items-center justify-between gap-3">
              <p className="text-xs text-text-muted">
                {selected.size} selected
                {dirty ? ` · +${added.length} / −${removed.length}` : ''}
              </p>
              <div className="flex items-center gap-3">
                <Button variant="ghost" onClick={onClose} disabled={busy}>
                  Cancel
                </Button>
                <Button
                  variant="primary"
                  onClick={() => void handleSave()}
                  disabled={busy || !dirty}
                  aria-label={busy ? 'Saving…' : 'Save tracks'}
                >
                  {busy ? 'Saving…' : 'Save'}
                </Button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </>
  )
}

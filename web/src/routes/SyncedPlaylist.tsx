import { useEffect, useRef, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import {
  useSyncedPlaylist,
  syncNow,
  downloadMissingForPlaylist,
  updateSyncSettings,
  deleteSyncedPlaylist,
  renameSyncedPlaylist,
  removeSyncedTrack,
  uploadPlaylistCover,
  reorderSyncedTracks,
} from '../lib/syncedPlaylistApi'
import type { TrackOrderEntry } from '../lib/syncedPlaylistApi'
import { PlaylistControls, PlaylistColumns } from '../components/PlaylistControls'
import { playlistOrder, type PlaylistSort } from '../lib/playlistOrder'
import { TrackRow } from '../components/ui/TrackRow'
import { DownloadAction } from '../components/download/DownloadAction'
import { Button, IconButton, Cover, Skeleton, EmptyState, Badge, Toggle, Select, Icon, Modal } from '../components/ui'
import { PortalMenu } from '../components/PortalMenu'
import type { AlbumDetailTrack, Track } from '../lib/types'
import { prewarmExternalStream } from '../lib/libraryApi'
import { prewarmTopResults } from '../lib/extstreamPrewarm'
import { externalResultFromRef, externalTrackFromRef } from '../lib/externalTrack'
import { usePlayer } from '../lib/playerStore'
import { useDownloads } from '../lib/downloadStore'
import { RenameTrackDialog } from '../components/RenameTrackDialog'
import { ManagePlaylistTracksDialog } from '../components/ManagePlaylistTracksDialog'
import { useToastStore } from '../lib/toastStore'
import { useAlbumPalette } from '../lib/useAlbumPalette'
import { rgbToCss } from '../lib/palette'
import { useOfflineSet, setOfflineSet } from '../lib/offlineSetApi'

// ── Local helpers ─────────────────────────────────────────────────────────────

/** Relative human-readable time from a unix timestamp (seconds). */
function relativeTime(unixSeconds: number): string {
  if (!unixSeconds) return 'Never synced'
  const diffSec = Math.floor(Date.now() / 1000) - unixSeconds
  if (diffSec < 60) return 'just now'
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return `${diffMin}m ago`
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return `${diffHr}h ago`
  const diffDays = Math.floor(diffHr / 24)
  return `${diffDays}d ago`
}

/** Build a display Track from an AlbumDetailTrack. When the row is owned, thread the
 *  matched library track's ids through so the artist + album render as clickable links
 *  and the cover resolves; otherwise these fall back to '' (plain text, no link). */
function asTrack(t: AlbumDetailTrack): Track {
  return {
    id: '',
    title: t.title,
    album: t.album ?? '',
    albumId: t.libraryTrack?.albumId ?? '',
    artist: t.artist,
    artistId: t.libraryTrack?.artistId ?? '',
    coverArtId: t.libraryTrack?.coverArtId ?? '',
    trackNumber: t.trackNumber,
    discNumber: 1,
    durationMs: t.durationMs,
    bitRate: 0,
    suffix: '',
    contentType: '',
    ...(t.artistExternalId ? { artistExternalId: t.artistExternalId } : {}),
  }
}

const INTERVAL_OPTIONS = [
  { value: '0', label: 'Manual' },
  { value: '86400', label: 'Daily' },
  { value: '604800', label: 'Weekly' },
]

// ── Component ─────────────────────────────────────────────────────────────────

export default function SyncedPlaylist() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { data: detail, isLoading, isError } = useSyncedPlaylist(id)
  const playTrackList = usePlayer((s) => s.playTrackList)
  const currentTrack = usePlayer((s) => s.current)
  const isPlaying = usePlayer((s) => s.playing)
  // Local job overlay: a track whose download is queued/running/completed is no
  // longer "missing", even before the server's coverage rollup catches up.
  const jobs = useDownloads((s) => s.jobs)
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState<PlaylistSort>('custom')
  const [bulkSubmitting, setBulkSubmitting] = useState(false)
  const [renaming, setRenaming] = useState<Track | null>(null)
  const [managingTracks, setManagingTracks] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [deleting, setDeleting] = useState(false)

  // "…" menu state
  const [menuOpen, setMenuOpen] = useState(false)
  const menuTriggerRef = useRef<HTMLDivElement>(null)

  // Inline title edit state
  const [editingName, setEditingName] = useState(false)
  const [nameInput, setNameInput] = useState('')
  const nameInputRef = useRef<HTMLInputElement>(null)
  // Set synchronously by Escape so the blur-triggered handleRename can tell a
  // cancel from a save (React state wouldn't flush before the blur fires).
  const renameCancelledRef = useRef(false)

  // Schedule settings local state — seeded from detail once loaded
  const [syncEnabled, setSyncEnabled] = useState<boolean | null>(null)
  const [intervalSec, setIntervalSec] = useState<number | null>(null)
  const [autoDownload, setAutoDownload] = useState<boolean | null>(null)

  // Cover upload state
  const coverInputRef = useRef<HTMLInputElement>(null)
  const [coverUploading, setCoverUploading] = useState(false)
  const [coverError, setCoverError] = useState<string | null>(null)

  // Drag-reorder state: optimistic local ordering of track indices
  const [trackOrder, setTrackOrder] = useState<number[] | null>(null)
  const dragSourceIdx = useRef<number | null>(null)

  // Offline set — per-playlist keep offline
  const { data: offlineSet } = useOfflineSet()
  const isOfflineEnabled = offlineSet?.find((e) => e.playlistId === id)?.enabled ?? false
  const [offlineUpdating, setOfflineUpdating] = useState(false)

  // Seed local state from detail once it loads / changes
  useEffect(() => {
    if (!detail) return
    /* eslint-disable react-hooks/set-state-in-effect -- intentional: seed local form state when server record loads */
    setSyncEnabled(detail.syncEnabled)
    setIntervalSec(detail.syncIntervalSec)
    setAutoDownload(detail.autoDownload)
    setTrackOrder(null) // reset optimistic order when playlist changes
    setQuery('')
    setSort('custom')
    /* eslint-enable react-hooks/set-state-in-effect */
    // eslint-disable-next-line react-hooks/exhaustive-deps -- intentional: re-seed only when the playlist id changes, not on every detail refresh
  }, [detail?.id])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- server membership changes invalidate positional drag indices
    setTrackOrder(null)
  }, [detail?.tracks])

  const palette = useAlbumPalette(detail?.coverUrl)

  // Resolving a not-in-library track costs seconds on the play path. Start the
  // top few as soon as the playlist appears, mirroring Search's prewarm.
  // Predicate matches the playable queue below: any row that isn't owned but
  // carries an externalRef streams (including full-but-libraryTrack-less rows).
  const streamableForPrewarm = (detail?.tracks ?? [])
    .filter((t) => !(t.state === 'full' && t.libraryTrack) && t.externalRef)
    .map((t) => t.externalRef!)
  const prewarmKey = streamableForPrewarm.slice(0, 4).map((r) => `${r.source}:${r.externalId}`).join(',')
  useEffect(() => {
    if (!detail || prewarmKey === '') return
    prewarmTopResults(
      streamableForPrewarm.slice(0, 4).map((r) => externalResultFromRef(r, detail.name, '')),
    )
    // streamableForPrewarm is rebuilt every render; prewarmKey is its stable identity.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detail?.id, prewarmKey])

  // ── Loading / error states ──────────────────────────────────────────────────

  if (isLoading) {
    return (
      <div data-testid="synced-playlist-skeleton" className="space-y-6">
        <header className="flex items-end gap-6 pt-4">
          <Skeleton className="h-52 w-52 flex-none" rounded="md" />
          <div className="flex-1 space-y-3 pb-2">
            <Skeleton className="h-3 w-24" />
            <Skeleton className="h-10 w-64" />
            <Skeleton className="h-3 w-48" />
            <Skeleton className="h-10 w-28 rounded-full" rounded="md" />
          </div>
        </header>
        <div className="space-y-1">
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="h-14 w-full" rounded="md" />
          ))}
        </div>
      </div>
    )
  }

  if (isError || !detail) {
    return (
      <EmptyState
        icon="browse"
        title="Playlist not found"
        hint="This synced playlist may have been removed."
      />
    )
  }

  // ── Derived data ────────────────────────────────────────────────────────────

  const tracks = detail.tracks ?? []

  const visibleOrder = playlistOrder(tracks, trackOrder ?? tracks.map((_, i) => i), query, sort)

  // Playable queue in track order: owned library tracks plus streamable
  // missing tracks (externalStream, no download) so Next flows across the boundary.
  const playableTracks: Track[] = visibleOrder.flatMap((i) => {
    const t = tracks[i]
    if (t.state === 'full' && t.libraryTrack) {
      return [{ ...t.libraryTrack!, ...(t.artistExternalId ? { artistExternalId: t.artistExternalId } : {}) }]
    }
    if (t.externalRef) {
      return [externalTrackFromRef(t.externalRef, {
        albumName: t.album ?? '',
        albumArtist: '',
        trackNumber: t.trackNumber,
        ...(t.artistExternalId ? { artistExternalId: t.artistExternalId } : {}),
      })]
    }
    return []
  })

  const claimedByJob = (t: (typeof tracks)[number]) => {
    if (!t.key) return false
    const job = Object.values(jobs).find(
      (j) => j.source === t.key!.source && j.externalId === t.key!.externalId,
    )
    return job?.status === 'queued' || job?.status === 'running' || job?.status === 'completed'
  }

  const missingCount = tracks.filter((t) => t.state === 'none' && !claimedByJob(t)).length

  // Playable index per row position in `tracks` order (-1 when the row has no
  // audio source). Positional, not id-keyed, so a repeated recording appearing
  // twice still plays at the row that was pressed. The queue follows the visible order, including search and sorting.
  const playableIdxByOrigRow: number[] = Array(tracks.length).fill(-1)
  {
    let pi = 0
    for (const i of visibleOrder) {
      const t = tracks[i]
      if ((t.state === 'full' && t.libraryTrack) || t.externalRef) {
        playableIdxByOrigRow[i] = pi++
      } else {
        playableIdxByOrigRow[i] = -1
      }
    }
  }

  // Resolved local settings (fall back to detail values)
  const effectiveSyncEnabled = syncEnabled ?? detail.syncEnabled
  const effectiveIntervalSec = intervalSec ?? detail.syncIntervalSec
  const effectiveAutoDownload = autoDownload ?? detail.autoDownload

  // ── Mutation helpers ────────────────────────────────────────────────────────

  async function handleSyncNow() {
    try {
      await syncNow(id)
      qc.invalidateQueries({ queryKey: ['synced-playlist', id] })
    } catch (err) {
      console.error('Sync failed:', err)
      useToastStore.getState().push("Couldn't sync this playlist", 'error')
    }
  }

  async function handleDownloadMissing() {
    if (bulkSubmitting) return
    setBulkSubmitting(true)
    try {
      await downloadMissingForPlaylist(id)
    } catch (err) {
      console.error('Download missing failed:', err)
      useToastStore.getState().push("Couldn't start downloading missing tracks", 'error')
    } finally {
      setBulkSubmitting(false)
    }
  }

  async function handleUpdateSettings(patch: { syncEnabled?: boolean; intervalSec?: number; autoDownload?: boolean }) {
    const next = {
      syncEnabled: patch.syncEnabled ?? effectiveSyncEnabled,
      intervalSec: patch.intervalSec ?? effectiveIntervalSec,
      autoDownload: patch.autoDownload ?? effectiveAutoDownload,
    }
    try {
      await updateSyncSettings(id, next)
      qc.invalidateQueries({ queryKey: ['synced-playlist', id] })
    } catch (err) {
      console.error('Failed to update sync settings:', err)
      useToastStore.getState().push("Couldn't save sync settings", 'error')
    }
  }

  async function handleOfflineToggle(next: boolean) {
    if (offlineUpdating) return
    setOfflineUpdating(true)
    try {
      await setOfflineSet(id, next)
      await qc.invalidateQueries({ queryKey: ['offline-set'] })
    } catch (err) {
      console.error('Failed to update offline set:', err)
      useToastStore.getState().push("Couldn't update offline set", 'error')
    } finally {
      setOfflineUpdating(false)
    }
  }

  async function handleRename() {
    setEditingName(false)
    if (renameCancelledRef.current) {
      renameCancelledRef.current = false
      return
    }
    const trimmed = nameInput.trim()
    if (!trimmed || trimmed === detail?.name) return
    try {
      await renameSyncedPlaylist(id, trimmed)
      void qc.invalidateQueries({ queryKey: ['synced-playlist', id] })
    } catch {
      // silent — title will revert on next render
    }
  }

  async function handleDelete() {
    if (deleting) return
    setDeleting(true)
    try {
      await deleteSyncedPlaylist(id)
      setDeleteOpen(false)
      void qc.invalidateQueries({ queryKey: ['synced-playlists'] })
      navigate('/library')
    } catch (err) {
      console.error('Failed to delete synced playlist:', err)
      useToastStore.getState().push("Couldn't delete this playlist", 'error')
    } finally {
      setDeleting(false)
    }
  }

  async function handleRemoveTrack(source: string, externalId: string) {
    try {
      await removeSyncedTrack(id, source, externalId)
      qc.invalidateQueries({ queryKey: ['synced-playlist', id] })
    } catch (err) {
      console.error('Failed to remove track:', err)
      useToastStore.getState().push("Couldn't remove that track", 'error')
    }
  }

  async function handleCoverFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    setCoverError(null)
    setCoverUploading(true)
    try {
      await uploadPlaylistCover(id, file)
      qc.invalidateQueries({ queryKey: ['synced-playlist', id] })
    } catch {
      setCoverError("Couldn't upload — try a smaller image")
    } finally {
      setCoverUploading(false)
      // Reset so the same file can be re-selected
      if (coverInputRef.current) coverInputRef.current.value = ''
    }
  }

  // Build the track order payload from current (possibly reordered) tracks
  function buildTrackOrderPayload(orderedTracks: AlbumDetailTrack[]): TrackOrderEntry[] {
    return orderedTracks
      .filter((t) => t.key)
      .map((t) => ({ source: t.key!.source, externalId: t.key!.externalId }))
  }

  function handleDragStart(idx: number) {
    dragSourceIdx.current = idx
  }

  function handleDragOver(e: React.DragEvent<HTMLDivElement>, idx: number) {
    e.preventDefault()
    const from = dragSourceIdx.current
    if (from === null || from === idx || !detail) return
    const currentOrder = trackOrder ?? tracks.map((_, i) => i)
    const next = [...currentOrder]
    const [moved] = next.splice(from, 1)
    next.splice(idx, 0, moved)
    dragSourceIdx.current = idx
    setTrackOrder(next)
  }

  async function handleDrop(e: React.DragEvent<HTMLDivElement>) {
    e.preventDefault()
    dragSourceIdx.current = null
    if (!trackOrder || !detail) return
    const orderedTracks = trackOrder.map((i) => tracks[i])
    const order = buildTrackOrderPayload(orderedTracks)
    try {
      await reorderSyncedTracks(id, order)
      qc.invalidateQueries({ queryKey: ['synced-playlist', id] })
    } catch (err) {
      console.error('Failed to reorder tracks:', err)
      useToastStore.getState().push("Couldn't save the new track order", 'error')
      // Restore server order on failure
      setTrackOrder(null)
      qc.invalidateQueries({ queryKey: ['synced-playlist', id] })
    }
  }

  function handleDragEnd() {
    dragSourceIdx.current = null
  }

  // ── Render ──────────────────────────────────────────────────────────────────

  return (
    <div className="space-y-6">
      {/* Gradient wash header */}
      <div
        className="relative -mx-4 -mt-4 px-4 pt-4 pb-6 rounded-b-2xl overflow-hidden bg-gradient-to-b from-raised to-transparent"
        style={palette ? { background: `linear-gradient(to bottom, ${rgbToCss(palette.rgb, 0.55)} 0%, transparent 100%)` } : undefined}
      >
        <header className="relative z-10 flex items-end gap-6 pt-2">
          {/* Cover — interactive (change-cover) for mode='once' */}
          <div className="relative flex-none group/cover">
            <Cover
              src={detail.coverUrl}
              alt={detail.name}
              size={208}
              rounded="md"
              className="shadow-cover"
            />
            {detail.mode === 'once' && (
              <>
                <button
                  type="button"
                  aria-label="Change cover"
                  disabled={coverUploading}
                  onClick={() => coverInputRef.current?.click()}
                  className="absolute inset-0 flex flex-col items-center justify-center gap-1 rounded-md bg-black/0 group-hover/cover:bg-black/50 transition-colors opacity-0 group-hover/cover:opacity-100 focus-visible:opacity-100 focus-visible:bg-black/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent text-white cursor-pointer disabled:cursor-wait"
                >
                  <Icon name="camera" className="text-2xl" />
                  <span className="text-xs font-semibold">
                    {coverUploading ? 'Uploading…' : 'Change cover'}
                  </span>
                </button>
                <input
                  ref={coverInputRef}
                  type="file"
                  accept="image/png,image/jpeg,image/webp"
                  className="sr-only"
                  aria-label="Upload cover image"
                  onChange={(e) => void handleCoverFileChange(e)}
                  data-testid="cover-file-input"
                />
              </>
            )}
          </div>
          {coverError && (
            <p role="alert" className="absolute bottom-2 left-0 right-0 text-center text-xs text-error">
              {coverError}
            </p>
          )}
          <div className="min-w-0 pb-1">
            <div className="flex items-center gap-2 mb-1">
              <span className="text-xs font-semibold uppercase tracking-widest text-text-muted">
                {detail.source === 'spotify' && detail.mode === 'synced' ? 'Synced playlist' : 'Playlist'}
              </span>
              {detail.source === 'spotify' && (
                <Badge kind="status" tone="success">
                  {detail.source}
                </Badge>
              )}
            </div>
            {editingName ? (
              <input
                ref={nameInputRef}
                value={nameInput}
                aria-label="Playlist name"
                className="text-4xl font-black leading-tight tracking-tight text-text-primary bg-transparent border-b border-text-primary outline-none w-full truncate"
                onChange={(e) => setNameInput(e.target.value)}
                onBlur={() => void handleRename()}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') { e.currentTarget.blur() }
                  if (e.key === 'Escape') { renameCancelledRef.current = true; e.currentTarget.blur() }
                }}
                autoFocus
              />
            ) : (
              <h1
                className="text-4xl font-black leading-tight tracking-tight text-text-primary truncate cursor-pointer hover:opacity-80 transition-opacity"
                onClick={() => {
                  setNameInput(detail.name)
                  setEditingName(true)
                }}
                title="Click to rename"
              >
                {detail.name}
              </h1>
            )}
            <div className="mt-2 text-sm text-text-secondary flex flex-wrap items-center gap-x-1">
              <span>
                {detail.source === 'local'
                  ? `${detail.totalCount} song${detail.totalCount !== 1 ? 's' : ''}`
                  : `${detail.ownedCount} of ${detail.totalCount} in library`}
              </span>
              {detail.source !== 'local' && missingCount > 0 && (
                <span className="text-accent">· {missingCount} missing</span>
              )}
            </div>
            {detail.source === 'spotify' && detail.mode === 'synced' && (
              <div className="mt-1 text-xs text-text-muted">
                Synced {relativeTime(detail.lastSyncedAt)}
              </div>
            )}
          </div>
        </header>
      </div>

      <PlaylistControls name={detail.name} disabled={playableTracks.length === 0}
        playing={isPlaying && playableTracks.some((t) => t.id === currentTrack?.id)}
        onPlay={() => playTrackList(playableTracks, 0)} query={query} onQuery={setQuery} sort={sort} onSort={setSort}>
        <button type="button" role="switch" aria-checked={isOfflineEnabled} aria-label="Keep offline" title={isOfflineEnabled ? 'Remove offline download' : 'Keep offline'} disabled={offlineUpdating}
          onClick={() => void handleOfflineToggle(!isOfflineEnabled)} className={`playlist-tool ${isOfflineEnabled ? 'text-accent' : 'text-text-secondary'}`}>
          <span className="grid h-7 w-7 place-items-center rounded-full border-2 border-current"><Icon name={isOfflineEnabled ? 'check' : 'dl'} className="h-4 w-4" /></span>
        </button>
        {detail.mode !== 'once' && <button type="button" className="playlist-tool text-text-secondary" aria-label="Sync now" title="Sync now" onClick={() => void handleSyncNow()}><Icon name="retry" className="h-5 w-5" /></button>}
              {/* "…" overflow menu — rendered via portal to escape scroll-container clip */}
              <div ref={menuTriggerRef} className="inline-flex">
                <IconButton
                  name="more"
                  label="More options"
                  onClick={() => setMenuOpen((o) => !o)}
                  aria-label="More options"
                />
              </div>
              {menuOpen && (
                <PortalMenu
                  triggerRef={menuTriggerRef}
                  onClose={() => setMenuOpen(false)}
                  label="Synced playlist options"
                  widthClass="w-72"
                >
                  <button type="button" role="menuitem" onClick={() => { setMenuOpen(false); setNameInput(detail.name); setEditingName(true) }} className="w-full px-4 py-2.5 text-left text-sm hover:bg-raised-hover">Edit playlist name</button>
                  {detail.mode === 'once' && <button type="button" role="menuitem" onClick={() => { setMenuOpen(false); setManagingTracks(true) }} className="w-full px-4 py-2.5 text-left text-sm hover:bg-raised-hover">Manage tracks</button>}
                  {/* Schedule settings panel — hidden for one-time imports */}
                  {detail.mode !== 'once' && (
                    <div className="px-4 py-3 space-y-3 border-b border-border-subtle">
                      <p className="text-xs font-semibold uppercase tracking-widest text-text-muted">
                        Schedule
                      </p>
                      <div className="flex items-center justify-between gap-3">
                        <span className="text-sm text-text-primary">Auto-sync</span>
                        <Toggle
                          checked={effectiveSyncEnabled}
                          label="Auto-sync"
                          onChange={(v) => {
                            setSyncEnabled(v)
                            void handleUpdateSettings({ syncEnabled: v })
                          }}
                        />
                      </div>
                      <div className="flex items-center justify-between gap-3">
                        <span className="text-sm text-text-primary">Interval</span>
                        <Select
                          value={String(effectiveIntervalSec)}
                          options={INTERVAL_OPTIONS}
                          label="Sync interval"
                          onChange={(v) => {
                            const sec = Number(v)
                            setIntervalSec(sec)
                            void handleUpdateSettings({ intervalSec: sec })
                          }}
                        />
                      </div>
                      <div className="flex items-center justify-between gap-3">
                        <span className="text-sm text-text-primary">Auto-download missing</span>
                        <Toggle
                          checked={effectiveAutoDownload}
                          label="Auto-download missing"
                          onChange={(v) => {
                            setAutoDownload(v)
                            void handleUpdateSettings({ autoDownload: v })
                          }}
                        />
                      </div>
                    </div>
                  )}
                  <button
                    type="button"
                    role="menuitem"
                    onClick={() => {
                      setMenuOpen(false)
                      setDeleteOpen(true)
                    }}
                    className="flex w-full items-center gap-3 rounded-b-xl px-4 py-2.5 text-sm text-error hover:bg-raised-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
                  >
                    Delete
                  </button>
                </PortalMenu>
              )}

      </PlaylistControls>
      <div className="flex flex-wrap items-center gap-2">
        {detail.mode === 'once' && <Button variant="secondary" size="sm" onClick={() => setManagingTracks(true)} aria-label="Manage tracks"><Icon name="plus" className="h-4 w-4" />Add songs</Button>}
        {missingCount > 0 && <button type="button" disabled={bulkSubmitting} onClick={() => void handleDownloadMissing()} aria-label={`Download all missing · ${missingCount}`} className="rounded-full border border-border-subtle px-3 py-1.5 text-xs font-semibold text-text-secondary transition-colors hover:border-text-muted hover:text-text-primary">{bulkSubmitting ? 'Starting downloads…' : `Download ${missingCount} missing`}</button>}
        {query && <span className="ml-auto text-xs text-text-muted" role="status">{visibleOrder.length} of {tracks.length} songs</span>}
      </div>
      {/* Track list */}
      <div className="space-y-0.5">
        <PlaylistColumns />
        {visibleOrder.map((origIdx, displayIdx) => {
          const t = tracks[origIdx]
          const owned = t.state === 'full' && t.libraryTrack
          const track = owned ? t.libraryTrack! : t.externalRef ? externalTrackFromRef(t.externalRef, { albumName: t.album ?? '', albumArtist: '', trackNumber: t.trackNumber, artistExternalId: t.artistExternalId }) : asTrack(t)
          const isActive = !!track.id && currentTrack?.id === track.id
          const isDraggable = detail.mode === 'once' && sort === 'custom' && !query.trim()
          const playableIdx = playableIdxByOrigRow[origIdx]
          return (
            <div key={`${origIdx}:${t.title}`} className="relative group" draggable={isDraggable} title={isDraggable ? 'Drag to reorder' : undefined}
              onDragStart={() => handleDragStart(displayIdx)}
              onDragOver={isDraggable ? (e) => handleDragOver(e, displayIdx) : undefined}
              onDrop={isDraggable ? (e) => void handleDrop(e) : undefined} onDragEnd={handleDragEnd}>
              <TrackRow playlist track={track} index={displayIdx} active={isActive} playing={isActive ? isPlaying : undefined}
                onPlay={() => { if (playableIdx >= 0) playTrackList(playableTracks, playableIdx) }}
                onRename={owned ? setRenaming : undefined}
                onRemove={detail.mode === 'once' && t.key ? () => void handleRemoveTrack(t.key!.source, t.key!.externalId) : undefined}
                onIntent={t.externalRef ? () => prewarmExternalStream(t.externalRef!.source, t.externalRef!.externalId, t.artist, t.title) : undefined}
                coverSrc={owned && t.libraryTrack?.coverArtId ? undefined : t.coverUrl ?? detail.coverUrl}
                artistTo={t.artistExternalId ? `/artist/spotify/${t.artistExternalId}` : undefined}
                albumTo={t.albumExternalId ? `/album/spotify/${t.albumExternalId}` : undefined}
                right={owned ? <span title="In Library" className="text-text-muted"><Icon name="check" className="h-4 w-4" /><span className="sr-only">In Library</span></span> : t.externalRef ? <DownloadAction compact result={externalResultFromRef(t.externalRef, detail.name, '')} onPlay={(libraryTrackId) => playTrackList([{ ...asTrack(t), id: libraryTrackId }], 0)} /> : undefined}
              />
            </div>
          )
        })}
        {tracks.length > 0 && visibleOrder.length === 0 && <EmptyState icon="search" title="No matching songs" hint="Try a different song, artist or album." action={<Button variant="secondary" onClick={() => setQuery('')}>Clear search</Button>} />}
        {tracks.length === 0 && (
          <EmptyState
            icon="browse"
            title="No tracks in this playlist"
            action={
              detail.mode === 'once' ? (
                <Button variant="primary" onClick={() => setManagingTracks(true)}>
                  Add tracks
                </Button>
              ) : undefined
            }
          />
        )}
      </div>

      <RenameTrackDialog track={renaming} onClose={() => setRenaming(null)} />

      <Modal
        open={deleteOpen}
        title="Delete playlist?"
        onClose={() => { if (!deleting) setDeleteOpen(false) }}
        footer={<>
          <Button disabled={deleting} onClick={() => setDeleteOpen(false)}>Cancel</Button>
          <Button variant="primary" disabled={deleting} onClick={() => void handleDelete()}>
            {deleting ? 'Deleting…' : 'Delete playlist'}
          </Button>
        </>}
      >
        <p className="text-sm text-text-secondary">
          Delete <strong className="text-text-primary">{detail.name}</strong>? This removes the playlist from every synced device. Its tracks will remain in your library.
        </p>
      </Modal>

      {managingTracks && (
        <ManagePlaylistTracksDialog
          playlistId={id}
          tracks={tracks}
          onClose={() => setManagingTracks(false)}
        />
      )}
    </div>
  )
}

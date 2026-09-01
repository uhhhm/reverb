import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAlbums, useArtists, useLibraryStatus, useSongs } from '../lib/libraryApi'
import { UploadTracksButton } from '../components/UploadTracksButton'
import { useSyncedPlaylists } from '../lib/syncedPlaylistApi'
import { Checkbox, Chip, MediaCard, Skeleton, EmptyState, Button, TrackRow } from '../components/ui'
import { ImportPlaylistDialog } from '../components/ImportPlaylistDialog'
import { RenameTrackDialog } from '../components/RenameTrackDialog'
import { BatchEditBar } from '../components/BatchEditBar'
import { BatchRenameDialog, type RenameSubject } from '../components/BatchRenameDialog'
import { CoverUploadDialog } from '../components/CoverUploadDialog'
import { SelectableCard } from '../components/SelectableCard'
import { useSelection } from '../lib/useSelection'
import type { CoverTarget } from '../lib/libraryEditApi'
import { usePlayer } from '../lib/playerStore'
import { useDocumentTitle } from '../lib/useDocumentTitle'
import type { Track } from '../lib/types'

type Filter = 'songs' | 'albums' | 'artists' | 'playlists'

const FILTERS: { key: Filter; label: string }[] = [
  { key: 'songs', label: 'Songs' },
  { key: 'albums', label: 'Albums' },
  { key: 'artists', label: 'Artists' },
  { key: 'playlists', label: 'Playlists' },
]

const SKELETON_COUNT = 10

function SkeletonGrid({ rounded = 'md' }: { rounded?: 'md' | 'full' }) {
  return (
    <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
      {Array.from({ length: SKELETON_COUNT }).map((_, i) => (
        <div key={i} className="space-y-2 p-3 rounded-lg bg-raised">
          <Skeleton
            className="aspect-square w-full"
            rounded={rounded}
            data-testid="skeleton-cover"
          />
          <Skeleton className="h-3 w-3/4" />
          <Skeleton className="h-2 w-1/2" />
        </div>
      ))}
    </div>
  )
}

function SkeletonRows() {
  return (
    <div className="space-y-2">
      {Array.from({ length: SKELETON_COUNT }).map((_, i) => (
        <div key={i} className="flex items-center gap-3 px-2.5 py-2">
          <Skeleton className="h-10 w-10 flex-none" rounded="md" data-testid="skeleton-row-cover" />
          <div className="flex-1 space-y-2">
            <Skeleton className="h-3 w-1/3" />
            <Skeleton className="h-2 w-1/4" />
          </div>
        </div>
      ))}
    </div>
  )
}

/** Distinct error state for a failed library query — separate from the "empty
 *  library" message so an outage never reads as "you have no music". */
function LibraryError({ onRetry }: { onRetry: () => void }) {
  return (
    <EmptyState
      icon="warn"
      title="Couldn't load your library"
      hint="Something went wrong reaching the server. Check your connection and try again."
      action={
        <Button size="sm" variant="secondary" onClick={onRetry}>
          Retry
        </Button>
      }
    />
  )
}

export default function Library() {
  useDocumentTitle('Library')
  const [filter, setFilter] = useState<Filter>('songs')
  const [importOpen, setImportOpen] = useState(false)
  const [renaming, setRenaming] = useState<Track | null>(null)
  // Selection is per filter: the ids in a song selection mean nothing on the
  // albums tab, so switching tabs drops it.
  const [selecting, setSelecting] = useState(false)
  const selection = useSelection()
  const [batchSubject, setBatchSubject] = useState<RenameSubject | null>(null)
  const [coverTargets, setCoverTargets] = useState<CoverTarget[] | null>(null)
  const navigate = useNavigate()
  const playTrackList = usePlayer((s) => s.playTrackList)
  const currentTrack = usePlayer((s) => s.current)
  const isPlaying = usePlayer((s) => s.playing)

  const songs = useSongs()
  const albums = useAlbums('newest')
  const artists = useArtists()
  const syncedPlaylists = useSyncedPlaylists()
  const libStatus = useLibraryStatus()

  const selectableTab = filter === 'songs' || filter === 'albums' || filter === 'artists'
  const songList = songs.data ?? []
  const albumList = albums.data ?? []
  const artistList = artists.data ?? []

  function leaveSelection() {
    selection.clear()
    setSelecting(false)
  }

  function switchFilter(next: Filter) {
    setFilter(next)
    leaveSelection()
  }

  /** The current tab's items, so one bar can serve all three grids. */
  function currentSubject(): RenameSubject {
    if (filter === 'albums') return { kind: 'albums', items: selection.selectedFrom(albumList) }
    if (filter === 'artists') return { kind: 'artists', items: selection.selectedFrom(artistList) }
    return { kind: 'tracks', items: selection.selectedFrom(songList) }
  }

  function currentCoverTargets(): CoverTarget[] {
    const kind = filter === 'albums' ? 'album' : 'track'
    return [...selection.ids].map((id) => ({ kind, id }) as CoverTarget)
  }

  function selectAllOnTab() {
    const all = filter === 'albums' ? albumList : filter === 'artists' ? artistList : songList
    selection.selectAll(all.map((i) => i.id))
  }

  return (
    <div className="space-y-6">
      {/* Library status banners */}
      {libStatus.data?.state === 'starting' && (
        <div className="rounded-lg border border-border-subtle bg-raised px-4 py-2 text-sm text-text-secondary">
          Library starting… the bundled music server is coming up.
        </div>
      )}
      {libStatus.data?.state === 'degraded' && (
        <div className="rounded-lg border border-error/40 bg-error/10 px-4 py-2 text-sm text-error">
          Library unavailable — the bundled server failed to start. Check logs or switch to an external server in Settings.
        </div>
      )}

      {/* Page header */}
      <div className="flex items-center justify-between gap-3">
        <h1 className="text-2xl font-bold text-text-primary">Your Library</h1>
        <div className="flex items-center gap-2">
          {/* Collection is a lens on the library (what you're missing), so it
              lives here rather than as a standalone rail entry. */}
          <Button
            size="sm"
            variant="ghost"
            aria-label="Open collection"
            onClick={() => navigate('/collection')}
          >
            Collection
          </Button>
          {/* Uploading writes into the managed music directory, which only
              exists with the built-in library. */}
          {libStatus.data?.mode === 'built-in' && <UploadTracksButton />}
          {selectableTab && (
            <Button
              size="sm"
              variant="ghost"
              aria-label={selecting ? 'Leave selection mode' : 'Select multiple items'}
              onClick={() => (selecting ? leaveSelection() : setSelecting(true))}
            >
              {selecting ? 'Done' : 'Select'}
            </Button>
          )}
          <Button
            size="sm"
            variant="secondary"
            aria-label="Import from Spotify"
            onClick={() => setImportOpen(true)}
          >
            + Import from Spotify
          </Button>
        </div>
      </div>

      {/* Filter chips */}
      <div className="flex gap-2 flex-wrap" role="group" aria-label="Library filter">
        {FILTERS.map(({ key, label }) => (
          <Chip
            key={key}
            selected={filter === key}
            onClick={() => switchFilter(key)}
          >
            {label}
          </Chip>
        ))}
      </div>

      {/* Songs list */}
      {filter === 'songs' && (
        <>
          {songs.isLoading ? (
            <SkeletonRows />
          ) : songs.isError ? (
            <LibraryError onRetry={() => void songs.refetch()} />
          ) : (songs.data ?? []).length === 0 ? (
            <EmptyState
              icon="music"
              title="Nothing here yet"
              hint="Download some music to start building your library."
            />
          ) : (
            <div>
              {songList.map((t, i) => (
                <div key={t.id} className="flex items-center gap-3">
                  {selecting && (
                    <Checkbox
                      checked={selection.has(t.id)}
                      onChange={() => selection.toggle(t.id)}
                      label={`Select ${t.title}`}
                    />
                  )}
                  <div className="min-w-0 flex-1">
                    <TrackRow
                      track={t}
                      index={i}
                      active={currentTrack?.id === t.id}
                      playing={currentTrack?.id === t.id ? isPlaying : undefined}
                      onPlay={() => playTrackList(songList, i)}
                      onRename={setRenaming}
                    />
                  </div>
                </div>
              ))}
            </div>
          )}
        </>
      )}

      {/* Albums grid */}
      {filter === 'albums' && (
        <>
          {albums.isLoading ? (
            <SkeletonGrid rounded="md" />
          ) : albums.isError ? (
            <LibraryError onRetry={() => void albums.refetch()} />
          ) : (albums.data ?? []).length === 0 ? (
            <EmptyState
              icon="browse"
              title="Nothing here yet"
              hint="Download some music to start building your library."
            />
          ) : (
            <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
              {albumList.map((al) => (
                <SelectableCard
                  key={al.id}
                  selecting={selecting}
                  selected={selection.has(al.id)}
                  onToggle={() => selection.toggle(al.id)}
                  label={al.name}
                >
                  <MediaCard
                    title={al.name}
                    subtitle={al.artist}
                    coverId={al.coverArtId || undefined}
                    rounded="md"
                    onClick={() => navigate(`/album/library/${al.id}`)}
                  />
                </SelectableCard>
              ))}
            </div>
          )}
        </>
      )}

      {/* Artists grid */}
      {filter === 'artists' && (
        <>
          {artists.isLoading ? (
            <SkeletonGrid rounded="full" />
          ) : artists.isError ? (
            <LibraryError onRetry={() => void artists.refetch()} />
          ) : (artists.data ?? []).length === 0 ? (
            <EmptyState
              icon="browse"
              title="Nothing here yet"
              hint="Download some music to start building your library."
            />
          ) : (
            <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
              {artistList.map((ar) => (
                <SelectableCard
                  key={ar.id}
                  selecting={selecting}
                  selected={selection.has(ar.id)}
                  onToggle={() => selection.toggle(ar.id)}
                  label={ar.name}
                >
                  <MediaCard
                    title={ar.name}
                    coverId={ar.coverArtId || undefined}
                    rounded="full"
                    onClick={() => navigate(`/artist/library/${ar.id}`)}
                  />
                </SelectableCard>
              ))}
            </div>
          )}
        </>
      )}

      {/* Playlists grid — all managed playlists */}
      {filter === 'playlists' && (
        <>
          {syncedPlaylists.isLoading ? (
            <SkeletonGrid rounded="md" />
          ) : syncedPlaylists.isError ? (
            <LibraryError onRetry={() => void syncedPlaylists.refetch()} />
          ) : (syncedPlaylists.data ?? []).length === 0 ? (
            <EmptyState
              icon="browse"
              title="Nothing here yet"
              hint="Create a playlist or download some music to get started."
            />
          ) : (
            <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
              {(syncedPlaylists.data ?? []).map((pl) => (
                <MediaCard
                  key={pl.id}
                  title={pl.name}
                  subtitle={`${pl.trackCount} track${pl.trackCount !== 1 ? 's' : ''}`}
                  coverSrc={pl.coverUrl}
                  rounded="md"
                  onClick={() => navigate(`/playlist/${pl.id}`)}
                />
              ))}
            </div>
          )}
        </>
      )}

      {selecting && (
        <BatchEditBar
          count={selection.count}
          canSetCover={filter !== 'artists'}
          onRename={() => setBatchSubject(currentSubject())}
          onSetCover={() => setCoverTargets(currentCoverTargets())}
          onSelectAll={selectAllOnTab}
          onClear={selection.clear}
        />
      )}

      <ImportPlaylistDialog open={importOpen} onClose={() => setImportOpen(false)} />
      <RenameTrackDialog track={renaming} onClose={() => setRenaming(null)} />
      <BatchRenameDialog
        subject={batchSubject}
        onClose={() => setBatchSubject(null)}
        onApplied={leaveSelection}
      />
      {/* Mounted only while open, so its file choice starts fresh each time. */}
      {coverTargets && (
        <CoverUploadDialog
          targets={coverTargets}
          onClose={() => setCoverTargets(null)}
          onApplied={leaveSelection}
        />
      )}
    </div>
  )
}

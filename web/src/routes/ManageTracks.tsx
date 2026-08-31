import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAlbums, useArtists, useSongs, coverUrl } from '../lib/libraryApi'
import { useTrackQualityIndex } from '../lib/trackQualityApi'
import { buildRefetchIndex, refetchFor, useRefetchable } from '../lib/upgradeApi'
import { formatBitrate, qualityForBitrate, qualityLabel } from '../lib/audioQuality'
import { selectionHandlers, useSelection, type Selection } from '../lib/useSelection'
import { useSyncedPlaylist, useSyncedPlaylists } from '../lib/syncedPlaylistApi'
import { useDocumentTitle } from '../lib/useDocumentTitle'
import { Checkbox, Chip, Cover, EmptyState, Skeleton, MediaCard } from '../components/ui'
import { SelectableCard } from '../components/SelectableCard'
import { BatchEditBar } from '../components/BatchEditBar'
import { BatchRenameDialog, type RenameSubject } from '../components/BatchRenameDialog'
import { BatchQualityDialog, type QualitySubject } from '../components/BatchQualityDialog'
import { CoverUploadDialog } from '../components/CoverUploadDialog'
import type { CoverTarget } from '../lib/libraryEditApi'
import type { Track } from '../lib/types'

type Tab = 'tracks' | 'albums' | 'artists'

const TABS: { key: Tab; label: string }[] = [
  { key: 'tracks', label: 'Tracks' },
  { key: 'albums', label: 'Albums' },
  { key: 'artists', label: 'Artists' },
]

/**
 * One page for everything you can change about your library without touching the
 * files: names, cover art, and audio quality.
 *
 * These used to be three surfaces — batch editing on the Library page, a bulk
 * quality page, and a per-track quality dialog — which meant deciding what to
 * change and then going somewhere else to change it. Here a track's real bitrate
 * sits next to its tier and its standing quality, so the decision and the action
 * are in the same place.
 *
 * Nothing here rewrites audio files or their tags. Renames and artwork are
 * display overrides; quality is what Reverb uses the next time it fetches a
 * track, and re-downloading is always an explicit, separate action.
 */
export default function ManageTracks() {
  useDocumentTitle('Manage tracks')
  const navigate = useNavigate()
  const [tab, setTab] = useState<Tab>('tracks')
  const [query, setQuery] = useState('')
  const [playlistId, setPlaylistId] = useState('')
  const selection = useSelection()

  const [renameSubject, setRenameSubject] = useState<RenameSubject | null>(null)
  const [qualitySubjects, setQualitySubjects] = useState<QualitySubject[] | null>(null)
  const [coverTargets, setCoverTargets] = useState<CoverTarget[] | null>(null)

  const songs = useSongs()
  const albums = useAlbums('newest')
  const artists = useArtists()
  const quality = useTrackQualityIndex()
  const refetchable = useRefetchable()
  const playlists = useSyncedPlaylists()
  const playlist = useSyncedPlaylist(playlistId)

  // A playlist names its tracks; the albums and artists it covers are whatever
  // those tracks belong to. Only owned entries carry a library track — the rest
  // describe something the library does not have, so there is nothing to manage.
  const playlistScope = useMemo(() => {
    if (!playlistId) return null
    const tracks = (playlist.data?.tracks ?? [])
      .map((row) => row.libraryTrack)
      .filter((t): t is Track => !!t?.id)
    return {
      trackIds: new Set(tracks.map((t) => t.id)),
      albumIds: new Set(tracks.map((t) => t.albumId).filter(Boolean)),
      artistIds: new Set(tracks.map((t) => t.artistId).filter(Boolean)),
    }
  }, [playlistId, playlist.data])

  // One pass over the download history, reused by every row.
  const refetchIndex = useMemo(() => buildRefetchIndex(refetchable.data), [refetchable.data])

  // Filtering is client-side because the lists are already fully loaded; a
  // selection is by id, so it survives the filter narrowing under it.
  const needle = query.trim().toLowerCase()
  const shownTracks = useMemo(() => {
    let all = songs.data ?? []
    if (playlistScope) all = all.filter((t) => playlistScope.trackIds.has(t.id))
    return needle
      ? all.filter((t) => `${t.title} ${t.artist} ${t.album}`.toLowerCase().includes(needle))
      : all
  }, [songs.data, needle, playlistScope])
  const shownAlbums = useMemo(() => {
    let all = albums.data ?? []
    if (playlistScope) all = all.filter((a) => playlistScope.albumIds.has(a.id))
    return needle ? all.filter((a) => `${a.name} ${a.artist}`.toLowerCase().includes(needle)) : all
  }, [albums.data, needle, playlistScope])
  const shownArtists = useMemo(() => {
    let all = artists.data ?? []
    if (playlistScope) all = all.filter((a) => playlistScope.artistIds.has(a.id))
    return needle ? all.filter((a) => a.name.toLowerCase().includes(needle)) : all
  }, [artists.data, needle, playlistScope])

  // Ranges and sweeps need the order the user sees, which is the filtered list.
  const orderedIds = useMemo(
    () =>
      (tab === 'albums' ? shownAlbums : tab === 'artists' ? shownArtists : shownTracks).map(
        (i) => i.id,
      ),
    [tab, shownTracks, shownAlbums, shownArtists],
  )

  function switchTab(next: Tab) {
    // Ids from a track selection mean nothing on the albums tab.
    setTab(next)
    selection.clear()
  }

  function renameSubjectForTab(): RenameSubject {
    if (tab === 'albums') return { kind: 'albums', items: selection.selectedFrom(shownAlbums) }
    if (tab === 'artists') return { kind: 'artists', items: selection.selectedFrom(shownArtists) }
    return { kind: 'tracks', items: selection.selectedFrom(shownTracks) }
  }

  function coverTargetsForTab(): CoverTarget[] {
    const kind = tab === 'albums' ? 'album' : 'track'
    return [...selection.ids].map((id) => ({ kind, id }) as CoverTarget)
  }

  function qualitySubjectsForTab(): QualitySubject[] {
    return selection
      .selectedFrom(shownTracks)
      .map((track) => ({ track, refetch: refetchFor(refetchIndex, track) }))
  }

  function selectAllOnTab() {
    const all = tab === 'albums' ? shownAlbums : tab === 'artists' ? shownArtists : shownTracks
    selection.selectAll(all.map((i) => i.id))
  }

  const loading =
    (tab === 'tracks' && songs.isLoading) ||
    (tab === 'albums' && albums.isLoading) ||
    (tab === 'artists' && artists.isLoading)

  return (
    <div className="max-w-5xl space-y-6 pb-24">
      <header>
        <h1 className="text-3xl font-black tracking-tight text-text-primary">Manage tracks</h1>
        <p className="mt-1 text-sm text-text-secondary">
          Rename, set cover art, and choose audio quality. Names and artwork are shown in Reverb
          only — your files and Navidrome keep their original tags.
        </p>
      </header>

      <div className="flex flex-wrap items-center gap-3">
        <div role="tablist" aria-label="What to manage" className="flex gap-2">
          {TABS.map((t) => (
            <Chip key={t.key} selected={tab === t.key} onClick={() => switchTab(t.key)}>
              {t.label}
            </Chip>
          ))}
        </div>
        <div className="flex-1" />
        <label htmlFor="playlist-filter" className="text-sm text-text-secondary">
          Playlist
        </label>
        <select
          id="playlist-filter"
          value={playlistId}
          onChange={(e) => {
            // The narrowed list is a different set of rows, so a selection made
            // against the old one no longer describes what is on screen.
            setPlaylistId(e.target.value)
            selection.clear()
          }}
          className="rounded-lg border border-border-subtle bg-input px-3 py-2 text-sm text-text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
        >
          <option value="">All tracks</option>
          {(playlists.data ?? []).map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
        <input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          aria-label={`Filter ${tab}`}
          placeholder={`Filter ${tab}…`}
          className="w-56 rounded-lg border border-border-subtle bg-input px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
        />
      </div>

      {loading ? (
        <Skeleton className="h-64 w-full rounded-lg" />
      ) : tab === 'tracks' ? (
        shownTracks.length === 0 ? (
          <EmptyState
            icon="browse"
            title={needle ? 'No tracks match' : 'No tracks'}
            hint={needle ? 'Try a different filter.' : undefined}
          />
        ) : (
          <div
            className={[
              'overflow-hidden rounded-lg border border-border-subtle bg-raised',
              // A sweep is a drag, and without this the browser starts selecting
              // the row text instead.
              selection.dragging ? 'select-none' : '',
            ].join(' ')}
          >
            <ul>
              {shownTracks.map((t) => (
                <TrackManageRow
                  key={t.id}
                  track={t}
                  selected={selection.has(t.id)}
                  onToggle={() => selection.toggle(t.id)}
                  gestures={selectionHandlers(selection, t.id, orderedIds)}
                  standing={quality.data?.overrides[t.id]}
                  fallback={quality.data?.default}
                  fetchedAt={refetchFor(refetchIndex, t)?.quality}
                  refetchable={!!refetchFor(refetchIndex, t)}
                />
              ))}
            </ul>
          </div>
        )
      ) : tab === 'albums' ? (
        shownAlbums.length === 0 ? (
          <EmptyState icon="browse" title={needle ? 'No albums match' : 'No albums'} />
        ) : (
          <div className={gridClass(selection)}>
            {shownAlbums.map((al) => (
              <SelectableCard
                key={al.id}
                selecting
                selected={selection.has(al.id)}
                onToggle={() => selection.toggle(al.id)}
                gestures={selectionHandlers(selection, al.id, orderedIds)}
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
        )
      ) : shownArtists.length === 0 ? (
        <EmptyState icon="browse" title={needle ? 'No artists match' : 'No artists'} />
      ) : (
        <div className={gridClass(selection)}>
          {shownArtists.map((ar) => (
            <SelectableCard
              key={ar.id}
              selecting
              selected={selection.has(ar.id)}
              onToggle={() => selection.toggle(ar.id)}
              gestures={selectionHandlers(selection, ar.id, orderedIds)}
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

      <BatchEditBar
        count={selection.count}
        canSetCover={tab !== 'artists'}
        canSetQuality={tab === 'tracks'}
        onRename={() => setRenameSubject(renameSubjectForTab())}
        onSetCover={() => setCoverTargets(coverTargetsForTab())}
        onSetQuality={() => setQualitySubjects(qualitySubjectsForTab())}
        onSelectAll={selectAllOnTab}
        onClear={selection.clear}
      />

      <BatchRenameDialog
        subject={renameSubject}
        onClose={() => setRenameSubject(null)}
        onApplied={selection.clear}
      />
      <BatchQualityDialog
        subjects={qualitySubjects}
        onClose={() => setQualitySubjects(null)}
        onApplied={selection.clear}
      />
      {/* Mounted only while open, so its file choice starts fresh each time. */}
      {coverTargets && (
        <CoverUploadDialog
          targets={coverTargets}
          onClose={() => setCoverTargets(null)}
          onApplied={selection.clear}
        />
      )}
    </div>
  )
}

/** The card grid, with text selection suppressed while a sweep is in progress. */
function gridClass(selection: Selection): string {
  return [
    'grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5',
    selection.dragging ? 'select-none' : '',
  ].join(' ')
}

interface TrackManageRowProps {
  track: Track
  selected: boolean
  onToggle: () => void
  /** Range and sweep handlers from selectionHandlers, spread onto the row. */
  gestures: ReturnType<typeof selectionHandlers>
  /** The per-track override, when the track has one. */
  standing?: string
  /** The global setting a track with no override falls back to. */
  fallback?: string
  /** The tier the existing file was fetched at, from download history. */
  fetchedAt?: string
  refetchable: boolean
}

/**
 * One track row. The quality column answers "what do I have" and "what will I
 * get" side by side: the file's measured bitrate with the tier that bitrate
 * implies, then the standing quality the next fetch would use.
 */
function TrackManageRow({
  track,
  selected,
  onToggle,
  gestures,
  standing,
  fallback,
  fetchedAt,
  refetchable,
}: TrackManageRowProps) {
  const bitrate = formatBitrate(track.bitRate)
  // The tier from download history is what Reverb actually asked for; the tier
  // implied by the bitrate is a guess, and only worth showing without one.
  const currentTier = fetchedAt || qualityForBitrate(track.bitRate)
  const next = standing || fallback

  return (
    <li
      {...gestures}
      className="flex items-center gap-3 border-b border-border-subtle px-4 py-3 last:border-b-0"
    >
      <Checkbox checked={selected} onChange={onToggle} label={`Select ${track.title}`} />
      <Cover
        src={track.coverArtId ? coverUrl(track.coverArtId, 80) : undefined}
        alt=""
        size={40}
        className="flex-none"
      />
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm font-semibold text-text-primary">{track.title}</div>
        <div className="truncate text-xs text-text-secondary">
          {track.artist}
          {track.album ? ` — ${track.album}` : ''}
        </div>
      </div>
      <div className="flex-none text-right">
        <div className="text-xs font-semibold text-text-primary">
          {bitrate || 'Bitrate unknown'}
          {currentTier ? ` · ${qualityLabel(currentTier)}` : ''}
        </div>
        <div className="text-xs text-text-muted">
          {next ? `Next fetch: ${qualityLabel(next)}` : ''}
          {standing ? '' : next ? ' (default)' : ''}
          {refetchable ? '' : ' · not re-fetchable'}
        </div>
      </div>
    </li>
  )
}

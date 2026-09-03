import { useState, useEffect, useMemo } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useAlbumDetail } from '../lib/coverageApi'
import { coverUrl, prewarmExternalStream } from '../lib/libraryApi'
import { prewarmTopResults } from '../lib/extstreamPrewarm'
import { externalResultFromRef, externalTrackFromRef } from '../lib/externalTrack'
import { TrackRow } from '../components/ui/TrackRow'
import { DownloadAction } from '../components/download/DownloadAction'
import { postBatchDownload } from '../lib/downloadApi'
import { formatDuration } from '../lib/types'
import type { AlbumDetailTrack, ExternalTrackRef, Track } from '../lib/types'
import { usePlayer } from '../lib/playerStore'
import { Button, IconButton, Cover, Skeleton, EmptyState, Badge, Icon } from '../components/ui'
import { useAlbumPalette } from '../lib/useAlbumPalette'
import { rgbToCss } from '../lib/palette'
import * as statsApi from '../lib/statsApi'
import type { EntityStats, PlayCountTrack } from '../lib/statsApi'
import { presetRange, msToHuman } from '../lib/range'
import { RenameTrackDialog } from '../components/RenameTrackDialog'
import { RenameEntityDialog } from '../components/RenameEntityDialog'
import { CoverUploadDialog } from '../components/CoverUploadDialog'
import { useDocumentTitle } from '../lib/useDocumentTitle'

// ── Local helpers ─────────────────────────────────────────────────────────────

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

// ── Component ─────────────────────────────────────────────────────────────────

export default function Album() {
  const { source = 'library', id = '' } = useParams()
  const { data: album, isLoading, isError } = useAlbumDetail(source, id)
  useDocumentTitle(album ? `${album.name} · ${album.artist}` : 'Album')
  const playTrackList = usePlayer((s) => s.playTrackList)
  const toggleShuffle = usePlayer((s) => s.toggleShuffle)
  const shuffle = usePlayer((s) => s.shuffle)
  const currentTrack = usePlayer((s) => s.current)
  const isPlaying = usePlayer((s) => s.playing)
  const palette = useAlbumPalette(album?.coverArtId ? coverUrl(album.coverArtId, 300) : album?.coverUrl)
  const [bulkSubmitting, setBulkSubmitting] = useState(false)
  const [renaming, setRenaming] = useState<Track | null>(null)
  // Album-level editing only means anything for an album Reverb actually has;
  // a Spotify album page is describing something in someone else's catalogue.
  const [renamingAlbum, setRenamingAlbum] = useState(false)
  const [settingCover, setSettingCover] = useState(false)

  // ── Listening-history stats ──────────────────────────────────────────────────
  // Hooks must run on every render (before the loading/error early returns), so
  // their inputs are derived defensively from the (possibly undefined) album.

  // Aggregate stat strip — fetched once the album name + artist are known. Album
  // titles collide across artists, so the artist is REQUIRED server-side.
  const [entityStats, setEntityStats] = useState<EntityStats | null>(null)
  const albumName = album?.name
  const albumArtistName = album?.artist
  useEffect(() => {
    if (!albumName || !albumArtistName) return
    statsApi
      .entity('album', albumName, presetRange('all'), albumArtistName)
      .then(setEntityStats)
      .catch(() => setEntityStats(null))
  }, [albumName, albumArtistName])

  // Per-track play counts — keyed on the owned tracks' library ids so the
  // "Played N×" caption can render. Lookup-only (never-played → 0).
  const playCountTracks = useMemo<PlayCountTrack[]>(
    () =>
      (album?.tracks ?? [])
        .filter((t) => t.state === 'full' && t.libraryTrack)
        .map((t) => ({
          key: t.libraryTrack!.id,
          title: t.title,
          artist: t.artist,
          album: t.album ?? album?.name ?? '',
          durationMs: t.durationMs,
          ...(t.libraryTrack!.isrc ? { isrc: t.libraryTrack!.isrc } : {}),
        })),
    [album?.tracks, album?.name],
  )
  const playCountKeys = playCountTracks.map((t) => t.key).join(',')
  const { data: playCounts } = useQuery({
    queryKey: ['album-play-counts', source, id, playCountKeys],
    queryFn: () => statsApi.playCounts(playCountTracks),
    enabled: playCountTracks.length > 0,
  })

  // Resolving a not-in-library track costs seconds on the play path. Start the
  // top few as soon as the album appears, mirroring Search's prewarm.
  // Predicate matches the playable queue below: any row that isn't owned but
  // carries an externalRef streams (including full-but-libraryTrack-less rows).
  const streamableRefs = (album?.tracks ?? [])
    .filter((t) => !(t.state === 'full' && t.libraryTrack) && t.externalRef)
    .map((t) => t.externalRef!)
  const prewarmKey = streamableRefs.slice(0, 4).map((r) => `${r.source}:${r.externalId}`).join(',')
  useEffect(() => {
    if (!album || prewarmKey === '') return
    prewarmTopResults(
      streamableRefs.slice(0, 4).map((r) => externalResultFromRef(r, album.name, album.artist)),
    )
    // streamableRefs is rebuilt every render; prewarmKey is its stable identity.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [album?.source, album?.id, prewarmKey])

  if (isLoading) {
    return (
      <div data-testid="album-skeleton" className="space-y-6">
        {/* Header skeleton */}
        <header className="flex items-end gap-6 pt-4">
          <Skeleton className="h-52 w-52 flex-none" rounded="md" />
          <div className="flex-1 space-y-3 pb-2">
            <Skeleton className="h-3 w-12" />
            <Skeleton className="h-10 w-64" />
            <Skeleton className="h-3 w-48" />
            <Skeleton className="h-10 w-28 rounded-full" rounded="md" />
          </div>
        </header>
        {/* Track row skeletons */}
        <div className="space-y-1">
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="h-14 w-full" rounded="md" />
          ))}
        </div>
      </div>
    )
  }

  if (isError || !album) {
    return (
      <EmptyState
        icon="browse"
        title="Album not found"
        hint="This album may have been removed from your library."
      />
    )
  }

  // ── Derived data ────────────────────────────────────────────────────────────

  // Playable queue in track order: owned library tracks plus streamable
  // missing tracks (externalStream, no download). Header Play/Shuffle and
  // per-row onPlay all index into this so Next flows across the boundary.
  const playableTracks: Track[] = album.tracks.flatMap((t) => {
    if (t.state === 'full' && t.libraryTrack) {
      return [{ ...t.libraryTrack!, ...(t.artistExternalId ? { artistExternalId: t.artistExternalId } : {}) }]
    }
    if (t.externalRef) {
      return [externalTrackFromRef(t.externalRef, {
        albumName: album.name,
        albumArtist: album.artist,
        trackNumber: t.trackNumber,
        ...(t.artistExternalId ? { artistExternalId: t.artistExternalId } : {}),
      })]
    }
    return []
  })

  // Missing externalRefs for batch download
  const missingRefs: ExternalTrackRef[] = album.tracks
    .filter((t) => t.state === 'none' && t.externalRef)
    .map((t) => t.externalRef!)

  const hasMissing = album.ownedCount < album.totalCount

  // Playable index per row position (parallel to album.tracks; -1 when the row
  // has no audio source). Positional, not id-keyed, so a repeated recording
  // appearing twice still plays at the row that was pressed.
  const playableIdxByRow: number[] = []
  {
    let pi = 0
    for (const t of album.tracks) {
      if ((t.state === 'full' && t.libraryTrack) || t.externalRef) {
        playableIdxByRow.push(pi++)
      } else {
        playableIdxByRow.push(-1)
      }
    }
  }

  // Cover source: prefer coverArtId proxy, fall back to direct coverUrl
  const coverSrc = album.coverArtId ? coverUrl(album.coverArtId, 300) : album.coverUrl

  // Total duration: sum across all tracks (owned + missing)
  const totalDurationMs = album.tracks.reduce((acc, t) => acc + t.durationMs, 0)

  return (
    <div className="space-y-6">
      {/* Subtle gradient wash behind header */}
      <div
        className="relative -mx-4 -mt-4 px-4 pt-4 pb-6 rounded-b-2xl overflow-hidden bg-gradient-to-b from-raised to-transparent"
        style={palette ? { background: `linear-gradient(to bottom, ${rgbToCss(palette.rgb, 0.55)} 0%, transparent 100%)` } : undefined}
      >
        <header className="relative z-10 flex items-end gap-6 pt-2">
          <Cover
            src={coverSrc}
            alt={album.name}
            size={208}
            rounded="md"
            className="shadow-cover flex-none"
          />
          <div className="min-w-0 pb-1">
            <div className="text-xs font-semibold uppercase tracking-widest text-text-muted mb-1">
              Album
            </div>
            <h1 className="text-4xl font-black leading-tight tracking-tight text-text-primary truncate">
              {album.name}
            </h1>
            <div className="mt-2 text-sm text-text-secondary flex flex-wrap items-center gap-x-1">
              {album.artistId ? (
                <Link
                  to={`/artist/library/${album.artistId}`}
                  className="font-semibold text-text-primary hover:text-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent rounded"
                >
                  {album.artist}
                </Link>
              ) : (
                <span className="font-semibold text-text-primary">{album.artist}</span>
              )}
              {album.year ? <span>· {album.year}</span> : null}
              {album.totalCount ? <span>· {album.totalCount} songs</span> : null}
              {totalDurationMs > 0 ? <span>· {formatDuration(totalDurationMs)}</span> : null}
              {hasMissing ? (
                <span className="text-accent">· {album.ownedCount} of {album.totalCount} in library</span>
              ) : null}
            </div>

            {/* Listening-history stat strip — hidden when Plays === 0 / no history */}
            {entityStats !== null && entityStats.Plays > 0 && (
              <p
                className="mt-1 text-xs text-text-secondary flex flex-wrap items-center gap-x-1"
                data-testid="album-stat-strip"
              >
                <span>{entityStats.Plays} plays</span>
                <span className="text-text-muted">·</span>
                <span>{msToHuman(entityStats.MsPlayed)} listened</span>
                {entityStats.FirstPlayed > 0 && (
                  <>
                    <span className="text-text-muted">·</span>
                    <span>since {new Date(entityStats.FirstPlayed * 1000).getFullYear()}</span>
                  </>
                )}
              </p>
            )}

            <div className="mt-4 flex items-center gap-3">
              <Button
                variant="primary"
                size="md"
                disabled={playableTracks.length === 0}
                onClick={() => playableTracks.length && playTrackList(playableTracks, 0)}
                aria-label={`Play ${album.name}`}
              >
                Play
              </Button>
              <IconButton
                name="shuffle"
                label={`Shuffle ${album.name}`}
                onClick={() => {
                  if (!playableTracks.length) return
                  if (!shuffle) toggleShuffle()
                  playTrackList(playableTracks, 0)
                }}
                disabled={playableTracks.length === 0}
              />
              {source === 'library' && id && (
                <>
                  <Button
                    variant="ghost"
                    size="md"
                    onClick={() => setRenamingAlbum(true)}
                    aria-label={`Rename ${album.name}`}
                  >
                    Rename
                  </Button>
                  <Button
                    variant="ghost"
                    size="md"
                    onClick={() => setSettingCover(true)}
                    aria-label={`Set cover art for ${album.name}`}
                  >
                    Set cover
                  </Button>
                </>
              )}
              {/* Acquisition action — one-click download of the missing tracks. */}
              {hasMissing && (
                <Button
                  variant="secondary"
                  size="md"
                  disabled={bulkSubmitting}
                  onClick={() => {
                    setBulkSubmitting(true)
                    void postBatchDownload(missingRefs).finally(() => setBulkSubmitting(false))
                  }}
                  aria-label={`Download missing · ${missingRefs.length}`}
                >
                  {bulkSubmitting ? 'Starting downloads…' : `Download missing · ${missingRefs.length}`}
                </Button>
              )}
            </div>
          </div>
        </header>
      </div>

      {/* Track list */}
      <div className="space-y-0.5">
        {album.tracks.map((t, i) => {
          if (t.state === 'full' && t.libraryTrack) {
            const playableIdx = playableIdxByRow[i] ?? 0
            const isActive = currentTrack?.id === t.libraryTrack.id
            const playCount = playCounts?.[t.libraryTrack.id] ?? 0
            return (
              <TrackRow
                key={`${t.libraryTrack.id}:${i}`}
                track={t.libraryTrack}
                index={i}
                active={isActive}
                playing={isActive ? isPlaying : undefined}
                onPlay={() => playTrackList(playableTracks, playableIdx)}
                onRename={setRenaming}
                coverSrc={t.libraryTrack.coverArtId ? undefined : t.coverUrl}
                artistTo={t.artistExternalId ? `/artist/spotify/${t.artistExternalId}` : undefined}
                albumTo={t.albumExternalId ? `/album/spotify/${t.albumExternalId}` : undefined}
                caption={playCount > 0 ? `Played ${playCount}×` : undefined}
                right={
                  <Badge kind="in-library">
                    <Icon name="check" className="text-xs" />
                    In Library
                  </Badge>
                }
                rightWidth="120px"
              />
            )
          }

          // Missing (or partial/pending with an externalRef): stream straight
          // from the source — no download. Rows without an externalRef stay
          // non-playable so no track ever silently vanishes from the list.
          if (!t.externalRef) {
            const displayTrack = asTrack(t)
            return (
              <TrackRow
                key={t.libraryTrack ? `${t.libraryTrack.id}:${i}` : `pending:${i}`}
                track={displayTrack}
                index={i}
                onPlay={() => {}}
                coverSrc={t.coverUrl ?? album.coverUrl}
                right={undefined}
                rightWidth={undefined}
              />
            )
          }
          const ref = t.externalRef
          const displayTrack = externalTrackFromRef(ref, {
            albumName: album.name,
            albumArtist: album.artist,
            trackNumber: t.trackNumber,
            ...(t.artistExternalId ? { artistExternalId: t.artistExternalId } : {}),
          })
          const playableIdx = playableIdxByRow[i] ?? 0
          const isActive = currentTrack?.id === displayTrack.id
          const right = (
            <DownloadAction
              result={externalResultFromRef(ref, album.name, album.artist)}
              onPlay={(libraryTrackId) => playTrackList([{ ...asTrack(t), id: libraryTrackId }], 0)}
            />
          )
          return (
            <TrackRow
              key={`${displayTrack.id}:${i}`}
              track={displayTrack}
              index={i}
              active={isActive}
              playing={isActive ? isPlaying : undefined}
              onPlay={() => playTrackList(playableTracks, playableIdx)}
              onIntent={() => {
                prewarmExternalStream(ref.source, ref.externalId, displayTrack.artist, displayTrack.title)
              }}
              coverSrc={t.coverUrl ?? album.coverUrl}
              artistTo={t.artistExternalId ? `/artist/spotify/${t.artistExternalId}` : undefined}
              albumTo={t.albumExternalId ? `/album/spotify/${t.albumExternalId}` : undefined}
              right={right}
              rightWidth="120px"
            />
          )
        })}
        {album.tracks.length === 0 && (
          <EmptyState icon="browse" title="No tracks in this album" />
        )}
      </div>

      <RenameTrackDialog track={renaming} onClose={() => setRenaming(null)} />
      {renamingAlbum && (
        <RenameEntityDialog
          kind="album"
          id={id}
          currentName={album.name}
          onClose={() => setRenamingAlbum(false)}
        />
      )}
      {settingCover && (
        <CoverUploadDialog
          targets={[{ kind: 'album', id }]}
          onClose={() => setSettingCover(false)}
        />
      )}
    </div>
  )
}

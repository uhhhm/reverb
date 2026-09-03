import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { useExternalPlaylist } from '../lib/syncedPlaylistApi'
import { prewarmExternalStream } from '../lib/libraryApi'
import { prewarmTopResults } from '../lib/extstreamPrewarm'
import { usePlayer } from '../lib/playerStore'
import type { ExternalResult, Track } from '../lib/types'
import { ImportPlaylistDialog } from '../components/ImportPlaylistDialog'
import { DownloadAction } from '../components/download/DownloadAction'
import { Button, Cover, EmptyState, Skeleton, TrackRow } from '../components/ui'

function displayTrack(result: ExternalResult): Track {
  return {
    id: `${result.source}:${result.externalId}`,
    title: result.title,
    artist: result.artist,
    artistId: '',
    album: result.album,
    albumId: '',
    coverArtId: result.coverArtId ?? '',
    trackNumber: 0,
    discNumber: 0,
    durationMs: result.durationMs,
    bitRate: 0,
    suffix: '',
    contentType: '',
    isrc: result.isrc,
    // Preview streams straight from the source — importing/downloading is separate.
    externalStream: { source: result.source, externalId: result.externalId },
  }
}

export default function ExternalPlaylist() {
  const { source = '', id = '' } = useParams()
  const { data: playlist, isLoading, isError } = useExternalPlaylist(source, id)
  const [importOpen, setImportOpen] = useState(false)
  const playTrackList = usePlayer((s) => s.playTrackList)
  const currentTrack = usePlayer((s) => s.current)
  const isPlaying = usePlayer((s) => s.playing)

  // Resolving a preview costs seconds on the play path. Start the top few as
  // soon as the playlist appears, mirroring Search's prewarm.
  const prewarmKey = (playlist?.tracks ?? []).slice(0, 4).map((t) => `${t.source}:${t.externalId}`).join(',')
  useEffect(() => {
    if (prewarmKey === '') return
    prewarmTopResults((playlist?.tracks ?? []).slice(0, 4))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [prewarmKey])

  if (isLoading) return <div className="space-y-3" aria-label="Loading playlist"><Skeleton className="h-52 w-52" />{Array.from({ length: 8 }).map((_, i) => <Skeleton key={i} className="h-14 w-full" />)}</div>
  if (isError || !playlist) return <EmptyState icon="browse" title="Playlist not found" hint="Check that Spotify is configured and the playlist is public." />

  const url = `https://open.spotify.com/playlist/${playlist.externalId}`
  const playableTracks = playlist.tracks.map(displayTrack)
  return (
    <div className="space-y-6">
      <header className="flex items-end gap-5 pt-3">
        <Cover src={playlist.coverUrl} alt={playlist.name} className="h-40 w-40 flex-none shadow-pop sm:h-52 sm:w-52" />
        <div className="min-w-0 space-y-3">
          <p className="text-xs font-bold uppercase tracking-wider text-text-muted">Spotify playlist</p>
          <h1 className="truncate text-3xl font-extrabold text-text-primary sm:text-5xl">{playlist.name}</h1>
          <p className="text-sm text-text-secondary">{playlist.tracks.length} songs · Inspect tracks before importing</p>
          <div className="flex items-center gap-3">
            <Button
              variant="primary"
              disabled={playableTracks.length === 0}
              onClick={() => playableTracks.length && playTrackList(playableTracks, 0)}
              aria-label={`Play ${playlist.name}`}
            >
              Play
            </Button>
            <Button variant="secondary" onClick={() => setImportOpen(true)}>Import playlist</Button>
          </div>
        </div>
      </header>
      <section aria-label="Songs">
        <div className="space-y-0.5">
          {playlist.tracks.map((track, index) => {
            const dt = displayTrack(track)
            const isActive = currentTrack?.id === dt.id
            return (
              <TrackRow
                key={`${track.source}:${track.externalId}:${index}`}
                track={dt}
                index={index}
                active={isActive}
                playing={isActive ? isPlaying : undefined}
                coverSrc={track.coverUrl || undefined}
                rightWidth="8.5rem"
                onPlay={() => {
                  playTrackList(playableTracks, index)
                }}
                onIntent={() => {
                  prewarmExternalStream(track.source, track.externalId, track.artist, track.title)
                }}
                right={<DownloadAction result={track} onPlay={(libraryTrackId) => playTrackList([{ ...dt, id: libraryTrackId, externalStream: undefined }], 0)} />}
              />
            )
          })}
        </div>
      </section>
      <ImportPlaylistDialog open={importOpen} onClose={() => setImportOpen(false)} initialURL={url} />
    </div>
  )
}

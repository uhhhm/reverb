import { useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { Button, EmptyState, Icon, Skeleton, TrackRow } from '../components/ui'
import { DownloadAction } from '../components/download/DownloadAction'
import { usePlayer } from '../lib/playerStore'
import {
  mixDescription,
  mixTitle,
  reasonText,
  recommendedTrackToTrack,
  saveMixAsPlaylist,
  useMix,
  type MixKind,
} from '../lib/recommendationsApi'
import type { Track } from '../lib/types'
import { useDocumentTitle } from '../lib/useDocumentTitle'

function shuffled(tracks: Track[]): Track[] {
  const out = [...tracks]
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1))
    ;[out[i], out[j]] = [out[j], out[i]]
  }
  return out
}

/**
 * One Mix: a regenerated list of recommendations, not a playlist. Saving it
 * makes an ordinary playlist copy, which is what can go offline.
 */
export default function MixPage() {
  const { kind: param = '' } = useParams()
  const kind = (param === 'releaseRadar' ? 'releaseRadar' : 'discoverWeekly') as MixKind
  const known = param === 'discoverWeekly' || param === 'releaseRadar'
  useDocumentTitle(mixTitle(kind))
  const { data, isLoading } = useMix(kind)
  const navigate = useNavigate()
  const qc = useQueryClient()
  const playTrackList = usePlayer((s) => s.playTrackList)
  const currentTrackId = usePlayer((s) => s.current?.id)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState(false)

  const results = useMemo(() => data?.tracks ?? [], [data])
  const tracks = useMemo(() => results.map((r) => recommendedTrackToTrack(r, 'mix')), [results])

  async function handleSave() {
    setSaving(true)
    setSaveError(false)
    try {
      const playlist = await saveMixAsPlaylist(kind)
      void qc.invalidateQueries({ queryKey: ['synced-playlists'] })
      navigate(`/playlist/${playlist.id}`)
    } catch {
      setSaveError(true)
    } finally {
      setSaving(false)
    }
  }

  if (!known) return <EmptyState icon="browse" title="Mix not found" />

  return (
    <div className="space-y-6 pb-8">
      <header>
        <div className="text-xs font-semibold uppercase tracking-widest text-text-muted mb-1">Mix</div>
        <h1 className="text-4xl font-black tracking-tight text-text-primary">{mixTitle(kind)}</h1>
        <p className="mt-2 text-sm text-text-secondary">{mixDescription(kind)}</p>
        {data?.updatedAt ? (
          <p className="mt-1 text-xs text-text-muted">
            {data.offline ? 'Offline · last generated ' : 'Updated '}
            {new Date(data.updatedAt * 1000).toLocaleDateString()}
          </p>
        ) : null}
        {data?.refreshing && <p className="mt-1 text-xs text-text-muted" role="status">Refreshing…</p>}
      </header>

      {tracks.length > 0 && (
        <div className="flex flex-wrap items-center gap-3">
          <Button variant="primary" onClick={() => playTrackList(tracks, 0)} aria-label={`Play ${mixTitle(kind)}`}>
            <Icon name="play" className="w-5 h-5 mr-1" />
            Play
          </Button>
          <Button variant="secondary" onClick={() => playTrackList(shuffled(tracks), 0)} aria-label="Shuffle">
            Shuffle
          </Button>
          <Button variant="secondary" disabled={saving} onClick={() => void handleSave()} aria-label="Save as playlist">
            {saving ? 'Saving…' : 'Save as playlist'}
          </Button>
          {saveError && <span className="text-xs text-text-muted" role="alert">Couldn't save the playlist.</span>}
        </div>
      )}

      {isLoading || (tracks.length === 0 && data?.refreshing) ? (
        <div className="space-y-2">
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="h-12 w-full" rounded="md" />
          ))}
        </div>
      ) : tracks.length === 0 ? (
        <EmptyState
          icon="browse"
          title={kind === 'releaseRadar' ? 'No new releases this week' : 'Nothing here yet'}
          hint={data && !data.available ? 'Online recommendations are off or no source is set up.' : 'Play some music and check back after the next refresh.'}
        />
      ) : (
        <div className="space-y-0.5">
          {tracks.map((track, i) => {
            const r = results[i]
            return (
              <TrackRow
                key={`${r.source}:${r.externalId}`}
                track={track}
                index={i}
                active={currentTrackId === track.id}
                coverSrc={r.coverUrl || undefined}
                artistTo={r.source !== 'library' && r.artistExternalId ? `/artist/${r.source}/${r.artistExternalId}` : undefined}
                caption={reasonText(r.reason)}
                right={r.source !== 'library' ? <DownloadAction compact result={{ source: r.source, externalId: r.externalId, title: r.title, artist: r.artist, album: r.album, durationMs: r.durationMs, isrc: r.isrc, mbid: r.mbid, coverUrl: r.coverUrl, coverArtId: r.coverArtId, artistExternalId: r.artistExternalId, albumExternalId: r.albumExternalId, type: 'track', recommendationOrigin: 'mix' }} /> : undefined}
                onPlay={() => playTrackList(tracks, i)}
              />
            )
          })}
        </div>
      )}
    </div>
  )
}

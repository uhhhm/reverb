import { useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { EmptyState, Skeleton, TrackRow } from '../components/ui'
import { usePlayer } from '../lib/playerStore'
import { recommendedTrackToTrack, useSimilarTracks } from '../lib/recommendationsApi'
import { useDocumentTitle } from '../lib/useDocumentTitle'

/**
 * Tracks similar to a seed, each already matched to something playable: the
 * library copy when owned, otherwise a search result that streams from its
 * source. Reached from a track's actions menu.
 */
export default function SimilarTracks() {
  const [params] = useSearchParams()
  const artist = params.get('artist') ?? ''
  const title = params.get('title') ?? ''
  useDocumentTitle(title ? `Similar to ${title}` : 'Similar tracks')
  const { data, isLoading } = useSimilarTracks(artist, title)
  const playTrackList = usePlayer((s) => s.playTrackList)
  const currentTrackId = usePlayer((s) => s.current?.id)

  const results = useMemo(() => data?.tracks ?? [], [data])
  const tracks = useMemo(() => results.map(recommendedTrackToTrack), [results])

  return (
    <div className="space-y-6 pb-8">
      <header>
        <div className="text-xs font-semibold uppercase tracking-widest text-text-muted mb-1">Similar tracks</div>
        <h1 className="text-3xl font-black tracking-tight text-text-primary truncate">{title || 'Similar tracks'}</h1>
        {artist && <p className="mt-1 text-sm text-text-secondary">{artist}</p>}
      </header>

      {isLoading ? (
        <div className="space-y-2">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-12 w-full" rounded="md" />
          ))}
        </div>
      ) : data && !data.available ? (
        <EmptyState
          icon="browse"
          title="Similar tracks aren't set up"
          hint="They come from Last.fm — add a Last.fm API key in Admin → Integrations."
        />
      ) : tracks.length === 0 ? (
        <EmptyState icon="browse" title="No similar tracks found" hint="Nothing similar could be matched to something playable." />
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
                onPlay={() => playTrackList(tracks, i)}
              />
            )
          })}
        </div>
      )}
    </div>
  )
}

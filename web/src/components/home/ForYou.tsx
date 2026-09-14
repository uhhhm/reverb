import { useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { Carousel, MediaCard, Skeleton } from '../ui'
import { usePlayer } from '../../lib/playerStore'
import {
  mixTitle,
  reasonText,
  recommendedTrackToTrack,
  shelfTitle,
  useMixes,
  useShelves,
  type Shelf,
} from '../../lib/recommendationsApi'

function updatedText(updatedAt?: number): string {
  return updatedAt ? ` · updated ${new Date(updatedAt * 1000).toLocaleString()}` : ''
}

/**
 * Reserves a shelf's height while the first shelves are generated, so they
 * replace it in place rather than pushing the page down when they land.
 */
function ShelfSkeleton() {
  return (
    <section className="mb-8" aria-busy="true" data-testid="for-you-loading">
      <Skeleton className="h-7 w-56 mb-4" />
      <div className="grid grid-flow-col gap-4 overflow-x-auto pb-2" style={{ gridAutoColumns: '160px' }}>
        {Array.from({ length: 5 }).map((_, i) => (
          <div key={i} className="p-3 rounded-lg bg-raised">
            <Skeleton className="w-full aspect-square mb-3 rounded-md" />
            <Skeleton className="h-3.5 w-3/4 mb-2" />
            <Skeleton className="h-3 w-1/2" />
          </div>
        ))}
      </div>
    </section>
  )
}

function ShelfRow({ shelf }: { shelf: Shelf }) {
  const navigate = useNavigate()
  const playTrackList = usePlayer((s) => s.playTrackList)
  const tracks = useMemo(() => shelf.tracks.map((t) => recommendedTrackToTrack(t, 'shelf')), [shelf.tracks])
  return (
    <div className="mb-8">
      <Carousel title={shelfTitle(shelf)}>
        {shelf.artists.map((artist) => (
          <MediaCard
            key={`${artist.source}:${artist.externalId}`}
            title={artist.name}
            subtitle={reasonText(artist.reason) ?? 'Artist'}
            coverSrc={artist.coverUrl || undefined}
            coverId={artist.coverArtId}
            rounded="full"
            onClick={() => navigate(`/artist/${artist.source}/${artist.externalId}`)}
          />
        ))}
        {shelf.tracks.map((r, i) => {
          const play = () => playTrackList(tracks, i)
          return (
            <MediaCard
              key={`${r.source}:${r.externalId}`}
              title={r.title}
              subtitle={r.artist}
              coverSrc={r.coverUrl || undefined}
              coverId={r.coverArtId || r.match?.coverArtId || undefined}
              onClick={play}
              onPlay={play}
            />
          )
        })}
      </Carousel>
    </div>
  )
}

/**
 * Home's "For you" shelves. The server answers from its cache at once and
 * refreshes in the background; the cached shelves stay on screen until the
 * refreshed ones arrive, which replace them in place.
 */
export function ForYouShelves() {
  const { data, isLoading } = useShelves()
  const shelves = data?.shelves ?? []
  if (isLoading || (shelves.length === 0 && data?.refreshing)) return <ShelfSkeleton />
  if (shelves.length === 0) return null
  return (
    <div aria-label="For you" role="region">
      {data?.offline && (
        <p className="-mt-4 mb-4 text-xs text-text-muted">Offline · last results{updatedText(data.updatedAt)}</p>
      )}
      {shelves.map((shelf, i) => (
        <ShelfRow key={`${shelf.kind}:${shelf.seed?.artist ?? ''}:${shelf.seed?.title ?? i}`} shelf={shelf} />
      ))}
    </div>
  )
}

/** Home's Mixes. An empty Mix, such as a Release Radar with nothing new, is hidden. */
export function MixesRow() {
  const { data } = useMixes()
  const navigate = useNavigate()
  const playTrackList = usePlayer((s) => s.playTrackList)
  const mixes = (data?.mixes ?? []).filter((m) => m.tracks.length > 0)
  if (mixes.length === 0) return null
  return (
    <div className="mb-8">
      <Carousel title="Your mixes">
        {mixes.map((mix) => {
          const first = mix.tracks[0]
          return (
            <MediaCard
              key={mix.kind}
              title={mixTitle(mix.kind)}
              subtitle={`${mix.tracks.length} songs`}
              coverSrc={first.coverUrl || undefined}
              coverId={first.coverArtId || first.match?.coverArtId || undefined}
              onClick={() => navigate(`/mix/${mix.kind}`)}
              onPlay={() => playTrackList(mix.tracks.map((t) => recommendedTrackToTrack(t, 'mix')), 0)}
            />
          )
        })}
      </Carousel>
    </div>
  )
}

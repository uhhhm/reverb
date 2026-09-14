import { useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Button, TrackRow } from './ui'
import { usePlayer } from '../lib/playerStore'
import { reasonText, recommendedTrackToTrack, usePlaylistSuggestions, type RecommendedTrack } from '../lib/recommendationsApi'
import { addSyncedTrack } from '../lib/syncedPlaylistApi'

const keyOf = (r: RecommendedTrack) => `${r.source}:${r.externalId}`

/**
 * "Suggested songs" below a managed playlist. Add puts a suggestion in the
 * playlist without downloading it, as any playlist edit does, and it leaves
 * the list at once. Refresh asks for the next best candidates.
 */
export function PlaylistSuggestions({ playlistId }: { playlistId: string }) {
  const [page, setPage] = useState(0)
  const [added, setAdded] = useState<Set<string>>(() => new Set())
  const [adding, setAdding] = useState<string | null>(null)
  const [addError, setAddError] = useState(false)
  const { data, isFetching } = usePlaylistSuggestions(playlistId, page, true)
  const qc = useQueryClient()
  const playTrackList = usePlayer((s) => s.playTrackList)
  const currentTrackId = usePlayer((s) => s.current?.id)

  const results = useMemo(() => (data?.tracks ?? []).filter((r) => !added.has(keyOf(r))), [data, added])
  const tracks = useMemo(() => results.map((r) => recommendedTrackToTrack(r)), [results])

  if (!data?.available && results.length === 0) return null

  async function handleAdd(r: RecommendedTrack) {
    const key = keyOf(r)
    setAdding(key)
    setAddError(false)
    try {
      await addSyncedTrack(playlistId, {
        source: r.source, externalId: r.externalId, title: r.title, artist: r.artist,
        album: r.album, isrc: r.isrc, durationMs: r.durationMs, coverArtId: r.coverArtId || r.match?.coverArtId,
        download: false,
      })
      setAdded((prev) => new Set(prev).add(key))
      void qc.invalidateQueries({ queryKey: ['synced-playlist', playlistId] })
      void qc.invalidateQueries({ queryKey: ['playlist-suggestions', playlistId] })
    } catch {
      setAddError(true)
    } finally {
      setAdding(null)
    }
  }

  return (
    <section aria-label="Suggested songs" className="mt-10">
      <div className="mb-3 flex items-baseline gap-3">
        <h2 className="text-xl font-bold text-text-primary">Suggested songs</h2>
        <span className="text-xs text-text-muted">Based on what's in this playlist</span>
        <Button className="ml-auto" variant="secondary" size="sm" disabled={isFetching} onClick={() => setPage((p) => p + 1)}>
          Refresh
        </Button>
      </div>
      {addError && <p role="alert" className="mb-2 text-xs text-text-muted">Couldn't add the song. Try again.</p>}
      {results.length === 0 ? (
        <p className="text-sm text-text-muted">No suggestions right now.</p>
      ) : (
        <div className="space-y-0.5">
          {tracks.map((track, i) => {
            const r = results[i]
            return (
              <TrackRow
                key={keyOf(r)}
                track={track}
                index={i}
                active={currentTrackId === track.id}
                coverSrc={r.coverUrl || undefined}
                caption={reasonText(r.reason)}
                right={
                  <Button variant="secondary" size="sm" disabled={adding === keyOf(r)} onClick={() => void handleAdd(r)} aria-label={`Add ${r.title} to playlist`}>
                    Add
                  </Button>
                }
                onPlay={() => playTrackList(tracks, i)}
              />
            )
          })}
        </div>
      )}
    </section>
  )
}

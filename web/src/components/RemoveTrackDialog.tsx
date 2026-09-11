import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { removeTrack } from '../lib/libraryApi'
import { useToastStore } from '../lib/toastStore'
import type { Track } from '../lib/types'
import { Button, Modal } from './ui'

interface Props {
  track: Track | null
  onClose: () => void
}

export function RemoveTrackDialog({ track, onClose }: Props) {
  const qc = useQueryClient()
  const pushToast = useToastStore((s) => s.push)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function handleRemove() {
    if (!track || busy) return
    setBusy(true)
    setError(null)
    try {
      await removeTrack(track.id)
      qc.setQueriesData<Track[]>({ queryKey: ['library', 'songs'] }, (songs) =>
        songs?.filter((song) => song.id !== track.id),
      )
      // Do not refetch before Navidrome's scheduled scan has completed: that
      // would briefly put the stale row back. The scan emits library.updated,
      // and realtimeWiring invalidates all affected views at that point.
      pushToast(`Removed “${track.title}” from your library`, 'success')
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't remove this track")
      setBusy(false)
    }
  }

  return (
    <Modal
      open={track !== null}
      onClose={() => { if (!busy) onClose() }}
      testId="remove-track-dialog"
      title="Remove track from library?"
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={onClose}>Cancel</Button>
          <Button variant="primary" disabled={busy} onClick={() => void handleRemove()}>
            {busy ? 'Removing…' : 'Remove track'}
          </Button>
        </>
      }
    >
      <p className="text-sm text-text-secondary">
        Remove <strong className="text-text-primary">{track?.title}</strong>? This permanently
        deletes its audio file from your library. This can&apos;t be undone.
      </p>
      {error && <p role="alert" className="text-sm text-error">{error}</p>}
    </Modal>
  )
}

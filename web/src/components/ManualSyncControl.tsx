import { useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getSyncStatus, triggerSync } from '../lib/syncApi'
import { useSyncStore } from '../lib/syncStore'
import { Button } from './ui/Button'

/** Polling is authoritative: a short round can finish before the socket connects. */
export function ManualSyncControl() {
  const qc = useQueryClient()
  const syncing = useSyncStore((s) => s.syncing)
  const status = useQuery({
    queryKey: ['sync', 'status'], queryFn: getSyncStatus, refetchInterval: 1000,
  })
  const sync = useMutation({
    mutationFn: triggerSync,
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ['sync', 'status'] }) },
  })
  const requested = sync.data?.round
  const observed = status.data?.round
  const round = requested && (!observed || (requested.id > observed.id && status.dataUpdatedAt <= sync.submittedAt)) ? requested : observed
  const active = round?.state === 'pending' || round?.state === 'running'
  const busy = sync.isPending || active || (!round && syncing)
  const finishedAt = round?.finishedAt

  useEffect(() => {
    if (!finishedAt) return
    for (const key of ['library', 'synced-playlist', 'synced-playlists', 'pairing/devices', 'p2p/peers', 'p2p/status', 'p2p/manifests']) {
      void qc.invalidateQueries({ queryKey: [key] })
    }
  }, [qc, finishedAt])

  return <div className="space-y-2">
    <Button onClick={() => sync.mutate()} disabled={busy}>
      {busy ? 'Syncing…' : 'Sync now'}
    </Button>
    <div role="status" className="text-sm text-text-secondary" aria-live="polite">
      {sync.isPending ? 'Requesting sync…' : round?.state === 'pending' ? 'Sync pending…' :
        round?.state === 'running' ? `Syncing with paired devices… ${round.succeeded} of ${round.peers} exchanges finished.` :
        round?.state === 'completed' ? `Library exchange finished with ${round.succeeded} device(s) at ${new Date(round.finishedAt).toLocaleTimeString()} (${(round.durationMs / 1000).toFixed(1)}s).` :
        round?.state === 'failed' ? `Sync finished with errors. ${round.succeeded} of ${round.peers} device exchanges succeeded.` :
        round?.state === 'no_peers' ? 'No paired devices to sync with. Pair another device first.' :
        syncing ? 'Syncing with paired devices…' : sync.isSuccess ? 'Sync requested; waiting for device status…' : null}
    </div>
    {!!finishedAt && round?.state !== 'no_peers' && <p className="text-xs text-text-muted">Library updates and file downloads may continue in the background.</p>}
    {round?.errors.map((error, i) => <p key={i} role="alert" className="break-words text-sm text-error">{error}</p>)}
    {sync.isError && <p role="alert" className="text-sm text-error">{sync.error.message}</p>}
    {status.isError && <p role="alert" className="text-sm text-error">Could not refresh sync status. Completion is unconfirmed; retrying…</p>}
  </div>
}

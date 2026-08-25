import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useOfflineSet, setOfflineSet } from '../lib/offlineSetApi'
import { useSyncStatus } from '../lib/syncApi'
import { useDocumentTitle } from '../lib/useDocumentTitle'
import { Toggle } from '../components/ui/Toggle'
import { EmptyState } from '../components/ui/EmptyState'
import { Button } from '../components/ui/Button'
import { Skeleton } from '../components/ui/Skeleton'

export default function OfflineSet() {
  useDocumentTitle('Offline set')
  const qc = useQueryClient()
  const { data, isLoading, isError, error, refetch } = useOfflineSet()
  const { data: sync } = useSyncStatus()
  const [now, setNow] = useState(() => new Date())
  const [toggling, setToggling] = useState<string | null>(null)
  const [toggleError, setToggleError] = useState<string | null>(null)

  useEffect(() => {
    const id = window.setInterval(() => setNow(new Date()), 60_000)
    return () => window.clearInterval(id)
  }, [])

  async function handleToggle(entry: { playlistId: string; enabled: boolean }, next: boolean) {
    setToggling(entry.playlistId)
    setToggleError(null)
    try {
      await setOfflineSet(entry.playlistId, next)
      await qc.invalidateQueries({ queryKey: ['offline-set'] })
    } catch (e) {
      setToggleError(e instanceof Error ? e.message : 'Could not update offline set')
    } finally {
      setToggling(null)
    }
  }

  return (
    <div className="max-w-4xl space-y-6 pb-8">
      <header>
        <h1 className="text-3xl font-black tracking-tight text-text-primary">Offline set</h1>
        <p className="mt-1 text-sm text-text-secondary">
          Playlists kept offline on this device. Removing from offline set does not delete the playlist.
        </p>
      </header>

      <div
        data-testid="sync-status"
        className="rounded-lg border border-border-subtle bg-raised px-4 py-3 text-sm text-text-secondary"
      >
        {sync ? (
          <span>
            Revision {sync.revision} · {sync.deviceCount} device(s) · Last sync {now.toLocaleTimeString()}
          </span>
        ) : (
          <span>Sync status unavailable</span>
        )}
      </div>

      {isLoading && (
        <div className="space-y-2">
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
        </div>
      )}

      {isError && (
        <div className="rounded-lg border border-border-subtle bg-raised p-6 space-y-3">
          <p role="alert" className="text-sm text-error">
            {error instanceof Error ? error.message : 'Could not load offline set'}
          </p>
          <Button variant="secondary" size="sm" onClick={() => void refetch()}>
            Retry
          </Button>
        </div>
      )}

      {!isLoading && !isError && (
        <>
          {toggleError && (
            <p role="alert" className="text-sm text-error">
              {toggleError}
            </p>
          )}
          {(!data || data.length === 0) ? (
            <EmptyState icon="browse" title="No playlists offline" hint="Add playlists to your offline set to keep them on this device." />
          ) : (
            <ul className="space-y-2">
              {data.map((entry) => (
                <li
                  key={entry.playlistId}
                  className="flex items-center justify-between gap-3 rounded-lg border border-border-subtle bg-raised px-4 py-3"
                >
                  <div className="min-w-0">
                    <p className="text-sm font-semibold text-text-primary truncate">
                      {entry.playlistName || entry.playlistId}
                    </p>
                    <p className="text-xs text-text-muted font-mono truncate">{entry.playlistId}</p>
                  </div>
                  <div className="flex items-center gap-3 flex-none">
                    <span className="text-xs text-text-muted hidden sm:inline">
                      {entry.enabled ? 'Kept offline' : 'Not offline'}
                    </span>
                    <Toggle
                      checked={entry.enabled}
                      label={`Keep ${entry.playlistName || entry.playlistId} offline`}
                      onChange={(next) => void handleToggle(entry, next)}
                    />
                    {toggling === entry.playlistId && (
                      <span className="text-xs text-text-muted">saving…</span>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}
          <p className="text-xs text-text-muted">Removing from offline set does not delete the playlist.</p>
        </>
      )}
    </div>
  )
}

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  getP2PStatus,
  getP2PPeers,
  getFileManifests,
  fetchFileFromPeer,
  getFileFetchFailures,
  migratePortableNames,
  getPortableNamesPending,
  type FileFetchFailure,
} from '../lib/p2pApi'
import { Button } from './ui/Button'
import { BackgroundSyncSettings } from './BackgroundSyncSettings'

/**
 * The sync controls and diagnostics beneath the pairing flow: background sync,
 * files that are not replicating, the portable-name migration, and the raw p2p
 * state. The Devices & sync page mounts this only while its section is open, so
 * the peer and failure polls run only while someone is looking at them.
 */
export function SyncAdvanced() {
  const qc = useQueryClient()

  const statusQ = useQuery({ queryKey: ['p2p/status'], queryFn: getP2PStatus })
  const peersQ = useQuery({ queryKey: ['p2p/peers'], queryFn: getP2PPeers, refetchInterval: 5000 })
  const manifestsQ = useQuery({ queryKey: ['p2p/manifests'], queryFn: getFileManifests })
  const failuresQ = useQuery({ queryKey: ['p2p/file-failures'], queryFn: getFileFetchFailures, refetchInterval: 30000 })
  // Asked of this device about its own library: the device holding an unstorable
  // name is not the one that fails to copy it.
  const pendingQ = useQuery({ queryKey: ['p2p/portable-names'], queryFn: getPortableNamesPending })

  // Renaming the owner's own music files is the most destructive thing Reverb
  // does to data it did not create, so it is a button they press rather than
  // something that happens at startup — and it sits next to the list of tracks
  // that are stuck, which is where they find out they need it.
  const migrate = useMutation({
    mutationFn: migratePortableNames,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['p2p/file-failures'] })
      void qc.invalidateQueries({ queryKey: ['p2p/manifests'] })
      void qc.invalidateQueries({ queryKey: ['p2p/portable-names'] })
    },
  })

  return (
    <div className="space-y-4">
      <BackgroundSyncSettings />

      {(failuresQ.data?.length ?? 0) > 0 && (
        <section className="rounded-lg border border-border-subtle bg-raised p-4 space-y-3">
          <h3 className="font-semibold">Files that are not syncing</h3>
          <p className="text-xs text-text-secondary">
            These tracks could not be copied from a paired device. Reverb keeps trying them on a widening
            schedule rather than giving up, so one that becomes available again arrives on its own.
          </p>
          <div className="max-h-64 space-y-1 overflow-auto">
            {failuresQ.data?.map((f) => (
              <div key={`${f.peerId}:${f.contentHash}`} className="rounded bg-input px-2 py-1 text-xs">
                <div className="flex items-center justify-between gap-2">
                  <span className="truncate" title={f.relPath}>{f.relPath}</span>
                  <span className="shrink-0 text-text-secondary">{describeRetry(f)}</span>
                </div>
                <p className="text-text-secondary" title={f.detail}>{describeFailure(f)}</p>
              </div>
            ))}
          </div>
        </section>
      )}

      {(pendingQ.data?.pending ?? 0) > 0 && (
        <section className="rounded-lg border border-border-subtle bg-raised p-4 space-y-3">
          <h3 className="font-semibold">File names other devices cannot store</h3>
          <p className="text-sm text-text-secondary">
            {pendingQ.data?.pending} of this device&apos;s files were named before Reverb started choosing names
            every platform can store, so a Windows device in your household cannot save them. Renaming them
            here lets them sync. Files keep their contents and their place in your library, and nothing is
            overwritten — a name that is already taken gets a free one.
          </p>
          <Button onClick={() => migrate.mutate()} disabled={migrate.isPending}>
            {migrate.isPending ? 'Renaming…' : 'Rename them for every device'}
          </Button>
          {migrate.error && <p className="text-sm text-error">{String(migrate.error)}</p>}
          {migrate.data && (
            <p className="text-sm text-green-600">
              Renamed {migrate.data.renamed.length} of {migrate.data.examined} entries
              {migrate.data.failed.length > 0 && `; ${migrate.data.failed.length} could not be moved`}.
            </p>
          )}
        </section>
      )}

      <section className="rounded-lg border border-border-subtle bg-raised p-4 space-y-3">
        <h3 className="font-semibold">Diagnostics</h3>
        {statusQ.isLoading ? (
          <p className="text-sm">Loading…</p>
        ) : statusQ.error ? (
          <p className="text-sm text-error">Unavailable — p2p host not started.</p>
        ) : statusQ.data ? (
          <div className="text-sm space-y-1">
            <div><span className="text-text-secondary">Peer ID:</span> <code className="break-all">{statusQ.data.peerId}</code></div>
            <div><span className="text-text-secondary">Peers:</span> {statusQ.data.peerCount}</div>
            <div><span className="text-text-secondary">HLC:</span> {statusQ.data.hlc}</div>
            <div><span className="text-text-secondary">Vector:</span> <code>{JSON.stringify(statusQ.data.vector)}</code></div>
          </div>
        ) : null}

        <h4 className="pt-2 text-sm font-semibold">Discovered peers (mDNS + DHT)</h4>
        {peersQ.data?.length ? (
          <ul className="space-y-2">
            {peersQ.data.map((p) => (
              <li key={p.peerId} className="rounded border border-border-subtle p-2 text-sm">
                <div className="font-mono break-all text-xs">{p.peerId}</div>
                <div className="text-text-secondary text-xs">{p.addrs.join(', ')}</div>
                <div className="text-xs">conns: {p.conns} {p.connected ? '● connected' : '○'}</div>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-text-secondary">No peers discovered. Ensure both devices are on same LAN or have relay.</p>
        )}

        <h4 className="pt-2 text-sm font-semibold">File manifests</h4>
        <p className="text-xs text-text-secondary">{manifestsQ.data?.length ?? 0} files tracked</p>
        <div className="max-h-64 overflow-auto space-y-1">
          {manifestsQ.data?.slice(0, 20).map((m) => (
            <div key={m.canonicalId} className="flex items-center justify-between rounded bg-input px-2 py-1 text-xs">
              <span className="truncate">{m.relPath}</span>
              <span className="text-text-secondary">{(m.size / 1024).toFixed(1)} KB</span>
              <button
                type="button"
                onClick={() => {
                  const firstPeer = peersQ.data?.[0]?.peerId
                  if (firstPeer) fetchFileFromPeer(firstPeer, m.relPath, m.contentHash)
                }}
                className="underline"
              >
                Fetch
              </button>
            </div>
          ))}
        </div>
      </section>
    </div>
  )
}

/**
 * The stored reason is a stable token; the sentence the owner reads lives here,
 * so the wording can change without a migration.
 */
function describeFailure(f: FileFetchFailure): string {
  switch (f.reason) {
    case 'unstorable-path':
      return 'This device cannot store that file name. Renaming it on the device that holds it will let it sync.'
    case 'content-mismatch':
      return 'The file that device sent did not match the content it advertised.'
    default:
      // A failure is recorded per device, so this one says nothing about
      // whether another device could supply the same file.
      return 'That device would not send this file.'
  }
}

function describeRetry(f: FileFetchFailure): string {
  // An unwritable name is not waiting on a timer. It is rejected before the
  // round even asks, and stays rejected until the name changes, so saying
  // "retrying in 4h" would promise something that will not happen.
  if (f.reason === 'unstorable-path') return 'needs renaming'
  const wait = f.nextAttemptAt - Date.now()
  if (wait <= 0) return 'retrying'
  const hours = Math.round(wait / 3_600_000)
  if (hours >= 1) return `retrying in ${hours}h`
  return `retrying in ${Math.max(1, Math.round(wait / 60_000))}m`
}

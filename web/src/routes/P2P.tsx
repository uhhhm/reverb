import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getP2PStatus, getP2PPeers, redeemViaPeer, getFileManifests, fetchFileFromPeer } from '../lib/p2pApi'
import { generatePairingCode, listDevices, deleteDevice, type DeviceInfo } from '../lib/pairingApi'
import { ManualSyncControl } from '../components/ManualSyncControl'
import { Button } from '../components/ui/Button'
import { Modal } from '../components/ui/Modal'
import { BackgroundSyncSettings } from '../components/BackgroundSyncSettings'

export default function P2P() {
  const qc = useQueryClient()
  const [code, setCode] = useState('')
  const [peerId, setPeerId] = useState('')
  const [deviceName, setDeviceName] = useState('')
  const [removingDevice, setRemovingDevice] = useState<DeviceInfo | null>(null)

  const devicesQ = useQuery({ queryKey: ['pairing/devices'], queryFn: listDevices, refetchInterval: 5000 })
  const statusQ = useQuery({ queryKey: ['p2p/status'], queryFn: getP2PStatus })
  const peersQ = useQuery({ queryKey: ['p2p/peers'], queryFn: getP2PPeers, refetchInterval: 5000 })
  const manifestsQ = useQuery({ queryKey: ['p2p/manifests'], queryFn: getFileManifests })

  const genCode = useMutation({
    mutationFn: () => generatePairingCode(),
    onSuccess: (data) => setCode(data.code),
  })

  const remove = useMutation({
    mutationFn: deleteDevice,
    onSuccess: () => {
      setRemovingDevice(null)
      void qc.invalidateQueries({ queryKey: ['pairing/devices'] })
      void qc.invalidateQueries({ queryKey: ['p2p/peers'] })
    },
  })

  const redeem = useMutation({
    mutationFn: () => redeemViaPeer(peerId, code, deviceName || 'unnamed'),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['pairing/devices'] })
      qc.invalidateQueries({ queryKey: ['p2p/peers'] })
      qc.invalidateQueries({ queryKey: ['p2p/status'] })
    },
  })

  return (
    <div className="space-y-8 max-w-3xl">
      <header className="space-y-1">
        <h1 className="text-2xl font-bold">P2P Devices</h1>
        <p className="text-sm text-text-secondary">Link devices directly via libp2p — no central server. Single pairing code, full file sync.</p>
      </header>

      <BackgroundSyncSettings />

      <section className="rounded-lg border border-border-subtle bg-raised p-4 space-y-3">
        <div className="flex items-center justify-between gap-3">
          <h2 className="font-semibold">Paired devices</h2>
        </div>
        <p className="text-sm text-text-secondary">Manage the devices allowed to sync with this library.</p>
        <ManualSyncControl />
        {devicesQ.isLoading ? (
          <p className="text-sm">Loading paired devices…</p>
        ) : devicesQ.isError ? (
          <p role="alert" className="text-sm text-error">{devicesQ.error.message}</p>
        ) : (
          <ul className="space-y-2">
            {devicesQ.data?.map((device) => (
              <li key={device.id} className="flex items-center justify-between gap-3 rounded border border-border-subtle p-3">
                <div className="min-w-0">
                  <p className="font-semibold truncate">{device.name}</p>
                  <p className="text-xs text-text-secondary">
                    {device.lastSeen ? `Last seen ${new Date(device.lastSeen * 1000).toLocaleString()}` : 'Not synced yet'}
                  </p>
                </div>
                {device.isServer ? (
                  <span className="text-xs text-text-secondary">Server device</span>
                ) : (
                  <Button size="sm" aria-label={`Remove ${device.name}`} onClick={() => {
                    remove.reset()
                    setRemovingDevice(device)
                  }}>Remove</Button>
                )}
              </li>
            ))}
          </ul>
        )}
        {devicesQ.data?.length === 0 && <p className="text-sm text-text-secondary">No paired devices yet.</p>}
      </section>

      <Modal
        open={removingDevice !== null}
        title={`Remove ${removingDevice?.name ?? 'device'}?`}
        onClose={() => { if (!remove.isPending) setRemovingDevice(null) }}
        footer={<>
          <Button disabled={remove.isPending} onClick={() => setRemovingDevice(null)}>Cancel</Button>
          <Button variant="primary" disabled={remove.isPending} onClick={() => {
            if (removingDevice) remove.mutate(removingDevice.id)
          }}>{remove.isPending ? 'Removing…' : 'Remove device'}</Button>
        </>}
      >
        <p className="text-sm text-text-secondary">
          This device will lose access to this library and stop syncing. Downloaded files stay on it.
          To reconnect, pair it again with a new code.
        </p>
        {remove.isError && <p role="alert" className="text-sm text-error">{remove.error.message}</p>}
      </Modal>

      <section className="rounded-lg border border-border-subtle bg-raised p-4 space-y-3">
        <h2 className="font-semibold">Local status</h2>
        {statusQ.isLoading ? (
          <p className="text-sm">Loading…</p>
        ) : statusQ.error ? (
          <p className="text-sm text-red-500">Unavailable — p2p host not started.</p>
        ) : statusQ.data ? (
          <div className="text-sm space-y-1">
            <div><span className="text-text-secondary">Peer ID:</span> <code className="break-all">{statusQ.data.peerId}</code></div>
            <div className="space-y-1">
              <span className="text-text-secondary">Address to give another device:</span>
              {(statusQ.data.dialAddrs ?? []).length ? (
                <ul className="space-y-1">
                  {(statusQ.data.dialAddrs ?? []).map((a) => (
                    <li key={a}>
                      <code className="break-all text-xs" data-testid="dial-addr">{a}</code>
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="text-xs text-text-secondary">
                  No routable address yet — this host has only loopback interfaces.
                </p>
              )}
              <p className="text-xs text-text-secondary">
                Paste one of these into the other device&apos;s Peer field. A bare peer ID only works on the
                same LAN, where mDNS can find it; over a VPN the full address is required.
              </p>
            </div>
            <div><span className="text-text-secondary">Peers:</span> {statusQ.data.peerCount}</div>
            <div><span className="text-text-secondary">HLC:</span> {statusQ.data.hlc}</div>
            <div><span className="text-text-secondary">Vector:</span> <code>{JSON.stringify(statusQ.data.vector)}</code></div>
          </div>
        ) : null}
      </section>

      <section className="rounded-lg border border-border-subtle bg-raised p-4 space-y-3">
        <h2 className="font-semibold">Discovered peers (mDNS + DHT)</h2>
        {peersQ.data?.length ? (
          <ul className="space-y-2">
            {peersQ.data.map((p) => (
              <li key={p.peerId} className="rounded border border-border-subtle p-2 text-sm">
                <div className="font-mono break-all text-xs">{p.peerId}</div>
                <div className="text-text-secondary text-xs">{p.addrs.join(', ')}</div>
                <div className="text-xs">conns: {p.conns} {p.connected ? '● connected' : '○'}</div>
                <button
                  type="button"
                  onClick={() => setPeerId(p.peerId)}
                  className="mt-1 text-xs underline"
                >
                  Select
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-text-secondary">No peers discovered. Ensure both devices are on same LAN or have relay.</p>
        )}
      </section>

      <section className="rounded-lg border border-border-subtle bg-raised p-4 space-y-3">
        <h2 className="font-semibold">Pairing (single code)</h2>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={() => genCode.mutate()}
            disabled={genCode.isPending}
            className="rounded bg-accent px-3 py-1.5 text-sm font-semibold text-on-accent"
          >
            Generate code
          </button>
          {code && <code className="rounded bg-input px-2 py-1 text-sm">{code}</code>}
        </div>
        <div className="grid gap-2 sm:grid-cols-3">
          <input
            placeholder="Peer ID or /ip4/…/p2p/…"
            aria-label="Peer ID or multiaddr"
            value={peerId}
            onChange={(e) => setPeerId(e.target.value)}
            className="rounded border border-border-subtle bg-input px-2 py-1.5 text-sm"
          />
          <input
            placeholder="Code XXXX-XXXX"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            className="rounded border border-border-subtle bg-input px-2 py-1.5 text-sm"
          />
          <input
            placeholder="Device name"
            value={deviceName}
            onChange={(e) => setDeviceName(e.target.value)}
            className="rounded border border-border-subtle bg-input px-2 py-1.5 text-sm"
          />
        </div>
        <button
          type="button"
          onClick={() => redeem.mutate()}
          disabled={redeem.isPending || !peerId || !code}
          className="rounded bg-accent px-3 py-1.5 text-sm font-semibold text-on-accent disabled:opacity-50"
        >
          Redeem via peer
        </button>
        {redeem.error && <p className="text-sm text-red-500">{String(redeem.error)}</p>}
        {redeem.data && <p className="text-sm text-green-600">Paired: {redeem.data.deviceId}</p>}
      </section>

      <section className="rounded-lg border border-border-subtle bg-raised p-4 space-y-3">
        <h2 className="font-semibold">File manifests (full sync)</h2>
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

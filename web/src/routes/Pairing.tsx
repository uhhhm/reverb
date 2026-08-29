import { useEffect, useState } from 'react'
import {
  generatePairingCode,
  redeemPairingCode,
  listDevices,
  deleteDevice,
  getSyncStatus,
  storeSyncCredentials,
  getSyncToken,
  getSyncDeviceId,
  clearSyncCredentials,
  type PairingCode,
  type DeviceInfo,
} from '../lib/pairingApi'
import { getP2PStatus } from '../lib/p2pApi'
import { useDocumentTitle } from '../lib/useDocumentTitle'
import { Button } from '../components/ui/Button'

function formatPairingInput(value: string): string {
  const raw = value.replace(/[^a-zA-Z0-9]/g, '').toUpperCase().slice(0, 8)
  if (raw.length <= 4) return raw
  return `${raw.slice(0, 4)}-${raw.slice(4)}`
}

function formatLastSeen(seconds: number, nowSec: number): string {
  if (!seconds) return 'never'
  const delta = Math.max(0, nowSec - seconds)
  if (delta < 60) return 'just now'
  if (delta < 3600) return `${Math.floor(delta / 60)}m ago`
  if (delta < 86400) return `${Math.floor(delta / 3600)}h ago`
  return `${Math.floor(delta / 86400)}d ago`
}

function formatExpiry(seconds: number): string {
  if (seconds <= 0) return '0:00'
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return `${m}:${String(s).padStart(2, '0')}`
}

export default function Pairing() {
  useDocumentTitle('Pairing')

  const [paired, setPaired] = useState(() => getSyncToken() !== null)
  const [thisDeviceId, setThisDeviceId] = useState(() => getSyncDeviceId())

  const [pairingCode, setPairingCode] = useState<PairingCode | null>(null)
  const [genLoading, setGenLoading] = useState(false)
  const [genError, setGenError] = useState<string | null>(null)
  const [nowSec, setNowSec] = useState(() => Math.floor(Date.now() / 1000))
  const [copied, setCopied] = useState(false)
  // The address the other device needs in order to reach this one. mDNS finds a
  // peer on the same LAN, but its multicast does not cross a VPN, so on a VPN
  // this address is the only way the two devices ever connect.
  const [dialAddrs, setDialAddrs] = useState<string[]>([])
  const [copiedAddr, setCopiedAddr] = useState(false)

  const [redeemInput, setRedeemInput] = useState('')
  const [deviceName, setDeviceName] = useState(() => {
    if (typeof navigator !== 'undefined' && navigator.userAgent) return navigator.userAgent.slice(0, 80) || 'Laptop'
    return 'Laptop'
  })
  const [redeemLoading, setRedeemLoading] = useState(false)
  const [redeemError, setRedeemError] = useState<string | null>(null)
  const [redeemSuccess, setRedeemSuccess] = useState<string | null>(null)

  const [devices, setDevices] = useState<DeviceInfo[] | null>(null)
  const [devicesLoading, setDevicesLoading] = useState(true)
  const [devicesError, setDevicesError] = useState<string | null>(null)

  const [confirmUnpairId, setConfirmUnpairId] = useState<string | null>(null)
  const [unpairingId, setUnpairingId] = useState<string | null>(null)

  const [syncStatus, setSyncStatus] = useState<{ revision: number; deviceCount: number } | null>(null)
  const [syncError, setSyncError] = useState<string | null>(null)

  async function refreshDevices(opts?: { silent?: boolean }) {
    const silent = opts?.silent ?? false
    if (!silent) setDevicesLoading(true)
    setDevicesError(null)
    try {
      const list = await listDevices()
      setDevices(list)
      setNowSec(Math.floor(Date.now() / 1000))
    } catch (e) {
      setDevicesError(e instanceof Error ? e.message : 'Could not load devices')
    } finally {
      if (!silent) setDevicesLoading(false)
    }
  }

  async function refreshSync() {
    try {
      const s = await getSyncStatus()
      setSyncStatus(s)
      setSyncError(null)
    } catch (e) {
      setSyncError(e instanceof Error ? e.message : 'Could not load sync status')
    }
  }

  /* eslint-disable react-hooks/set-state-in-effect -- intentional: initial load of devices and sync status */
  useEffect(() => {
    void refreshDevices()
    void refreshSync()
    // Best-effort: p2p may be unavailable, in which case there is simply no
    // address to show and the LAN path is all that is on offer.
    void getP2PStatus()
      .then((s) => setDialAddrs(s.dialAddrs ?? []))
      .catch(() => setDialAddrs([]))
  }, [])
  /* eslint-enable react-hooks/set-state-in-effect */

  // Keep the paired list (and its last-seen times) current while the page is open.
  useEffect(() => {
    const id = window.setInterval(() => void refreshDevices({ silent: true }), 15000)
    return () => window.clearInterval(id)
  }, [])

  // Keep last-seen captions fresh without a network round-trip.
  useEffect(() => {
    const id = window.setInterval(() => setNowSec(Math.floor(Date.now() / 1000)), 30000)
    return () => window.clearInterval(id)
  }, [])

  useEffect(() => {
    if (!pairingCode) return
    const id = window.setInterval(() => setNowSec(Math.floor(Date.now() / 1000)), 1000)
    return () => window.clearInterval(id)
  }, [pairingCode])

  async function onGenerate() {
    setGenLoading(true)
    setGenError(null)
    setCopied(false)
    try {
      const pc = await generatePairingCode()
      setPairingCode(pc)
      setNowSec(Math.floor(Date.now() / 1000))
    } catch (e) {
      setGenError(e instanceof Error ? e.message : 'Could not generate pairing code')
    } finally {
      setGenLoading(false)
    }
  }

  async function onRedeem(e: React.FormEvent) {
    e.preventDefault()
    setRedeemError(null)
    setRedeemSuccess(null)
    const code = redeemInput.replace(/-/g, '').trim()
    if (code.length < 8) {
      setRedeemError('Enter a pairing code like XXXX-XXXX')
      return
    }
    if (!deviceName.trim()) {
      setRedeemError('Enter a device name')
      return
    }
    setRedeemLoading(true)
    try {
      const result = await redeemPairingCode(redeemInput, deviceName.trim())
      storeSyncCredentials(result.token, result.deviceId)
      setThisDeviceId(result.deviceId)
      setPaired(true)
      setRedeemSuccess(`Device paired. Sync token stored for device ${result.deviceId}.`)
      setRedeemInput('')
      void refreshDevices()
      void refreshSync()
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Could not redeem pairing code'
      setRedeemError(msg)
    } finally {
      setRedeemLoading(false)
    }
  }

  async function onUnpairDevice(id: string) {
    setUnpairingId(id)
    setDevicesError(null)
    try {
      await deleteDevice(id)
      setConfirmUnpairId(null)
      void refreshDevices()
      void refreshSync()
    } catch (e) {
      setDevicesError(e instanceof Error ? e.message : 'Could not unpair device')
    } finally {
      setUnpairingId(null)
    }
  }

  function onClearPaired() {
    clearSyncCredentials()
    setThisDeviceId(null)
    setPaired(false)
    setRedeemSuccess(null)
  }

  async function onCopyAddr() {
    if (dialAddrs.length === 0) return
    try {
      await navigator.clipboard.writeText(dialAddrs[0])
      setCopiedAddr(true)
      window.setTimeout(() => setCopiedAddr(false), 1500)
    } catch {
      setCopiedAddr(false)
    }
  }

  async function onCopy() {
    if (!pairingCode) return
    try {
      await navigator.clipboard.writeText(pairingCode.code)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      setCopied(false)
    }
  }

  const expiresIn = pairingCode ? Math.max(0, pairingCode.expiresAt - nowSec) : 0
  const expired = pairingCode ? expiresIn <= 0 : false

  return (
    <div className="max-w-4xl space-y-6 pb-8">
      <header>
        <h1 className="text-3xl font-black tracking-tight text-text-primary">Pairing</h1>
        <p className="mt-1 text-sm text-text-secondary">
          Connect two devices with a one-time code. One device generates the code, the other enters it — no
          passwords to type twice. A sync token is stored on the new device to authorize future sync.
        </p>
      </header>

      {/* How it works */}
      <section className="rounded-lg border border-accent/20 bg-accent/5 p-5 space-y-3">
        <h2 className="text-sm font-bold tracking-wide text-text-primary uppercase">How to pair two devices</h2>
        <ol className="list-decimal space-y-2 pl-5 text-sm leading-relaxed text-text-secondary">
          <li>
            <span className="font-semibold text-text-primary">On Device A — the device you are using right now</span>{' '}
            (or any device already paired): tap{' '}
            <span className="font-semibold text-text-primary">Generate pairing code</span> in Step 1 below and copy
            the <span className="font-mono font-semibold">XXXX-XXXX</span> code.
          </li>
          <li>
            <span className="font-semibold text-text-primary">On Device B — the new device you want to add</span>:
            open Reverb → <span className="font-semibold">Pairing</span> → paste that code into{' '}
            <span className="font-semibold text-text-primary">Pairing code from your other device</span> in Step 2,
            give <em>this</em> device a name, then tap <span className="font-semibold">Pair device</span>.
          </li>
        </ol>
        <p className="text-xs leading-relaxed text-text-muted">
          Tip: keep both devices online. Codes expire in 10 minutes and can only be used once. After pairing, both
          devices appear in <span className="font-semibold">Paired devices</span> below.
        </p>
      </section>

      {/* Generate pairing code */}
      <section className="rounded-lg border border-border-subtle bg-raised p-6 space-y-4">
        <div className="flex flex-wrap items-center gap-2">
          <span className="inline-flex items-center rounded-full bg-accent px-2.5 py-0.5 text-xs font-bold text-on-accent">
            Step 1
          </span>
          <span className="inline-flex items-center rounded-full border border-border-subtle bg-surface px-2.5 py-0.5 text-xs font-semibold text-text-secondary">
            Do this on Device A — this device (where you are now)
          </span>
        </div>
        <h2 className="text-lg font-extrabold tracking-tight text-text-primary">Generate pairing code</h2>
        <p className="text-xs leading-relaxed text-text-secondary">
          Create a one-time code <span className="font-semibold">on this device</span> to give to your other device.
          You will enter it on the other device in Step 2. Share it directly — it expires in 10 minutes and can only
          be used once.
        </p>
        <Button variant="primary" size="sm" onClick={() => void onGenerate()} disabled={genLoading}>
          {genLoading ? 'Generating...' : 'Generate pairing code'}
        </Button>
        {genError && (
          <p role="alert" className="text-sm text-error">
            {genError}
          </p>
        )}
        {pairingCode && (
          <div className="rounded-md border border-border-subtle bg-surface p-4 space-y-2">
            <p className="text-xs font-semibold tracking-wide text-text-muted uppercase">
              Code to enter on your other device (Device B)
            </p>
            <div className="flex items-center gap-3">
              <span className="font-mono text-xl font-bold tracking-widest text-text-primary" data-testid="pairing-code">
                {pairingCode.code}
              </span>
              <Button variant="secondary" size="sm" aria-label="Copy pairing code" onClick={() => void onCopy()}>
                {copied ? 'Copied' : 'Copy'}
              </Button>
            </div>
            {expired ? (
              <p className="text-xs text-error">Pairing code expired. Generate a new pairing code.</p>
            ) : (
              <p className="text-xs text-text-secondary">
                Code expires in {formatExpiry(expiresIn)} — paste it into Step 2 on your other device.
              </p>
            )}
            {dialAddrs.length > 0 && (
              <div className="border-t border-border-subtle pt-2 space-y-1">
                <p className="text-xs font-semibold tracking-wide text-text-muted uppercase">
                  This device&apos;s address
                </p>
                <div className="flex items-start gap-3">
                  <code className="min-w-0 flex-1 break-all font-mono text-xs text-text-primary" data-testid="dial-addr">
                    {dialAddrs[0]}
                  </code>
                  <Button variant="secondary" size="sm" aria-label="Copy device address" onClick={() => void onCopyAddr()}>
                    {copiedAddr ? 'Copied' : 'Copy'}
                  </Button>
                </div>
                <p className="text-xs text-text-secondary">
                  Only needed if the two devices are on a VPN rather than the same local network. Devices on the
                  same network find each other automatically; across a VPN they cannot, so the other device needs
                  this address as well as the code.
                </p>
              </div>
            )}
          </div>
        )}
      </section>

      {/* Paired devices */}
      <section className="rounded-lg border border-border-subtle bg-raised p-6 space-y-4">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h2 className="text-lg font-extrabold tracking-tight text-text-primary">Paired devices</h2>
          {devices && (
            <span className="text-xs font-semibold text-text-secondary" data-testid="paired-device-count">
              {devices.length} device{devices.length === 1 ? '' : 's'} currently paired
            </span>
          )}
        </div>
        <p className="text-xs text-text-secondary">
          These devices share your library over sync. Unpairing revokes a device&apos;s access immediately — it stops
          syncing and must redeem a new pairing code to rejoin. The server device cannot be unpaired.
        </p>
        {devicesLoading ? (
          <p className="text-sm text-text-muted">Loading devices...</p>
        ) : devicesError ? (
          <p role="alert" className="text-sm text-error">
            {devicesError}
          </p>
        ) : devices && devices.length === 0 ? (
          <p className="text-sm text-text-muted">No devices paired yet. Generate a code in Step 1 to add one.</p>
        ) : (
          <ul className="space-y-2">
            {devices?.map((d) => (
              <li
                key={d.id}
                className="rounded-md border border-border-subtle bg-surface px-3 py-2 space-y-2"
                data-testid={`device-${d.id}`}
              >
                <div className="flex items-center justify-between gap-3">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-semibold text-text-primary truncate">{d.name}</span>
                      {d.isServer && (
                        <span className="inline-flex items-center rounded-full bg-accent px-2 py-0.5 text-xs font-bold text-on-accent">
                          server
                        </span>
                      )}
                      {d.id === thisDeviceId && (
                        <span className="inline-flex items-center rounded-full border border-border-subtle px-2 py-0.5 text-xs font-semibold text-text-secondary">
                          this device
                        </span>
                      )}
                    </div>
                    <div className="text-xs text-text-muted">Last seen {formatLastSeen(d.lastSeen, nowSec)}</div>
                    <div className="text-xs text-text-muted font-mono truncate">{d.id}</div>
                  </div>
                  {d.isServer ? (
                    <span className="text-xs text-text-muted">cannot unpair server device</span>
                  ) : confirmUnpairId === d.id ? null : (
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={`Unpair ${d.name}`}
                      onClick={() => setConfirmUnpairId(d.id)}
                    >
                      Unpair
                    </Button>
                  )}
                </div>
                {confirmUnpairId === d.id && (
                  <div role="alertdialog" aria-label={`Confirm unpair ${d.name}`} className="rounded border border-error/40 bg-error/5 p-3 space-y-2">
                    <p className="text-xs leading-relaxed text-text-secondary">
                      Unpair <span className="font-semibold text-text-primary">{d.name}</span>? It will lose access to
                      this library and stop syncing. Files already downloaded onto that device stay there. To reconnect
                      it later you will need a new pairing code.
                    </p>
                    <div className="flex gap-2">
                      <Button
                        variant="primary"
                        size="sm"
                        aria-label={`Confirm unpairing ${d.name}`}
                        disabled={unpairingId === d.id}
                        onClick={() => void onUnpairDevice(d.id)}
                      >
                        {unpairingId === d.id ? 'Unpairing...' : 'Yes, unpair'}
                      </Button>
                      <Button variant="secondary" size="sm" onClick={() => setConfirmUnpairId(null)}>
                        Cancel
                      </Button>
                    </div>
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}
        {syncStatus && (
          <p className="text-xs text-text-secondary">
            Sync status: revision {syncStatus.revision}, {syncStatus.deviceCount} device(s)
          </p>
        )}
        {syncError && (
          <p role="alert" className="text-xs text-error">
            {syncError}
          </p>
        )}
      </section>

      {/* Redeem pairing code */}
      <section className="rounded-lg border border-border-subtle bg-raised p-6 space-y-4">
        <div className="flex flex-wrap items-center gap-2">
          <span className="inline-flex items-center rounded-full bg-accent px-2.5 py-0.5 text-xs font-bold text-on-accent">
            Step 2
          </span>
          <span className="inline-flex items-center rounded-full border border-border-subtle bg-surface px-2.5 py-0.5 text-xs font-semibold text-text-secondary">
            Do this on Device B — the new device you want to add
          </span>
        </div>
        <h2 className="text-lg font-extrabold tracking-tight text-text-primary">Enter pairing code</h2>
        {paired ? (
          <div className="space-y-3">
            <p className="text-sm text-text-secondary">
              This device is paired. A sync token is stored on this device.
            </p>
            {redeemSuccess && (
              <p role="status" className="text-sm text-green-400">
                {redeemSuccess}
              </p>
            )}
            <Button variant="secondary" size="sm" onClick={onClearPaired}>
              Clear sync token
            </Button>
            <p className="text-xs text-text-muted">Clear the sync token to pair this device again with a new pairing code.</p>
          </div>
        ) : (
          <form onSubmit={onRedeem} className="space-y-4">
            <div className="space-y-1">
              <label htmlFor="pairing-code-input" className="text-sm font-semibold text-text-primary">
                Pairing code from your other device
              </label>
              <p className="text-xs leading-relaxed text-text-muted">
                Paste the <span className="font-mono font-semibold">XXXX-XXXX</span> code you generated in Step 1 on
                Device A. This field is on the <span className="font-semibold">new</span> device (Device B).
              </p>
              <input
                id="pairing-code-input"
                aria-label="Pairing code"
                placeholder="XXXX-XXXX"
                value={redeemInput}
                onChange={(e) => setRedeemInput(formatPairingInput(e.target.value))}
                maxLength={9}
                className="w-full rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm font-mono tracking-widest text-text-primary placeholder:text-text-muted outline-none focus:border-accent focus:ring-1 focus:ring-accent"
              />
              <p className="text-xs text-text-muted">Accepts with or without dash; auto-uppercases to XXXX-XXXX.</p>
            </div>
            <div className="space-y-1">
              <label htmlFor="device-name-input" className="text-sm font-semibold text-text-primary">
                Device name for this device
              </label>
              <p className="text-xs leading-relaxed text-text-muted">
                How <em>this</em> device (Device B) will appear in the Paired devices list. Use any name you will
                recognise later.
              </p>
              <input
                id="device-name-input"
                aria-label="Device name"
                placeholder="e.g. Work Laptop"
                value={deviceName}
                onChange={(e) => setDeviceName(e.target.value)}
                className="w-full rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent focus:ring-1 focus:ring-accent"
              />
              <details className="group rounded-md border border-border-subtle bg-surface/60 px-3 py-2">
                <summary className="cursor-pointer list-none text-xs font-semibold text-text-secondary group-open:text-text-primary">
                  <span className="inline-flex items-center gap-1">
                    <span className="transition-transform group-open:rotate-90">›</span> How to find your device name
                  </span>
                </summary>
                <ul className="mt-2 list-disc space-y-1 pl-4 text-xs leading-relaxed text-text-muted">
                  <li>
                    <span className="font-semibold text-text-secondary">Windows:</span> Settings → System → About →
                    Device name
                  </li>
                  <li>
                    <span className="font-semibold text-text-secondary">macOS:</span> System Settings → General →
                    About → Name
                  </li>
                  <li>
                    <span className="font-semibold text-text-secondary">Linux:</span> run{' '}
                    <code className="rounded bg-raised px-1 py-0.5 font-mono">hostname</code> in a terminal
                  </li>
                  <li>
                    <span className="font-semibold text-text-secondary">iPhone / iPad:</span> Settings → General →
                    About → Name
                  </li>
                  <li>
                    <span className="font-semibold text-text-secondary">Android:</span> Settings → About phone →
                    Device name
                  </li>
                  <li className="list-none pl-0 pt-1 text-text-muted">
                    Or just pick anything memorable, e.g. “Kitchen Tablet” or “John’s Phone”.
                  </li>
                </ul>
              </details>
            </div>
            {redeemError && (
              <p role="alert" className="text-sm text-error">
                {redeemError}
              </p>
            )}
            {redeemSuccess && (
              <p role="status" className="text-sm text-green-400">
                {redeemSuccess}
              </p>
            )}
            <Button type="submit" variant="primary" size="sm" disabled={redeemLoading}>
              {redeemLoading ? 'Pairing...' : 'Pair device'}
            </Button>
          </form>
        )}
      </section>
    </div>
  )
}

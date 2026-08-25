import { useEffect, useState } from 'react'
import {
  generatePairingCode,
  redeemPairingCode,
  listDevices,
  deleteDevice,
  getSyncStatus,
  storeSyncCredentials,
  getSyncToken,
  clearSyncCredentials,
  type PairingCode,
  type DeviceInfo,
} from '../lib/pairingApi'
import { useDocumentTitle } from '../lib/useDocumentTitle'
import { Button } from '../components/ui/Button'

function formatPairingInput(value: string): string {
  const raw = value.replace(/[^a-zA-Z0-9]/g, '').toUpperCase().slice(0, 8)
  if (raw.length <= 4) return raw
  return `${raw.slice(0, 4)}-${raw.slice(4)}`
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

  const [pairingCode, setPairingCode] = useState<PairingCode | null>(null)
  const [genLoading, setGenLoading] = useState(false)
  const [genError, setGenError] = useState<string | null>(null)
  const [nowSec, setNowSec] = useState(() => Math.floor(Date.now() / 1000))
  const [copied, setCopied] = useState(false)

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

  const [syncStatus, setSyncStatus] = useState<{ revision: number; deviceCount: number } | null>(null)
  const [syncError, setSyncError] = useState<string | null>(null)

  async function refreshDevices() {
    setDevicesLoading(true)
    setDevicesError(null)
    try {
      const list = await listDevices()
      setDevices(list)
    } catch (e) {
      setDevicesError(e instanceof Error ? e.message : 'Could not load devices')
    } finally {
      setDevicesLoading(false)
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
  }, [])
  /* eslint-enable react-hooks/set-state-in-effect */

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

  async function onDeleteDevice(id: string) {
    try {
      await deleteDevice(id)
      void refreshDevices()
      void refreshSync()
    } catch (e) {
      setDevicesError(e instanceof Error ? e.message : 'Could not delete device')
    }
  }

  function onClearPaired() {
    clearSyncCredentials()
    setPaired(false)
    setRedeemSuccess(null)
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
          Pair a device with the server using a one-time pairing code. The sync token is stored on this device to
          authorize future sync.
        </p>
      </header>

      {/* Generate pairing code */}
      <section className="rounded-lg border border-border-subtle bg-raised p-6 space-y-4">
        <h2 className="text-lg font-extrabold tracking-tight text-text-primary">Generate pairing code</h2>
        <p className="text-xs text-text-secondary">
          Create a one-time pairing code to add another device. The pairing code expires in 10 minutes and can only
          be used once. Share it with the device you want to pair.
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
              <p className="text-xs text-text-secondary">Code expires in {formatExpiry(expiresIn)}</p>
            )}
          </div>
        )}
      </section>

      {/* Paired devices */}
      <section className="rounded-lg border border-border-subtle bg-raised p-6 space-y-4">
        <h2 className="text-lg font-extrabold tracking-tight text-text-primary">Paired devices</h2>
        <p className="text-xs text-text-secondary">All devices paired with this server. The server device cannot be removed.</p>
        {devicesLoading ? (
          <p className="text-sm text-text-muted">Loading devices...</p>
        ) : devicesError ? (
          <p role="alert" className="text-sm text-error">
            {devicesError}
          </p>
        ) : devices && devices.length === 0 ? (
          <p className="text-sm text-text-muted">No devices found.</p>
        ) : (
          <ul className="space-y-2">
            {devices?.map((d) => (
              <li
                key={d.id}
                className="flex items-center justify-between gap-3 rounded-md border border-border-subtle bg-surface px-3 py-2"
              >
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-semibold text-text-primary truncate">{d.name}</span>
                    {d.isServer && (
                      <span className="inline-flex items-center rounded-full bg-accent px-2 py-0.5 text-xs font-bold text-on-accent">
                        server
                      </span>
                    )}
                  </div>
                  <div className="text-xs text-text-muted font-mono truncate">{d.id}</div>
                </div>
                {d.isServer ? (
                  <span className="text-xs text-text-muted">cannot delete server device</span>
                ) : (
                  <Button variant="ghost" size="sm" aria-label={`Delete device ${d.name}`} onClick={() => void onDeleteDevice(d.id)}>
                    Delete
                  </Button>
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
          <form onSubmit={onRedeem} className="space-y-3">
            <div className="space-y-1">
              <label htmlFor="pairing-code-input" className="text-sm font-semibold text-text-primary">
                Pairing code
              </label>
              <input
                id="pairing-code-input"
                aria-label="Pairing code"
                placeholder="XXXX-XXXX"
                value={redeemInput}
                onChange={(e) => setRedeemInput(formatPairingInput(e.target.value))}
                maxLength={9}
                className="w-full rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm font-mono tracking-widest text-text-primary placeholder:text-text-muted outline-none focus:border-accent focus:ring-1 focus:ring-accent"
              />
              <p className="text-xs text-text-muted">Accepts with or without dash; auto-uppercase.</p>
            </div>
            <div className="space-y-1">
              <label htmlFor="device-name-input" className="text-sm font-semibold text-text-primary">
                Device name
              </label>
              <input
                id="device-name-input"
                aria-label="Device name"
                placeholder="Laptop"
                value={deviceName}
                onChange={(e) => setDeviceName(e.target.value)}
                className="w-full rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent focus:ring-1 focus:ring-accent"
              />
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

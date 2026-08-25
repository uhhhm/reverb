import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  generatePairingCode,
  redeemPairingCode,
  listDevices,
  deleteDevice,
  getSyncStatus,
  storeSyncCredentials,
  getSyncToken,
  clearSyncCredentials,
  SYNC_TOKEN_KEY,
  SYNC_DEVICE_ID_KEY,
} from './pairingApi'

const fetchMock = vi.fn()

beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock)
  fetchMock.mockReset()
  window.localStorage.clear()
})

afterEach(() => {
  vi.unstubAllGlobals()
  window.localStorage.clear()
})

function ok(body: unknown) {
  return Promise.resolve({
    ok: true,
    status: 200,
    text: () => Promise.resolve(JSON.stringify(body)),
  } as Response)
}

describe('pairingApi', () => {
  it('generatePairingCode POSTs /pairing/code', async () => {
    fetchMock.mockReturnValue(ok({ code: 'AB12-CD34', expiresAt: 999 }))
    const out = await generatePairingCode()
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/pairing/code', expect.objectContaining({ method: 'POST' }))
    expect(out.code).toBe('AB12-CD34')
    expect(out.expiresAt).toBe(999)
  })

  it('redeemPairingCode POSTs /pairing/redeem with stripped code', async () => {
    fetchMock.mockReturnValue(ok({ deviceId: 'dev_1', token: 'tok', serverDeviceId: 'srv' }))
    await redeemPairingCode('ab12-cd34', 'My Laptop')
    const call = fetchMock.mock.calls[0]
    expect(call[0]).toBe('/api/v1/pairing/redeem')
    expect(call[1]).toMatchObject({ method: 'POST' })
    const body = JSON.parse((call[1] as RequestInit).body as string)
    expect(body.code).toBe('ab12cd34')
    expect(body.deviceName).toBe('My Laptop')
  })

  it('redeemPairingCode strips dashes', async () => {
    fetchMock.mockReturnValue(ok({ deviceId: 'dev_1', token: 'tok', serverDeviceId: 'srv' }))
    await redeemPairingCode('ABCD-1234', 'Laptop')
    const body = JSON.parse((fetchMock.mock.calls[0][1] as RequestInit).body as string)
    expect(body.code).toBe('ABCD1234')
  })

  it('redeemPairingCode handles code without dash', async () => {
    fetchMock.mockReturnValue(ok({ deviceId: 'dev_1', token: 'tok', serverDeviceId: 'srv' }))
    await redeemPairingCode('ABCD1234', 'Laptop')
    const body = JSON.parse((fetchMock.mock.calls[0][1] as RequestInit).body as string)
    expect(body.code).toBe('ABCD1234')
  })

  it('listDevices GETs /pairing/devices', async () => {
    fetchMock.mockReturnValue(ok([{ id: 'd1', name: 'server', isServer: true, createdAt: 1, lastSeen: 2 }]))
    const out = await listDevices()
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/pairing/devices', expect.objectContaining({ method: 'GET' }))
    expect(out[0].id).toBe('d1')
  })

  it('deleteDevice DELETEs /pairing/devices/:id', async () => {
    fetchMock.mockReturnValue(ok({ ok: true }))
    const out = await deleteDevice('d1')
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/pairing/devices/d1', expect.objectContaining({ method: 'DELETE' }))
    expect(out.ok).toBe(true)
  })

  it('getSyncStatus GETs /sync/status', async () => {
    fetchMock.mockReturnValue(ok({ revision: 5, deviceCount: 2 }))
    const out = await getSyncStatus()
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/sync/status', expect.objectContaining({ method: 'GET' }))
    expect(out.revision).toBe(5)
  })

  it('storeSyncCredentials and getSyncToken and clearSyncCredentials use localStorage', () => {
    expect(getSyncToken()).toBeNull()
    storeSyncCredentials('tok123', 'dev123')
    expect(window.localStorage.getItem(SYNC_TOKEN_KEY)).toBe('tok123')
    expect(window.localStorage.getItem(SYNC_DEVICE_ID_KEY)).toBe('dev123')
    expect(getSyncToken()).toBe('tok123')
    clearSyncCredentials()
    expect(getSyncToken()).toBeNull()
    expect(window.localStorage.getItem(SYNC_DEVICE_ID_KEY)).toBeNull()
  })
})

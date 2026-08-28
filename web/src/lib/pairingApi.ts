import { api } from './api'

export interface PairingCode {
  code: string
  expiresAt: number
}

export interface RedeemResult {
  deviceId: string
  token: string
  serverDeviceId: string
}

export interface DeviceInfo {
  id: string
  name: string
  isServer: boolean
  createdAt: number
  lastSeen: number
}

export const SYNC_TOKEN_KEY = 'reverb:syncToken'
export const SYNC_DEVICE_ID_KEY = 'reverb:syncDeviceId'

export function generatePairingCode(): Promise<PairingCode> {
  return api.post<PairingCode>('/pairing/code')
}

export function redeemPairingCode(code: string, deviceName: string): Promise<RedeemResult> {
  const normalized = code.replace(/-/g, '').trim()
  return api.post<RedeemResult>('/pairing/redeem', { code: normalized, deviceName })
}

export function listDevices(): Promise<DeviceInfo[]> {
  return api.get<DeviceInfo[]>('/pairing/devices')
}

export function deleteDevice(id: string): Promise<{ ok: boolean }> {
  return api.del<{ ok: boolean }>(`/pairing/devices/${encodeURIComponent(id)}`)
}

export function getSyncStatus(): Promise<{ revision: number; deviceCount: number }> {
  return api.get<{ revision: number; deviceCount: number }>('/sync/status')
}

export function storeSyncCredentials(token: string, deviceId: string): void {
  if (typeof window === 'undefined') return
  window.localStorage.setItem(SYNC_TOKEN_KEY, token)
  window.localStorage.setItem(SYNC_DEVICE_ID_KEY, deviceId)
}

export function getSyncToken(): string | null {
  if (typeof window === 'undefined') return null
  return window.localStorage.getItem(SYNC_TOKEN_KEY)
}

export function getSyncDeviceId(): string | null {
  if (typeof window === 'undefined') return null
  return window.localStorage.getItem(SYNC_DEVICE_ID_KEY)
}

export function clearSyncCredentials(): void {
  if (typeof window === 'undefined') return
  window.localStorage.removeItem(SYNC_TOKEN_KEY)
  window.localStorage.removeItem(SYNC_DEVICE_ID_KEY)
}

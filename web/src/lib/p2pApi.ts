import { api } from './api'
import type { components } from './generated/api'

export interface P2PStatus {
  peerId: string
  /** The device ID this instance authors sync changes under, when known. */
  deviceId?: string
  addrs: string[]
  /**
   * Complete /p2p/-terminated addresses another device can dial this one on.
   * On a VPN the peer ID alone is not enough, so this is what the pairing UI
   * shows the user to copy across.
   */
  dialAddrs: string[]
  peerCount: number
  vector: Record<string, number>
  hlc: number
}

export interface P2PPeer {
  peerId: string
  addrs: string[]
  conns: number
  connected: boolean
}

export interface FileManifest {
  canonicalId: string
  contentHash: string
  size: number
  relPath: string
  mtime: number
  deviceId: string
}

export function getP2PStatus(): Promise<P2PStatus> {
  return api.get<P2PStatus>('/p2p/status')
}

export function getP2PPeers(): Promise<P2PPeer[]> {
  return api.get<P2PPeer[]>('/p2p/peers')
}

/**
 * peerId is a bare peer ID or a full multiaddr ending in /p2p/<peerID>, or
 * empty to offer the code to every Reverb device discovered on the local
 * network. Over a VPN discovery finds nothing, so the address is required there.
 */
export function redeemViaPeer(peerId: string, code: string, deviceName: string): Promise<{ deviceId: string; token: string }> {
  return api.post<{ deviceId: string; token: string }>('/p2p/pair/redeem', { peerId: peerId.trim(), code, deviceName })
}

export function getFileManifests(): Promise<FileManifest[]> {
  return api.get<FileManifest[]>('/p2p/manifests')
}

export function fetchFileFromPeer(peerId: string, relPath: string, contentHash: string): Promise<{ ok: boolean }> {
  return api.post<{ ok: boolean }>('/p2p/fetch', { peerId, relPath, contentHash })
}

/**
 * A file this device could not copy from a peer, and why. `reason` is a stable
 * token so the wording lives in the UI rather than the database; the shape is
 * taken from the generated contract so the three spellings of that token — Go,
 * OpenAPI, TypeScript — cannot drift apart.
 */
export type FileFetchFailure = components['schemas']['FileFetchFailure']

export function getFileFetchFailures(): Promise<FileFetchFailure[]> {
  return api.get<FileFetchFailure[]>('/p2p/file-failures')
}

export type PortableNameMigration = components['schemas']['PortableNameMigration']

/**
 * Renames the library's existing files onto names every device in the household
 * can store. Safe to interrupt and safe to repeat: a run cut short leaves every
 * file at either its old name or its new one, and asking again finishes the job.
 */
export function migratePortableNames(): Promise<PortableNameMigration> {
  return api.post<PortableNameMigration>('/p2p/portable-names', {})
}

/**
 * How many of this device's files carry a name another device in the household
 * could not store.
 *
 * The device holding those names is not the device that notices them — a Linux
 * box stores them perfectly well — so the offer to migrate has to be driven by
 * each device's own library rather than by what a peer failed to copy.
 */
export function getPortableNamesPending(): Promise<{ pending: number }> {
  return api.get<{ pending: number }>('/p2p/portable-names')
}

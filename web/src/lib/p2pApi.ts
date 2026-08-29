import { api } from './api'

export interface P2PStatus {
  peerId: string
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

/** peerId is a bare peer ID or a full multiaddr ending in /p2p/<peerID>. */
export function redeemViaPeer(peerId: string, code: string, deviceName: string): Promise<{ deviceId: string; token: string }> {
  return api.post<{ deviceId: string; token: string }>('/p2p/pair/redeem', { peerId, code, deviceName })
}

export function getFileManifests(): Promise<FileManifest[]> {
  return api.get<FileManifest[]>('/p2p/manifests')
}

export function fetchFileFromPeer(peerId: string, relPath: string, contentHash: string): Promise<{ ok: boolean }> {
  return api.post<{ ok: boolean }>('/p2p/fetch', { peerId, relPath, contentHash })
}

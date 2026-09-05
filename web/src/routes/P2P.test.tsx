import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import P2P from './P2P'
import { listDevices, deleteDevice } from '../lib/pairingApi'
import { triggerSync } from '../lib/syncApi'
import { useSyncStore } from '../lib/syncStore'

vi.mock('../lib/pairingApi', () => ({
  generatePairingCode: vi.fn(), listDevices: vi.fn(), deleteDevice: vi.fn(),
}))
vi.mock('../lib/syncApi', () => ({ triggerSync: vi.fn() }))
vi.mock('../lib/p2pApi', () => ({
  getP2PStatus: vi.fn().mockResolvedValue({ peerId: 'local', addrs: [], dialAddrs: [], peerCount: 0 }),
  getP2PPeers: vi.fn().mockResolvedValue([]),
  getFileManifests: vi.fn().mockResolvedValue([]),
  redeemViaPeer: vi.fn(), fetchFileFromPeer: vi.fn(),
}))

const server = { id: 'server', name: 'Server', isServer: true, createdAt: 1, lastSeen: 1 }
const laptop = { id: 'laptop', name: 'Laptop', isServer: false, createdAt: 1, lastSeen: 0 }
function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><P2P /></QueryClientProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(listDevices).mockResolvedValue([server, laptop])
  vi.mocked(deleteDevice).mockResolvedValue({ ok: true })
  vi.mocked(triggerSync).mockResolvedValue({ status: 'started' })
  useSyncStore.setState({ syncing: false })
})

describe('P2P device management', () => {
  it('lists paired devices and protects the server from removal', async () => {
    renderPage()
    expect(await screen.findByText('Laptop')).toBeInTheDocument()
    expect(screen.getByText('Server device')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Remove Server' })).not.toBeInTheDocument()
  })

  it('requires confirmation, supports cancellation, and refreshes after removal', async () => {
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Remove Laptop' }))
    const dialog = screen.getByRole('dialog', { name: 'Remove Laptop?' })
    expect(deleteDevice).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(deleteDevice).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Remove Laptop' }))
    vi.mocked(listDevices).mockResolvedValue([server])
    fireEvent.click(screen.getByRole('button', { name: 'Remove device' }))
    await waitFor(() => expect(deleteDevice).toHaveBeenCalledWith('laptop', expect.anything()))
    await waitFor(() => expect(screen.queryByText('Laptop')).not.toBeInTheDocument())
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('keeps removal failures in the confirmation popup', async () => {
    vi.mocked(deleteDevice).mockRejectedValue(new Error('Removal failed'))
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Remove Laptop' }))
    fireEvent.click(screen.getByRole('button', { name: 'Remove device' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Removal failed')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByText('Laptop')).toBeInTheDocument()
  })

  it('triggers manual sync and disables the button during the round', async () => {
    renderPage()
    await screen.findByText('Laptop')
    fireEvent.click(screen.getByRole('button', { name: 'Sync now' }))
    await waitFor(() => expect(triggerSync).toHaveBeenCalledTimes(1))
    act(() => useSyncStore.getState().setSyncing(true))
    expect(screen.getByRole('button', { name: 'Syncing…' })).toBeDisabled()
    act(() => useSyncStore.getState().setSyncing(false))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Sync now' })).toBeEnabled())
  })

  it('shows a failed sync request', async () => {
    vi.mocked(triggerSync).mockRejectedValue(new Error('Sync unavailable'))
    renderPage()
    await screen.findByText('Laptop')
    fireEvent.click(screen.getByRole('button', { name: 'Sync now' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Sync unavailable')
    expect(screen.getByRole('button', { name: 'Sync now' })).toBeEnabled()
  })
})

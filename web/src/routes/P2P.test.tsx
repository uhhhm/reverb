import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import P2P from './P2P'
import { listDevices, deleteDevice } from '../lib/pairingApi'
import { triggerSync, getSyncStatus } from '../lib/syncApi'
import { useSyncStore } from '../lib/syncStore'
import { getFileFetchFailures, migratePortableNames, getPortableNamesPending } from '../lib/p2pApi'

vi.mock('../lib/pairingApi', () => ({
  generatePairingCode: vi.fn(), listDevices: vi.fn(), deleteDevice: vi.fn(),
}))
vi.mock('../lib/syncApi', () => ({ triggerSync: vi.fn(), getSyncStatus: vi.fn() }))
vi.mock('../lib/p2pApi', () => ({
  getP2PStatus: vi.fn().mockResolvedValue({ peerId: 'local', addrs: [], dialAddrs: [], peerCount: 0 }),
  getP2PPeers: vi.fn().mockResolvedValue([]),
  getFileManifests: vi.fn().mockResolvedValue([]),
  getFileFetchFailures: vi.fn().mockResolvedValue([]),
  migratePortableNames: vi.fn(),
  getPortableNamesPending: vi.fn().mockResolvedValue({ pending: 0 }),
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
  vi.mocked(getSyncStatus).mockResolvedValue({ revision: 0, deviceCount: 2 })
  useSyncStore.setState({ syncing: false })
  vi.mocked(getFileFetchFailures).mockResolvedValue([])
  vi.mocked(getPortableNamesPending).mockResolvedValue({ pending: 0 })
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

it('recovers pending and completed sync status without any WebSocket events', async () => {
  const pending = { id: 1, state: 'pending' as const, startedAt: 1000, finishedAt: 0, durationMs: 0, peers: 0, succeeded: 0, errors: [] }
  vi.mocked(triggerSync).mockResolvedValue({ status: 'started', round: pending })
  renderPage()
  await screen.findByText('Laptop')
  vi.mocked(getSyncStatus).mockResolvedValue({ revision: 0, deviceCount: 2, round: pending })
  fireEvent.click(screen.getByRole('button', { name: 'Sync now' }))
  expect(await screen.findByText('Sync pending…')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Syncing…' })).toBeDisabled()
  vi.mocked(getSyncStatus).mockResolvedValue({ revision: 1, deviceCount: 2, round: { ...pending, state: 'completed', finishedAt: 2000, durationMs: 1000, peers: 1, succeeded: 1 } })
  expect(await screen.findByText(/Library exchange finished with 1 device/, {}, { timeout: 2500 })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Sync now' })).toBeEnabled()
})

it('shows device failures instead of a successful request acknowledgement', async () => {
  vi.mocked(getSyncStatus).mockResolvedValue({ revision: 0, deviceCount: 2, round: { id: 1, state: 'failed', startedAt: 1000, finishedAt: 2000, durationMs: 1000, peers: 1, succeeded: 0, errors: ['Laptop: pairing required'] } })
  renderPage()
  expect(await screen.findByText('Laptop: pairing required')).toBeInTheDocument()
  expect(screen.getByText(/Sync finished with errors/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Sync now' })).toBeEnabled()
})

it('explains when there are no paired sync devices', async () => {
  vi.mocked(getSyncStatus).mockResolvedValue({ revision: 0, deviceCount: 1, round: { id: 1, state: 'no_peers', startedAt: 1000, finishedAt: 2000, durationMs: 0, peers: 0, succeeded: 0, errors: [] } })
  renderPage()
  expect(await screen.findByText(/No paired devices to sync with/)).toBeInTheDocument()
})

describe('files that are not replicating', () => {
  // A file silently absent forever is worse than one reported as failed: the
  // owner cannot tell it apart from one still in flight.
  it('names the stuck file and explains why, in the owner\'s terms', async () => {
    vi.mocked(getFileFetchFailures).mockResolvedValue([
      {
        peerId: 'peer-1',
        contentHash: 'abc',
        relPath: 'Pixies/Where Is My Mind?.flac',
        reason: 'unstorable-path',
        detail: "this device's filesystem cannot store the name",
        attempts: 0,
        firstFailedAt: 1,
        lastFailedAt: 1,
        nextAttemptAt: Date.now() + 3_600_000,
      },
    ])
    renderPage()
    expect(await screen.findByText('Pixies/Where Is My Mind?.flac')).toBeInTheDocument()
    expect(screen.getByText(/cannot store that file name/i)).toBeInTheDocument()
  })

  // Backing off is not giving up, and the owner should be able to see that.
  it('says the file is still being retried rather than abandoned', async () => {
    vi.mocked(getFileFetchFailures).mockResolvedValue([
      {
        peerId: 'peer-1',
        contentHash: 'abc',
        relPath: 'A/1.flac',
        reason: 'unavailable',
        detail: 'stream reset',
        attempts: 3,
        firstFailedAt: 1,
        lastFailedAt: 1,
        nextAttemptAt: Date.now() + 3_600_000,
      },
    ])
    renderPage()
    expect(await screen.findByText(/retrying in/i)).toBeInTheDocument()
  })

  // With nothing stuck the section stays out of the way.
  it('shows nothing when every file is replicating', async () => {
    renderPage()
    expect(await screen.findByText('Laptop')).toBeInTheDocument()
    expect(screen.queryByText('Files that are not replicating')).not.toBeInTheDocument()
  })
})

describe('making an established library portable', () => {
  // The device holding the unportable names is the one that must migrate, and
  // it is not the device that notices: a Linux box stores "Where Is My Mind?"
  // quite happily and records no failure at all, while the Windows peer that
  // cannot copy it has nothing to rename. Gating the offer on pull failures put
  // the button on the only device that could not use it.
  it('offers the rename on the device that holds the names, with no failures recorded', async () => {
    vi.mocked(getFileFetchFailures).mockResolvedValue([])
    vi.mocked(getPortableNamesPending).mockResolvedValue({ pending: 12 })
    vi.mocked(migratePortableNames).mockResolvedValue({
      renamed: [{ from: 'Pixies/Where Is My Mind?.flac', to: 'Pixies/Where Is My Mind_.flac' }],
      failed: [],
      examined: 12,
    })
    renderPage()
    const button = await screen.findByRole('button', { name: /rename them for every device/i })
    fireEvent.click(button)
    expect(await screen.findByText(/Renamed 1 of 12 entries/)).toBeInTheDocument()
  })

  it('says nothing when this device holds no unstorable names', async () => {
    renderPage()
    expect(await screen.findByText('Laptop')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /rename them for every device/i })).not.toBeInTheDocument()
  })

  // A file stuck because a peer will not serve it is not a naming problem.
  it('does not offer the rename just because a copy failed', async () => {
    vi.mocked(getFileFetchFailures).mockResolvedValue([
      {
        peerId: 'peer-1',
        contentHash: 'abc',
        relPath: 'A/1.flac',
        reason: 'unavailable' as const,
        detail: 'stream reset',
        attempts: 3,
        firstFailedAt: 1,
        lastFailedAt: 1,
        nextAttemptAt: Date.now() + 3_600_000,
      },
    ])
    renderPage()
    expect(await screen.findByText('A/1.flac')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /rename them for every device/i })).not.toBeInTheDocument()
  })
})

// A failure is recorded per device, because one that no longer holds a file
// says nothing about a device that does. Two devices failing the same track are
// two separate things the owner may need to act on.
describe('the same file failing from two devices', () => {
  it('lists each device separately', async () => {
    const base = {
      contentHash: 'abc',
      relPath: 'A/1.flac',
      reason: 'unavailable' as const,
      detail: 'stream reset',
      attempts: 3,
      firstFailedAt: 1,
      lastFailedAt: 1,
      nextAttemptAt: Date.now() + 3_600_000,
    }
    vi.mocked(getFileFetchFailures).mockResolvedValue([
      { ...base, peerId: 'peer-1' },
      { ...base, peerId: 'peer-2' },
    ])
    renderPage()
    expect(await screen.findAllByText('A/1.flac')).toHaveLength(2)
  })
})

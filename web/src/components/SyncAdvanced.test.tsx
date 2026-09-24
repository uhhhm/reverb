import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SyncAdvanced } from './SyncAdvanced'
import { getFileFetchFailures, migratePortableNames, getPortableNamesPending } from '../lib/p2pApi'

vi.mock('../lib/p2pApi', () => ({
  getP2PStatus: vi.fn().mockResolvedValue({ peerId: 'local', addrs: [], dialAddrs: [], peerCount: 0 }),
  getP2PPeers: vi.fn().mockResolvedValue([]),
  getFileManifests: vi.fn().mockResolvedValue([]),
  getFileFetchFailures: vi.fn().mockResolvedValue([]),
  migratePortableNames: vi.fn(),
  getPortableNamesPending: vi.fn().mockResolvedValue({ pending: 0 }),
  fetchFileFromPeer: vi.fn(),
}))

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><SyncAdvanced /></QueryClientProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getFileFetchFailures).mockResolvedValue([])
  vi.mocked(getPortableNamesPending).mockResolvedValue({ pending: 0 })
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
    expect(await screen.findByText('local')).toBeInTheDocument()
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
    expect(await screen.findByText('local')).toBeInTheDocument()
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

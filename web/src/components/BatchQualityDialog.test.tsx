import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BatchQualityDialog, type QualitySubject } from './BatchQualityDialog'
import type { Track } from '../lib/types'

const mockSetBatch = vi.fn().mockResolvedValue({ applied: 2 })
const mockUpgrade = vi.fn().mockResolvedValue({})

vi.mock('../lib/trackQualityApi', () => ({
  useSetTrackQualityBatch: () => ({ mutateAsync: mockSetBatch, isPending: false }),
}))
vi.mock('../lib/upgradeApi', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/upgradeApi')>()),
  useUpgradeDownload: () => ({ mutateAsync: mockUpgrade, isPending: false }),
}))
vi.mock('../lib/toastStore', () => ({
  useToastStore: (sel: (s: unknown) => unknown) => sel({ push: vi.fn() }),
}))

function track(id: string, over: Partial<Track> = {}): Track {
  return {
    id,
    title: `Song ${id}`,
    albumId: 'al1',
    album: 'Album',
    artistId: 'ar1',
    artist: 'Artist',
    coverArtId: '',
    trackNumber: 1,
    discNumber: 1,
    durationMs: 1000,
    bitRate: 160,
    suffix: 'mp3',
    contentType: 'audio/mpeg',
    ...over,
  }
}

function renderDialog(subjects: QualitySubject[], onClose = vi.fn(), onApplied = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <BatchQualityDialog subjects={subjects} onClose={onClose} onApplied={onApplied} />
    </QueryClientProvider>,
  )
  return { onClose, onApplied }
}

const refetchable = (quality: string) => ({
  jobId: 'j1',
  source: 'spotify',
  externalId: 'sp1',
  artist: 'Artist',
  title: 'Song',
  album: 'Album',
  quality: quality as 'low' | 'medium' | 'high' | 'best',
  libraryTrackId: 't1',
})

describe('BatchQualityDialog', () => {
  beforeEach(() => vi.clearAllMocks())

  it('shows the kbps ceiling on each tier so the numbers are visible in the picker', () => {
    renderDialog([{ track: track('t1') }])
    expect(screen.getByText('Low (up to 128 kbps)')).toBeInTheDocument()
    expect(screen.getByText('Medium (up to 192 kbps)')).toBeInTheDocument()
    expect(screen.getByText('High (up to 320 kbps)')).toBeInTheDocument()
    // "Best" has no ceiling, so it says what it does instead of a number.
    expect(screen.getByText('Best (no re-encode)')).toBeInTheDocument()
  })

  it('saves the standing quality for every selected track without re-downloading', async () => {
    const { onApplied } = renderDialog([{ track: track('t1') }, { track: track('t2') }])

    fireEvent.click(screen.getByRole('radio', { name: /Medium/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(mockSetBatch).toHaveBeenCalledTimes(1))
    expect(mockSetBatch).toHaveBeenCalledWith({ trackIds: ['t1', 't2'], quality: 'medium' })
    expect(mockUpgrade).not.toHaveBeenCalled()
    await waitFor(() => expect(onApplied).toHaveBeenCalled())
  })

  it('clears the override when "Follow the default" is chosen', async () => {
    renderDialog([{ track: track('t1') }])

    fireEvent.click(screen.getByRole('radio', { name: /Follow the default/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(mockSetBatch).toHaveBeenCalledWith({ trackIds: ['t1'], quality: '' }))
  })

  it('only offers to re-download tracks that have a source and are not already at the tier', () => {
    renderDialog([
      // Re-fetchable, currently low — a change at "high".
      { track: track('t1'), refetch: refetchable('low') },
      // Re-fetchable but already high — re-fetching would produce the same file.
      { track: track('t2'), refetch: refetchable('high') },
      // Not in download history at all, so there is no source to fetch from.
      { track: track('t3') },
    ])

    // "high" is the default choice, so only t1 counts.
    expect(screen.getByRole('button', { name: 'Re-download 1' })).toBeEnabled()
    expect(screen.getByText(/1 of 3 can be re-fetched/)).toBeInTheDocument()
  })

  it('disables re-download when nothing in the selection can be re-fetched', () => {
    renderDialog([{ track: track('t1') }])
    expect(screen.getByRole('button', { name: 'Re-download 0' })).toBeDisabled()
    expect(screen.getByText(/None of the selected tracks can be re-fetched/)).toBeInTheDocument()
  })

  it('queues a re-download per re-fetchable track at the chosen tier', async () => {
    renderDialog([
      { track: track('t1'), refetch: refetchable('low') },
      { track: track('t2'), refetch: { ...refetchable('low'), libraryTrackId: 't2' } },
    ])

    fireEvent.click(screen.getByRole('button', { name: 'Re-download 2' }))

    await waitFor(() => expect(mockUpgrade).toHaveBeenCalledTimes(2))
    expect(mockUpgrade.mock.calls[0][0]).toMatchObject({
      quality: 'high',
      currentQuality: 'low',
      libraryTrackId: 't1',
    })
    // Re-downloading is its own action: it must not quietly change the standing
    // quality as well.
    expect(mockSetBatch).not.toHaveBeenCalled()
  })

  it('renders nothing without a selection', () => {
    const { container } = render(
      <QueryClientProvider client={new QueryClient()}>
        <BatchQualityDialog subjects={null} onClose={vi.fn()} />
      </QueryClientProvider>,
    )
    expect(container).toBeEmptyDOMElement()
  })
})

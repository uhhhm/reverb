import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import ManageTracks from './ManageTracks'
import type { Track } from '../lib/types'

const songs: Track[] = [
  {
    id: 't1',
    title: 'Downloaded Song',
    albumId: 'al1',
    album: 'First Album',
    artistId: 'ar1',
    artist: 'Alpha',
    coverArtId: 'c1',
    trackNumber: 1,
    discNumber: 1,
    durationMs: 200000,
    bitRate: 143,
    suffix: 'mp3',
    contentType: 'audio/mpeg',
  },
  {
    id: 't2',
    title: 'Ripped Song',
    albumId: 'al2',
    album: 'Second Album',
    artistId: 'ar2',
    artist: 'Beta',
    coverArtId: '',
    trackNumber: 1,
    discNumber: 1,
    durationMs: 200000,
    bitRate: 320,
    suffix: 'mp3',
    contentType: 'audio/mpeg',
  },
]

vi.mock('../lib/libraryApi', () => ({
  useSongs: () => ({ data: songs, isLoading: false }),
  useAlbums: () => ({ data: [{ id: 'al1', name: 'First Album', artist: 'Alpha', coverArtId: '' }], isLoading: false }),
  useArtists: () => ({ data: [{ id: 'ar1', name: 'Alpha', coverArtId: '' }], isLoading: false }),
  coverUrl: (id: string) => `/api/v1/cover/${id}`,
}))

vi.mock('../lib/trackQualityApi', () => ({
  useTrackQualityIndex: () => ({ data: { default: 'high', overrides: { t2: 'low' } } }),
}))

vi.mock('../lib/upgradeApi', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/upgradeApi')>()),
  // Only t1 was downloaded by Reverb, at "medium".
  useRefetchable: () => ({
    data: [
      {
        jobId: 'j1',
        source: 'spotify',
        externalId: 'sp1',
        artist: 'Alpha',
        title: 'Downloaded Song',
        album: 'First Album',
        quality: 'medium',
        libraryTrackId: 't1',
      },
    ],
  }),
}))

vi.mock('../components/BatchRenameDialog', () => ({
  BatchRenameDialog: ({ subject }: { subject: unknown }) =>
    subject ? <div data-testid="rename-dialog">{JSON.stringify(subject)}</div> : null,
}))
vi.mock('../components/BatchQualityDialog', () => ({
  BatchQualityDialog: ({ subjects }: { subjects: unknown[] | null }) =>
    subjects ? <div data-testid="quality-dialog">{subjects.length}</div> : null,
}))
vi.mock('../components/CoverUploadDialog', () => ({
  CoverUploadDialog: ({ targets }: { targets: unknown[] }) => (
    <div data-testid="cover-dialog">{JSON.stringify(targets)}</div>
  ),
}))

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <ManageTracks />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function rowFor(title: string): HTMLElement {
  return screen.getByText(title).closest('li') as HTMLElement
}

describe('ManageTracks', () => {
  beforeEach(() => vi.clearAllMocks())

  it('shows the real bitrate beside the tier it was fetched at', () => {
    renderPage()
    // The tier comes from download history, not from the bitrate: 143 kbps
    // would read as "medium" either way, but only history knows it was asked for.
    expect(within(rowFor('Downloaded Song')).getByText('143 kbps · Medium')).toBeInTheDocument()
  })

  it('falls back to the tier the bitrate implies for a track Reverb did not download', () => {
    renderPage()
    const row = rowFor('Ripped Song')
    expect(within(row).getByText('320 kbps · High')).toBeInTheDocument()
    expect(within(row).getByText(/not re-fetchable/)).toBeInTheDocument()
  })

  it('shows the standing quality, marking the ones following the global default', () => {
    renderPage()
    // t2 carries an override.
    expect(within(rowFor('Ripped Song')).getByText(/Next fetch: Low/)).toBeInTheDocument()
    // t1 has none, so it follows the default and says so.
    expect(within(rowFor('Downloaded Song')).getByText(/Next fetch: High \(default\)/)).toBeInTheDocument()
  })

  it('offers quality only on the tracks tab', () => {
    renderPage()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Downloaded Song' }))
    expect(screen.getByRole('button', { name: 'Quality…' })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Albums' }))
    fireEvent.click(screen.getByRole('button', { name: 'Select First Album' }))
    expect(screen.queryByRole('button', { name: 'Quality…' })).not.toBeInTheDocument()
    // Albums still carry artwork.
    expect(screen.getByRole('button', { name: 'Set cover…' })).toBeInTheDocument()
  })

  it('offers no cover action for artists, which carry no artwork of their own', () => {
    renderPage()
    fireEvent.click(screen.getByRole('button', { name: 'Artists' }))
    fireEvent.click(screen.getByRole('button', { name: 'Select Alpha' }))
    expect(screen.queryByRole('button', { name: 'Set cover…' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Rename…' })).toBeInTheDocument()
  })

  it('drops the selection when the tab changes, since ids do not carry across', () => {
    renderPage()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Downloaded Song' }))
    expect(screen.getByText('1 selected')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Albums' }))
    expect(screen.queryByText('1 selected')).not.toBeInTheDocument()
  })

  it('filters the list without losing a selection made before the filter', () => {
    renderPage()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Downloaded Song' }))

    fireEvent.change(screen.getByLabelText('Filter tracks'), { target: { value: 'ripped' } })
    expect(screen.queryByText('Downloaded Song')).not.toBeInTheDocument()
    expect(screen.getByText('Ripped Song')).toBeInTheDocument()
    // Selection is by id, so it is still counted while filtered out of view.
    expect(screen.getByText('1 selected')).toBeInTheDocument()
  })

  it('passes the selected tracks with their re-fetch entry to the quality dialog', () => {
    renderPage()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Downloaded Song' }))
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Ripped Song' }))
    fireEvent.click(screen.getByRole('button', { name: 'Quality…' }))
    expect(screen.getByTestId('quality-dialog')).toHaveTextContent('2')
  })
})

import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import ExternalPlaylist from './ExternalPlaylist'
import type { ExternalPlaylist as ExtPlaylist } from '../lib/types'

const mockPlayTrackList = vi.fn()
const mockPlayerState = { playTrackList: mockPlayTrackList, current: null, playing: false }
vi.mock('../lib/playerStore', () => ({
  usePlayer: (sel: (s: typeof mockPlayerState) => unknown) => sel(mockPlayerState),
}))

const mockUseExternalPlaylist = vi.fn()
vi.mock('../lib/syncedPlaylistApi', () => ({
  useExternalPlaylist: (...args: unknown[]) => mockUseExternalPlaylist(...args),
}))

vi.mock('../components/download/DownloadAction', () => ({
  DownloadAction: ({ result }: { result: { title: string } }) => (
    <button type="button" aria-label={`Download ${result.title}`}>Download</button>
  ),
}))

vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>()
  return { ...actual, useParams: () => ({ source: 'spotify', id: 'PL' }) }
})

const playlist: ExtPlaylist = {
  source: 'spotify',
  externalId: 'PL',
  name: 'Preview Mix',
  tracks: [
    { source: 'spotify', externalId: 'e1', title: 'Song One', artist: 'Artist A', album: 'Album A', durationMs: 180000, type: 'track' },
    { source: 'spotify', externalId: 'e2', title: 'Song Two', artist: 'Artist B', album: 'Album B', durationMs: 200000, type: 'track' },
  ],
}

function wrapper(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/playlist/spotify/PL']}>
        <Routes>
          <Route path="/playlist/:source/:id" element={ui} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('ExternalPlaylist preview playback', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUseExternalPlaylist.mockReturnValue({ data: playlist, isLoading: false, isError: false })
  })

  it('row play streams without downloading — carries externalStream', async () => {
    wrapper(<ExternalPlaylist />)
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Preview Mix' })).toBeInTheDocument())
    fireEvent.doubleClick(screen.getByText('Song One'))
    expect(mockPlayTrackList).toHaveBeenCalledOnce()
    const [tracks, idx] = mockPlayTrackList.mock.calls[0] as [import('../lib/types').Track[], number]
    expect(idx).toBe(0)
    expect(tracks[0]).toMatchObject({ externalStream: { source: 'spotify', externalId: 'e1' } })
  })

  it('header Play plays the full preview queue in order', async () => {
    wrapper(<ExternalPlaylist />)
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Preview Mix' })).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: /play preview mix/i }))
    expect(mockPlayTrackList).toHaveBeenCalledOnce()
    const [tracks, idx] = mockPlayTrackList.mock.calls[0] as [import('../lib/types').Track[], number]
    expect(idx).toBe(0)
    expect(tracks.map((t) => t.id)).toEqual(['spotify:e1', 'spotify:e2'])
  })
})

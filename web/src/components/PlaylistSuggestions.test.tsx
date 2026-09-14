import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider, type UseQueryResult } from '@tanstack/react-query'
import { PlaylistSuggestions } from './PlaylistSuggestions'
import { usePlaylistSuggestions, type SimilarTracksResult } from '../lib/recommendationsApi'
import { addSyncedTrack } from '../lib/syncedPlaylistApi'

vi.mock('../lib/recommendationsApi', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/recommendationsApi')>()
  return { ...actual, usePlaylistSuggestions: vi.fn() }
})
vi.mock('../lib/syncedPlaylistApi', () => ({ addSyncedTrack: vi.fn() }))
vi.mock('../lib/playerStore', () => ({
  usePlayer: (selector: (s: { playTrackList: () => void; current: null }) => unknown) => selector({ playTrackList: vi.fn(), current: null }),
}))

const result: SimilarTracksResult = {
  available: true,
  tracks: [
    { source: 'deezer', externalId: '1', title: 'Kiara', artist: 'Bonobo', album: 'Black Sands', durationMs: 1000, type: 'track', reason: { kind: 'similar', artist: 'Boards', title: 'Roygbiv' } },
    { source: 'library', externalId: 'lib-2', title: 'Olson', artist: 'Boards', album: '', durationMs: 1000, type: 'track' },
  ],
}

function mockSuggestions(data: SimilarTracksResult) {
  vi.mocked(usePlaylistSuggestions).mockReturnValue({ data, isFetching: false } as unknown as UseQueryResult<SimilarTracksResult>)
}

function renderSuggestions() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={qc}><MemoryRouter><PlaylistSuggestions playlistId="pl-1" /></MemoryRouter></QueryClientProvider>)
}

describe('PlaylistSuggestions', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockSuggestions(result)
  })

  it('adds a suggestion without downloading and drops it from the list', async () => {
    vi.mocked(addSyncedTrack).mockResolvedValue({} as Awaited<ReturnType<typeof addSyncedTrack>>)
    renderSuggestions()
    expect(screen.getByText('Similar to Roygbiv')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Add Kiara to playlist' }))
    await waitFor(() => expect(screen.queryByText('Kiara')).not.toBeInTheDocument())
    expect(addSyncedTrack).toHaveBeenCalledWith('pl-1', expect.objectContaining({ source: 'deezer', externalId: '1', title: 'Kiara', artist: 'Bonobo', download: false }))
    expect(screen.getByText('Olson')).toBeInTheDocument()
  })

  it('refresh asks for the next best candidates', () => {
    renderSuggestions()
    expect(usePlaylistSuggestions).toHaveBeenLastCalledWith('pl-1', 0, true)
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(usePlaylistSuggestions).toHaveBeenLastCalledWith('pl-1', 1, true)
  })

  it('is hidden when suggestions are unavailable', () => {
    mockSuggestions({ available: false, tracks: [] })
    const { container } = renderSuggestions()
    expect(container).toBeEmptyDOMElement()
  })
})

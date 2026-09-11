import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import SimilarTracks from './SimilarTracks'
import type { SimilarTracksResult } from '../lib/recommendationsApi'

const playTrackList = vi.fn()
vi.mock('../lib/playerStore', () => ({
  usePlayer: (selector: (s: unknown) => unknown) => selector({ playTrackList, current: null }),
}))
vi.mock('../lib/useTrackUpgrade', () => ({
  useTrackUpgrade: () => ({ available: false, isPending: false }),
}))
vi.mock('../lib/recommendationsApi', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/recommendationsApi')>()),
  useSimilarTracks: vi.fn(),
}))

import { useSimilarTracks } from '../lib/recommendationsApi'

function respond(data: SimilarTracksResult | undefined, isLoading = false) {
  vi.mocked(useSimilarTracks).mockReturnValue({ data, isLoading } as ReturnType<typeof useSimilarTracks>)
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/similar-tracks?artist=Daft%20Punk&title=One%20More%20Time']}>
        <Routes>
          <Route path="/similar-tracks" element={<SimilarTracks />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const RESULT: SimilarTracksResult = {
  available: true,
  tracks: [
    {
      source: 'library', externalId: 'lib-7', canonicalId: 'trk_7', title: 'Around the World', artist: 'Daft Punk',
      album: '', durationMs: 429000, type: 'track',
      match: { status: 'in_library', libraryTrackId: 'lib-7', method: 'fuzzy', confidence: 1 },
    },
    { source: 'deezer', externalId: '1', title: 'D.A.N.C.E.', artist: 'Justice', album: 'Cross', durationMs: 242000, type: 'track' },
  ],
}

beforeEach(() => {
  playTrackList.mockClear()
})

describe('Similar tracks page', () => {
  it('asks about the seed track from the URL', () => {
    respond(undefined, true)
    renderPage()
    expect(useSimilarTracks).toHaveBeenCalledWith('Daft Punk', 'One More Time')
  })

  it('plays the list from the clicked track', () => {
    respond(RESULT)
    renderPage()
    expect(screen.getByText('Around the World')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Play D.A.N.C.E.' }))
    expect(playTrackList).toHaveBeenCalledTimes(1)
    const [tracks, index] = playTrackList.mock.calls[0]
    expect(index).toBe(1)
    expect(tracks[0]).toMatchObject({ id: 'lib-7' })
    expect(tracks[1]).toMatchObject({ externalStream: { source: 'deezer', externalId: '1' } })
  })

  it('explains that Last.fm is needed when it is not configured', () => {
    respond({ available: false, tracks: [] })
    renderPage()
    expect(screen.getByText(/last\.fm/i)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Play / })).toBeNull()
  })

  it('says so when nothing similar could be played', () => {
    respond({ available: true, tracks: [] })
    renderPage()
    expect(screen.getByText(/no similar tracks/i)).toBeInTheDocument()
  })
})

import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider, type UseQueryResult } from '@tanstack/react-query'
import MixPage from './Mix'
import { saveMixAsPlaylist, useMix, type Mix } from '../lib/recommendationsApi'

vi.mock('../lib/recommendationsApi', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/recommendationsApi')>()
  return { ...actual, useMix: vi.fn(), saveMixAsPlaylist: vi.fn() }
})

const playTrackList = vi.fn()
vi.mock('../lib/playerStore', () => ({
  usePlayer: (selector: (s: { playTrackList: typeof playTrackList; current: null }) => unknown) => selector({ playTrackList, current: null }),
}))

const mockNavigate = vi.fn()
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>()
  return { ...actual, useNavigate: () => mockNavigate }
})

const mix: Mix = {
  kind: 'discoverWeekly', period: '2026-09-07', available: true, refreshing: false, updatedAt: 1_789_000_000,
  tracks: [
    { source: 'deezer', externalId: '1', title: 'Xone', artist: 'Xenon', album: '', durationMs: 1000, type: 'track', reason: { kind: 'played', artist: 'Aphex', title: 'Xtal' } },
    { source: 'library', externalId: 'lib-2', title: 'Yone', artist: 'Yarn', album: '', durationMs: 1000, type: 'track' },
  ],
}

function renderMix(path = '/mix/discoverWeekly') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes><Route path="/mix/:kind" element={<MixPage />} /></Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('Mix page', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(useMix).mockReturnValue({ data: mix, isLoading: false } as unknown as UseQueryResult<Mix>)
  })

  it('plays the Mix in order as Mix recommendations', () => {
    renderMix()
    expect(screen.getByRole('heading', { name: 'Discover Weekly' })).toBeInTheDocument()
    expect(screen.getByText('Because you played Xtal')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Play Discover Weekly' }))
    expect(playTrackList).toHaveBeenCalledWith([
      expect.objectContaining({ title: 'Xone', recommendationOrigin: 'mix' }),
      expect.objectContaining({ title: 'Yone', recommendationOrigin: 'mix' }),
    ], 0)
  })

  it('shuffles every track', () => {
    renderMix()
    fireEvent.click(screen.getByRole('button', { name: 'Shuffle' }))
    const [played] = playTrackList.mock.calls[0]
    expect(played.map((t: { title: string }) => t.title).sort()).toEqual(['Xone', 'Yone'])
  })

  it('saves the Mix as a playlist and opens it', async () => {
    vi.mocked(saveMixAsPlaylist).mockResolvedValue({ id: 'pl-9' } as Awaited<ReturnType<typeof saveMixAsPlaylist>>)
    renderMix()
    fireEvent.click(screen.getByRole('button', { name: 'Save as playlist' }))
    await waitFor(() => expect(mockNavigate).toHaveBeenCalledWith('/playlist/pl-9'))
    expect(saveMixAsPlaylist).toHaveBeenCalledWith('discoverWeekly')
  })

  it('says so when Release Radar has nothing new', () => {
    vi.mocked(useMix).mockReturnValue({ data: { ...mix, kind: 'releaseRadar', tracks: [] }, isLoading: false } as unknown as UseQueryResult<Mix>)
    renderMix('/mix/releaseRadar')
    expect(screen.getByText('No new releases this week')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Save as playlist' })).not.toBeInTheDocument()
  })
})

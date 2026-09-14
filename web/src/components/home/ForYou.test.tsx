import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { UseQueryResult } from '@tanstack/react-query'
import { ForYouShelves, MixesRow } from './ForYou'
import { useMixes, useShelves, type HomeShelves, type Mix, type RecommendedTrack } from '../../lib/recommendationsApi'

vi.mock('../../lib/recommendationsApi', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/recommendationsApi')>()
  return { ...actual, useShelves: vi.fn(), useMixes: vi.fn() }
})

const playTrackList = vi.fn()
vi.mock('../../lib/playerStore', () => ({
  usePlayer: (selector: (s: { playTrackList: typeof playTrackList }) => unknown) => selector({ playTrackList }),
}))

function track(title: string, artist: string): RecommendedTrack {
  return { source: 'deezer', externalId: title, title, artist, album: '', durationMs: 1000, type: 'track' }
}

function shelvesResult(data: HomeShelves | undefined, isLoading = false) {
  vi.mocked(useShelves).mockReturnValue({ data, isLoading } as unknown as UseQueryResult<HomeShelves>)
}

const shelves: HomeShelves = {
  refreshing: false,
  shelves: [
    { kind: 'becauseYouPlayed', seed: { artist: 'Aphex', title: 'Xtal' }, tracks: [track('Windowlicker', 'Squarepusher')], artists: [] },
    { kind: 'artistsYouMightLike', tracks: [], artists: [{ source: 'deezer', externalId: 'sq', name: 'Squarepusher', reason: { kind: 'fansAlsoLike', artist: 'Aphex' } }] },
    { kind: 'moreFromArtistsYouLove', tracks: [{ ...track('Aphex Deep', 'Aphex'), reason: { kind: 'moreFrom', artist: 'Aphex' } }], artists: [] },
  ],
}

describe('ForYouShelves', () => {
  beforeEach(() => vi.clearAllMocks())

  it('titles each shelf with its reason and plays a shelf track as a shelf recommendation', () => {
    shelvesResult(shelves)
    render(<MemoryRouter><ForYouShelves /></MemoryRouter>)

    expect(screen.getByRole('heading', { name: 'Because you played Xtal' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Artists you might like' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'More from artists you love' })).toBeInTheDocument()
    expect(screen.getByText('Fans of Aphex also like')).toBeInTheDocument()

    fireEvent.click(screen.getByText('Windowlicker'))
    expect(playTrackList).toHaveBeenCalledWith([expect.objectContaining({ title: 'Windowlicker', recommendationOrigin: 'shelf' })], 0)
  })

  it('reserves space while the first shelves are generated', () => {
    shelvesResult({ refreshing: true, shelves: [] })
    render(<MemoryRouter><ForYouShelves /></MemoryRouter>)
    expect(screen.getByTestId('for-you-loading')).toBeInTheDocument()
  })

  it('keeps showing cached shelves while they refresh', () => {
    shelvesResult({ ...shelves, refreshing: true })
    render(<MemoryRouter><ForYouShelves /></MemoryRouter>)
    expect(screen.queryByTestId('for-you-loading')).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Because you played Xtal' })).toBeInTheDocument()
  })

  it('notes offline results', () => {
    shelvesResult({ ...shelves, offline: true })
    render(<MemoryRouter><ForYouShelves /></MemoryRouter>)
    expect(screen.getByText(/Offline · last results/)).toBeInTheDocument()
  })
})

describe('MixesRow', () => {
  it('hides an empty Mix', () => {
    const mixes: Mix[] = [
      { kind: 'discoverWeekly', period: '2026-09-07', available: true, refreshing: false, tracks: [track('Xone', 'Xenon')] },
      { kind: 'releaseRadar', period: '2026-09-11', available: true, refreshing: false, tracks: [] },
    ]
    vi.mocked(useMixes).mockReturnValue({ data: { mixes } } as unknown as ReturnType<typeof useMixes>)
    render(<MemoryRouter><MixesRow /></MemoryRouter>)

    expect(screen.getByText('Discover Weekly')).toBeInTheDocument()
    expect(screen.queryByText('Release Radar')).not.toBeInTheDocument()
  })
})

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useSearchParams } from 'react-router-dom'
import { TrackActionsMenu } from './TrackActionsMenu'
import { makeTrack } from '../test/factories'
import type { UpgradableTrack } from '../lib/upgradeApi'

const mockMutate = vi.fn()
let upgradable: UpgradableTrack[] = []

vi.mock('../lib/settingsApi', () => ({
  useSettings: () => ({ data: { downloadQuality: 'high' } }),
}))
// Only the hooks are stubbed; findRefetchable is the real matching logic these
// tests are exercising, so it comes from the actual module.
vi.mock('../lib/upgradeApi', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/upgradeApi')>()),
  useUpgradable: () => ({ data: upgradable }),
  useRefetchable: () => ({ data: upgradable }),
  useUpgradeDownload: () => ({ mutate: mockMutate, isPending: false }),
}))
vi.mock('../lib/toastStore', () => ({
  useToastStore: (sel: (s: unknown) => unknown) => sel({ push: vi.fn() }),
}))
vi.mock('./AddToPlaylistMenu', () => ({
  AddToPlaylistMenu: () => <div data-testid="add-to-playlist" />,
}))
const mockSetQuality = vi.fn()
vi.mock('../lib/trackQualityApi', () => ({
  useTrackQuality: () => ({ data: { quality: 'high', overridden: false, default: 'high' } }),
  useSetTrackQuality: () => ({ mutate: mockSetQuality, isPending: false }),
}))

const mockMarkNotInterested = vi.fn()
vi.mock('../lib/notInterestedApi', () => ({
  useMarkNotInterested: () => ({ mutate: mockMarkNotInterested, isPending: false }),
}))

const track = makeTrack({ id: 't1', title: '01 - Dunanna Pit', artist: 'A', bitRate: 143 })

function open() {
  render(
    <MemoryRouter>
      <TrackActionsMenu track={track} />
    </MemoryRouter>,
  )
  fireEvent.click(screen.getByRole('button', { name: /more actions/i }))
}

beforeEach(() => {
  vi.clearAllMocks()
  upgradable = []
})

describe('TrackActionsMenu similar tracks', () => {
  it('opens similar tracks for this track', () => {
    render(
      <MemoryRouter>
        <Routes>
          <Route path="/" element={<TrackActionsMenu track={track} />} />
          <Route path="/similar-tracks" element={<SimilarTracksProbe />} />
        </Routes>
      </MemoryRouter>,
    )
    fireEvent.click(screen.getByRole('button', { name: /more actions/i }))
    fireEvent.click(screen.getByRole('menuitem', { name: /similar tracks/i }))
    expect(screen.getByTestId('similar-probe')).toHaveTextContent('artist=A title=01 - Dunanna Pit')
  })

  it('is offered for a track that streams from a search source', () => {
    render(
      <MemoryRouter>
        <TrackActionsMenu track={makeTrack({ id: 'ext:deezer:1', title: 'D.A.N.C.E.', artist: 'Justice', externalStream: { source: 'deezer', externalId: '1' } })} />
      </MemoryRouter>,
    )
    fireEvent.click(screen.getByRole('button', { name: /more actions/i }))
    expect(screen.getByRole('menuitem', { name: /similar tracks/i })).toBeInTheDocument()
  })
})

describe('TrackActionsMenu not interested', () => {
  it('marks a library track by its library id', () => {
    open()
    fireEvent.click(screen.getByRole('menuitem', { name: /not interested/i }))
    expect(mockMarkNotInterested).toHaveBeenCalledWith({
      kind: 'track', source: 'library', trackId: 't1',
      title: '01 - Dunanna Pit', artist: 'A', album: 'Test Album', durationMs: 180000,
    })
  })

  it('marks a search-source track by its source id', () => {
    render(
      <MemoryRouter>
        <TrackActionsMenu track={makeTrack({ id: 'ext:deezer:1', title: 'D.A.N.C.E.', artist: 'Justice', externalStream: { source: 'deezer', externalId: '1' } })} />
      </MemoryRouter>,
    )
    fireEvent.click(screen.getByRole('button', { name: /more actions/i }))
    fireEvent.click(screen.getByRole('menuitem', { name: /not interested/i }))
    expect(mockMarkNotInterested).toHaveBeenCalledWith({
      kind: 'track', source: 'deezer', externalId: '1', title: 'D.A.N.C.E.', artist: 'Justice',
    })
  })
})

function SimilarTracksProbe() {
  const [params] = useSearchParams()
  return <div data-testid="similar-probe">{`artist=${params.get('artist')} title=${params.get('title')}`}</div>
}

describe('TrackActionsMenu', () => {
  // A low bitrate is not evidence a better file exists — the sources serve
  // ~130-160 kbps. What matters is whether Reverb still knows a source to
  // re-fetch from, which only the server list can say.
  it('omits the quality action for a track the server cannot re-fetch', () => {
    open()
    expect(screen.queryByRole('menuitem', { name: /audio quality/i })).toBeNull()
  })

  it('re-downloads at the chosen tier using the original source', async () => {
    upgradable = [
      {
        jobId: 'j1',
        source: 'spotify',
        externalId: 'sp-1',
        artist: 'A',
        title: '01 - Dunanna Pit',
        album: 'OST',
        quality: 'low',
        libraryTrackId: 't1',
      },
    ]
    open()
    fireEvent.click(screen.getByRole('menuitem', { name: /audio quality/i }))
    fireEvent.click(await screen.findByRole('radio', { name: /best/i }))
    fireEvent.click(screen.getByRole('button', { name: /re-download at best/i }))
    await waitFor(() => expect(mockMutate).toHaveBeenCalled())
    expect(mockMutate.mock.calls[0][0]).toMatchObject({
      source: 'spotify',
      externalId: 'sp-1',
      libraryTrackId: 't1',
      quality: 'best',
      currentQuality: 'low',
    })
  })

  // A downgrade is a first-class choice, not something the UI should block.
  it('allows picking a tier below the current file', async () => {
    upgradable = [
      { jobId: 'j1', source: 'spotify', externalId: 'sp-1', artist: 'A', title: '01 - Dunanna Pit', album: 'OST', quality: 'best', libraryTrackId: 't1' },
    ]
    open()
    fireEvent.click(screen.getByRole('menuitem', { name: /audio quality/i }))
    fireEvent.click(await screen.findByRole('radio', { name: /low/i }))
    fireEvent.click(screen.getByRole('button', { name: /re-download at low/i }))
    await waitFor(() => expect(mockMutate).toHaveBeenCalled())
    expect(mockMutate.mock.calls[0][0]).toMatchObject({ quality: 'low', currentQuality: 'best' })
  })

  // Saving records a standing preference without spending a download.
  it('saves a per-track quality override without re-downloading', async () => {
    upgradable = [
      { jobId: 'j1', source: 'spotify', externalId: 'sp-1', artist: 'A', title: '01 - Dunanna Pit', album: 'OST', quality: 'low', libraryTrackId: 't1' },
    ]
    open()
    fireEvent.click(screen.getByRole('menuitem', { name: /audio quality/i }))
    fireEvent.click(await screen.findByRole('radio', { name: /medium/i }))
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))
    await waitFor(() => expect(mockSetQuality).toHaveBeenCalled())
    expect(mockSetQuality.mock.calls[0][0]).toMatchObject({ trackId: 't1', quality: 'medium' })
    expect(mockMutate).not.toHaveBeenCalled()
  })
})

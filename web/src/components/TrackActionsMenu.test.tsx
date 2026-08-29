import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { TrackActionsMenu } from './TrackActionsMenu'
import { makeTrack } from '../test/factories'
import type { UpgradableTrack } from '../lib/upgradeApi'

const mockMutate = vi.fn()
let upgradable: UpgradableTrack[] = []

vi.mock('../lib/settingsApi', () => ({
  useSettings: () => ({ data: { downloadQuality: 'high' } }),
}))
vi.mock('../lib/upgradeApi', () => ({
  useUpgradable: () => ({ data: upgradable }),
  useUpgradeDownload: () => ({ mutate: mockMutate, isPending: false }),
}))
vi.mock('../lib/toastStore', () => ({
  useToastStore: (sel: (s: unknown) => unknown) => sel({ push: vi.fn() }),
}))
vi.mock('./AddToPlaylistMenu', () => ({
  AddToPlaylistMenu: () => <div data-testid="add-to-playlist" />,
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

describe('TrackActionsMenu', () => {
  // A low bitrate is not evidence a better file exists — the sources serve
  // ~130-160 kbps. Offering an upgrade there is the button that "does nothing".
  it('omits the upgrade action for a track the server does not list as upgradable', () => {
    open()
    expect(screen.queryByRole('menuitem', { name: /upgrade/i })).toBeNull()
  })

  it('upgrades using the source the track was originally downloaded from', async () => {
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
    fireEvent.click(screen.getByRole('menuitem', { name: /upgrade to high/i }))
    await waitFor(() => expect(mockMutate).toHaveBeenCalled())
    expect(mockMutate.mock.calls[0][0]).toMatchObject({
      source: 'spotify',
      externalId: 'sp-1',
      libraryTrackId: 't1',
      quality: 'high',
      currentQuality: 'low',
    })
  })
})

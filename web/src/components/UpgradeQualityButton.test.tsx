import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { UpgradeQualityButton } from './UpgradeQualityButton'
import { makeTrack } from '../test/factories'

const mockMutate = vi.fn()
const mockPush = vi.fn()

vi.mock('../lib/settingsApi', () => ({
  useSettings: () => ({ data: { downloadQuality: 'high' } }),
}))
vi.mock('../lib/upgradeApi', () => ({
  useUpgradeDownload: () => ({ mutate: mockMutate, isPending: false }),
}))
vi.mock('../lib/toastStore', () => ({
  useToastStore: (sel: (s: unknown) => unknown) => sel({ push: mockPush }),
}))

beforeEach(() => vi.clearAllMocks())

describe('UpgradeQualityButton', () => {
  it('offers an upgrade for an owned track below the target tier', () => {
    render(<UpgradeQualityButton track={makeTrack({ id: 't1', bitRate: 128 })} />)
    expect(screen.getByLabelText(/upgrade quality to high/i)).toBeInTheDocument()
  })

  it('stays hidden when the track already meets the tier', () => {
    render(<UpgradeQualityButton track={makeTrack({ id: 't1', bitRate: 320 })} />)
    expect(screen.queryByLabelText(/upgrade quality/i)).toBeNull()
  })

  it('stays hidden for an unowned track, which has no file to replace', () => {
    render(<UpgradeQualityButton track={makeTrack({ id: '', bitRate: 128 })} />)
    expect(screen.queryByLabelText(/upgrade quality/i)).toBeNull()
  })

  it('stays hidden when the bitrate is unknown', () => {
    render(<UpgradeQualityButton track={makeTrack({ id: 't1', bitRate: 0 })} />)
    expect(screen.queryByLabelText(/upgrade quality/i)).toBeNull()
  })

  it('sends the current tier so the server can reject a non-upgrade', async () => {
    render(<UpgradeQualityButton track={makeTrack({ id: 't1', bitRate: 128, title: 'Song', artist: 'A' })} />)
    fireEvent.click(screen.getByLabelText(/upgrade quality to high/i))
    await waitFor(() => expect(mockMutate).toHaveBeenCalled())
    expect(mockMutate.mock.calls[0][0]).toMatchObject({
      title: 'Song',
      quality: 'high',
      currentQuality: 'low',
    })
  })
})

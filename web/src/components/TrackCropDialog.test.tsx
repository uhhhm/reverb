import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TrackCropDialog } from './TrackCropDialog'
import { makeTrack } from '../test/factories'

const mockSave = vi.fn()
let savedCrop: { startMs: number; endMs: number } = { startMs: 0, endMs: 0 }

vi.mock('../lib/cropApi', () => ({
  useTrackCrop: () => ({ data: savedCrop }),
  useSaveTrackCrop: () => ({ mutate: mockSave, isPending: false }),
}))
vi.mock('../lib/peaksApi', () => ({ usePeaks: () => ({ data: null }) }))
vi.mock('../lib/toastStore', () => ({
  useToastStore: (sel: (s: unknown) => unknown) => sel({ push: vi.fn() }),
}))

const track = makeTrack({ id: 't1', title: 'Long One', durationMs: 200000 })

function open() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <TrackCropDialog track={track} onClose={() => {}} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  savedCrop = { startMs: 0, endMs: 0 }
})

describe('TrackCropDialog', () => {
  it('starts from the stored crop', async () => {
    savedCrop = { startMs: 30000, endMs: 90000 }
    open()
    await waitFor(() =>
      expect(screen.getByRole('slider', { name: /crop start/i })).toHaveAttribute('aria-valuenow', '30000'),
    )
    expect(screen.getByRole('slider', { name: /crop end/i })).toHaveAttribute('aria-valuenow', '90000')
  })

  // An uncropped track opens with the handles at the ends of the file.
  it('opens spanning the whole track when there is no crop', async () => {
    open()
    await waitFor(() =>
      expect(screen.getByRole('slider', { name: /crop end/i })).toHaveAttribute('aria-valuenow', '200000'),
    )
  })

  it('nudges a handle with the arrow keys and saves the window', async () => {
    open()
    const start = await screen.findByRole('slider', { name: /crop start/i })
    fireEvent.keyDown(start, { key: 'ArrowRight' })
    expect(start).toHaveAttribute('aria-valuenow', '500')
    fireEvent.click(screen.getByRole('button', { name: /save crop/i }))
    await waitFor(() => expect(mockSave).toHaveBeenCalled())
    // endMs 0 means "to the end of the file", which is what a full-length end is.
    expect(mockSave.mock.calls[0][0]).toMatchObject({
      trackId: 't1',
      points: { startMs: 500, endMs: 0 },
    })
  })

  // Saving a full-length window is not a crop — it clears the stored one.
  it('clears the crop when the window covers the whole track', async () => {
    savedCrop = { startMs: 30000, endMs: 90000 }
    open()
    await screen.findByRole('slider', { name: /crop start/i })
    fireEvent.click(screen.getByRole('button', { name: /uncrop/i }))
    await waitFor(() => expect(mockSave).toHaveBeenCalledWith({ trackId: 't1', points: null }, expect.anything()))
  })

  // Nothing is destroyed, so Uncrop is only offered when a crop actually exists.
  it('does not offer Uncrop on a track that is not cropped', async () => {
    open()
    await screen.findByRole('slider', { name: /crop start/i })
    expect(screen.queryByRole('button', { name: /uncrop/i })).toBeNull()
  })
})

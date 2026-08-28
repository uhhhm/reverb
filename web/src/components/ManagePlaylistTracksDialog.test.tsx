import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ManagePlaylistTracksDialog } from './ManagePlaylistTracksDialog'
import { makeTrack } from '../test/factories'
import type { AlbumDetailTrack } from '../lib/types'

const songs = [
  makeTrack({ id: 't1', title: 'Already In', artist: 'A' }),
  makeTrack({ id: 't2', title: 'Not In Yet', artist: 'B' }),
  makeTrack({ id: 't3', title: 'Also Out', artist: 'C' }),
]

vi.mock('../lib/libraryApi', () => ({
  useSongs: () => ({ data: songs, isLoading: false }),
  coverUrl: () => '',
  trackCoverUrl: () => '',
}))

const mockAddSyncedTrack = vi.fn().mockResolvedValue({})
const mockRemoveSyncedTrack = vi.fn().mockResolvedValue({})

vi.mock('../lib/syncedPlaylistApi', () => ({
  addSyncedTrack: (...args: unknown[]) => mockAddSyncedTrack(...args),
  removeSyncedTrack: (...args: unknown[]) => mockRemoveSyncedTrack(...args),
}))

const playlistTracks: AlbumDetailTrack[] = [
  {
    state: 'full',
    key: { source: 'library', externalId: 't1' },
    title: 'Already In',
    artist: 'A',
    trackNumber: 1,
    durationMs: 1000,
  },
  {
    // An imported entry — not selectable here, and untouched on save.
    state: 'none',
    key: { source: 'spotify', externalId: 'sp1' },
    title: 'Imported',
    artist: 'D',
    trackNumber: 2,
    durationMs: 1000,
  },
]

function renderDialog(onClose = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <ManagePlaylistTracksDialog playlistId="pl1" tracks={playlistTracks} onClose={onClose} />
    </QueryClientProvider>,
  )
  return onClose
}

describe('ManagePlaylistTracksDialog', () => {
  beforeEach(() => {
    mockAddSyncedTrack.mockClear()
    mockRemoveSyncedTrack.mockClear()
  })

  it('seeds the selection from the playlist library entries', () => {
    renderDialog()
    expect(screen.getByRole('checkbox', { name: /Already In/ })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: /Not In Yet/ })).not.toBeChecked()
  })

  it('disables Save until something changes', () => {
    renderDialog()
    expect(screen.getByRole('button', { name: 'Save tracks' })).toBeDisabled()
    fireEvent.click(screen.getByRole('checkbox', { name: /Not In Yet/ }))
    expect(screen.getByRole('button', { name: 'Save tracks' })).toBeEnabled()
  })

  it('filters the list by the search box', () => {
    renderDialog()
    fireEvent.change(screen.getByLabelText('Search your library'), {
      target: { value: 'not in' },
    })
    expect(screen.getByRole('checkbox', { name: /Not In Yet/ })).toBeInTheDocument()
    expect(screen.queryByRole('checkbox', { name: /Already In/ })).not.toBeInTheDocument()
  })

  it('applies only the difference on save and leaves imported entries alone', async () => {
    const onClose = renderDialog()
    fireEvent.click(screen.getByRole('checkbox', { name: /Not In Yet/ })) // add t2
    fireEvent.click(screen.getByRole('checkbox', { name: /Already In/ })) // remove t1
    fireEvent.click(screen.getByRole('button', { name: 'Save tracks' }))

    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(mockRemoveSyncedTrack).toHaveBeenCalledTimes(1)
    expect(mockRemoveSyncedTrack).toHaveBeenCalledWith('pl1', 'library', 't1')
    expect(mockAddSyncedTrack).toHaveBeenCalledTimes(1)
    expect(mockAddSyncedTrack).toHaveBeenCalledWith(
      'pl1',
      expect.objectContaining({ source: 'library', externalId: 't2', title: 'Not In Yet' }),
    )
  })

  it('reports a failed save and stays open', async () => {
    mockAddSyncedTrack.mockRejectedValueOnce(new Error('boom'))
    const onClose = renderDialog()
    fireEvent.click(screen.getByRole('checkbox', { name: /Not In Yet/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Save tracks' }))

    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(onClose).not.toHaveBeenCalled()
  })
})

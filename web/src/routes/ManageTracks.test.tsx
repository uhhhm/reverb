import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import ManageTracks from './ManageTracks'
import type { Track } from '../lib/types'

function song(id: string, title: string, over: Partial<Track> = {}): Track {
  return {
    id,
    title,
    albumId: 'al1',
    album: 'First Album',
    artistId: 'ar1',
    artist: 'Alpha',
    coverArtId: '',
    trackNumber: 1,
    discNumber: 1,
    durationMs: 200000,
    bitRate: 200,
    suffix: 'mp3',
    contentType: 'audio/mpeg',
    ...over,
  }
}

const songs: Track[] = [
  {
    id: 't1',
    title: 'Downloaded Song',
    albumId: 'al1',
    album: 'First Album',
    artistId: 'ar1',
    artist: 'Alpha',
    coverArtId: 'c1',
    trackNumber: 1,
    discNumber: 1,
    durationMs: 200000,
    bitRate: 143,
    suffix: 'mp3',
    contentType: 'audio/mpeg',
  },
  {
    id: 't2',
    title: 'Ripped Song',
    albumId: 'al2',
    album: 'Second Album',
    artistId: 'ar2',
    artist: 'Beta',
    coverArtId: '',
    trackNumber: 1,
    discNumber: 1,
    durationMs: 200000,
    bitRate: 320,
    suffix: 'mp3',
    contentType: 'audio/mpeg',
  },
  song('t3', 'Third'),
  song('t4', 'Fourth'),
  song('t5', 'Fifth'),
]

vi.mock('../lib/libraryApi', () => ({
  useSongs: () => ({ data: songs, isLoading: false }),
  useAlbums: () => ({ data: [{ id: 'al1', name: 'First Album', artist: 'Alpha', coverArtId: '' }], isLoading: false }),
  useArtists: () => ({ data: [{ id: 'ar1', name: 'Alpha', coverArtId: '' }], isLoading: false }),
  coverUrl: (id: string) => `/api/v1/cover/${id}`,
}))

vi.mock('../lib/trackQualityApi', () => ({
  useTrackQualityIndex: () => ({ data: { default: 'high', overrides: { t2: 'low' } } }),
}))

vi.mock('../lib/upgradeApi', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/upgradeApi')>()),
  // Only t1 was downloaded by Reverb, at "medium".
  useRefetchable: () => ({
    data: [
      {
        jobId: 'j1',
        source: 'spotify',
        externalId: 'sp1',
        artist: 'Alpha',
        title: 'Downloaded Song',
        album: 'First Album',
        quality: 'medium',
        libraryTrackId: 't1',
      },
    ],
  }),
}))

vi.mock('../lib/syncedPlaylistApi', () => ({
  useSyncedPlaylists: () => ({ data: [{ id: 'p1', name: 'Road trip' }] }),
  // The playlist holds t1 and t4, plus one entry the library does not have.
  useSyncedPlaylist: (id: string) => ({
    data: id
      ? {
          tracks: [
            { libraryTrack: songs[0] },
            { libraryTrack: songs.find((s) => s.id === 't4') },
            { title: 'Not owned' },
          ],
        }
      : undefined,
  }),
}))

vi.mock('../components/BatchRenameDialog', () => ({
  BatchRenameDialog: ({ subject }: { subject: unknown }) =>
    subject ? <div data-testid="rename-dialog">{JSON.stringify(subject)}</div> : null,
}))
vi.mock('../components/BatchQualityDialog', () => ({
  BatchQualityDialog: ({ subjects }: { subjects: unknown[] | null }) =>
    subjects ? <div data-testid="quality-dialog">{subjects.length}</div> : null,
}))
vi.mock('../components/CoverUploadDialog', () => ({
  CoverUploadDialog: ({ targets }: { targets: unknown[] }) => (
    <div data-testid="cover-dialog">{JSON.stringify(targets)}</div>
  ),
}))

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <ManageTracks />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function rowFor(title: string): HTMLElement {
  return screen.getByText(title).closest('li') as HTMLElement
}

describe('ManageTracks', () => {
  beforeEach(() => vi.clearAllMocks())

  it('shows the real bitrate beside the tier it was fetched at', () => {
    renderPage()
    // The tier comes from download history, not from the bitrate: 143 kbps
    // would read as "medium" either way, but only history knows it was asked for.
    expect(within(rowFor('Downloaded Song')).getByText('143 kbps · Medium')).toBeInTheDocument()
  })

  it('falls back to the tier the bitrate implies for a track Reverb did not download', () => {
    renderPage()
    const row = rowFor('Ripped Song')
    expect(within(row).getByText('320 kbps · High')).toBeInTheDocument()
    expect(within(row).getByText(/not re-fetchable/)).toBeInTheDocument()
  })

  it('shows the standing quality, marking the ones following the global default', () => {
    renderPage()
    // t2 carries an override.
    expect(within(rowFor('Ripped Song')).getByText(/Next fetch: Low/)).toBeInTheDocument()
    // t1 has none, so it follows the default and says so.
    expect(within(rowFor('Downloaded Song')).getByText(/Next fetch: High \(default\)/)).toBeInTheDocument()
  })

  it('offers quality only on the tracks tab', () => {
    renderPage()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Downloaded Song' }))
    expect(screen.getByRole('button', { name: 'Quality…' })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Albums' }))
    fireEvent.click(screen.getByRole('button', { name: 'Select First Album' }))
    expect(screen.queryByRole('button', { name: 'Quality…' })).not.toBeInTheDocument()
    // Albums still carry artwork.
    expect(screen.getByRole('button', { name: 'Set cover…' })).toBeInTheDocument()
  })

  it('offers no cover action for artists, which carry no artwork of their own', () => {
    renderPage()
    fireEvent.click(screen.getByRole('button', { name: 'Artists' }))
    fireEvent.click(screen.getByRole('button', { name: 'Select Alpha' }))
    expect(screen.queryByRole('button', { name: 'Set cover…' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Rename…' })).toBeInTheDocument()
  })

  it('drops the selection when the tab changes, since ids do not carry across', () => {
    renderPage()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Downloaded Song' }))
    expect(screen.getByText('1 selected')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Albums' }))
    expect(screen.queryByText('1 selected')).not.toBeInTheDocument()
  })

  it('filters the list without losing a selection made before the filter', () => {
    renderPage()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Downloaded Song' }))

    fireEvent.change(screen.getByLabelText('Filter tracks'), { target: { value: 'ripped' } })
    expect(screen.queryByText('Downloaded Song')).not.toBeInTheDocument()
    expect(screen.getByText('Ripped Song')).toBeInTheDocument()
    // Selection is by id, so it is still counted while filtered out of view.
    expect(screen.getByText('1 selected')).toBeInTheDocument()
  })

  it('passes the selected tracks with their re-fetch entry to the quality dialog', () => {
    renderPage()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Downloaded Song' }))
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Ripped Song' }))
    fireEvent.click(screen.getByRole('button', { name: 'Quality…' }))
    expect(screen.getByTestId('quality-dialog')).toHaveTextContent('2')
  })

  describe('selection gestures', () => {
    // A real mouse click carries detail >= 1; testing-library's default of 0 is
    // what a keyboard-generated click looks like, and the two take different
    // paths through the row handlers.
    const mouseClick = (el: HTMLElement, init: MouseEventInit = {}) =>
      fireEvent.click(el, { detail: 1, ...init })

    function press(title: string) {
      fireEvent.pointerDown(rowFor(title), { button: 0 })
    }

    it('shift-clicking takes everything between it and the last row picked', () => {
      renderPage()
      press('Ripped Song')
      mouseClick(rowFor('Fourth'), { shiftKey: true })

      expect(screen.getByText('3 selected')).toBeInTheDocument()
      for (const t of ['Ripped Song', 'Third', 'Fourth']) {
        expect(screen.getByRole('checkbox', { name: `Select ${t}` })).toBeChecked()
      }
      expect(screen.getByRole('checkbox', { name: 'Select Downloaded Song' })).not.toBeChecked()
    })

    it('shift-clicking uses the order on screen, not the underlying list', () => {
      renderPage()
      // "Fourth" and "Fifth" are adjacent once the list is filtered; "Third" is
      // between them in the unfiltered one and must not be caught.
      fireEvent.change(screen.getByLabelText('Filter tracks'), { target: { value: 'f' } })
      press('Fourth')
      mouseClick(rowFor('Fifth'), { shiftKey: true })
      expect(screen.getByText('2 selected')).toBeInTheDocument()
    })

    it('pressing and sweeping selects the rows crossed', () => {
      renderPage()
      press('Downloaded Song')
      fireEvent.pointerEnter(rowFor('Ripped Song'))
      fireEvent.pointerEnter(rowFor('Third'))
      expect(screen.getByText('3 selected')).toBeInTheDocument()

      // Sweeping back releases what it passed, because each move replays from
      // the selection as it was when the press began.
      fireEvent.pointerEnter(rowFor('Ripped Song'))
      expect(screen.getByText('2 selected')).toBeInTheDocument()
    })

    it('a sweep that starts on a selected row removes instead of adds', () => {
      renderPage()
      // The edit bar only appears once something is selected, and "Select all"
      // lives on it.
      fireEvent.click(screen.getByRole('checkbox', { name: 'Select Downloaded Song' }))
      fireEvent.click(screen.getByRole('button', { name: 'Select all' }))
      expect(screen.getByText('5 selected')).toBeInTheDocument()

      press('Ripped Song')
      fireEvent.pointerEnter(rowFor('Third'))
      expect(screen.getByText('3 selected')).toBeInTheDocument()
      expect(screen.getByRole('checkbox', { name: 'Select Ripped Song' })).not.toBeChecked()
    })

    it('stops sweeping once the button is released', () => {
      renderPage()
      press('Downloaded Song')
      fireEvent.pointerUp(window)
      fireEvent.pointerEnter(rowFor('Third'))
      expect(screen.getByText('1 selected')).toBeInTheDocument()
    })

    it('does not double-toggle: the pointer selects and the click is swallowed', () => {
      renderPage()
      const row = rowFor('Downloaded Song')
      fireEvent.pointerDown(row, { button: 0 })
      mouseClick(row)
      expect(screen.getByRole('checkbox', { name: 'Select Downloaded Song' })).toBeChecked()
      expect(screen.getByText('1 selected')).toBeInTheDocument()
    })

    it('leaves the keyboard path on the checkbox working', () => {
      renderPage()
      // No pointerdown, and detail 0 — what Space on a focused checkbox produces.
      fireEvent.click(screen.getByRole('checkbox', { name: 'Select Third' }))
      expect(screen.getByRole('checkbox', { name: 'Select Third' })).toBeChecked()
    })

    it('ignores a press with a non-primary button', () => {
      renderPage()
      fireEvent.pointerDown(rowFor('Downloaded Song'), { button: 2 })
      expect(screen.queryByText('1 selected')).not.toBeInTheDocument()
    })

    it('sweeps across album cards too', () => {
      renderPage()
      fireEvent.click(screen.getByRole('button', { name: 'Albums' }))
      fireEvent.pointerDown(screen.getByRole('button', { name: 'Select First Album' }), {
        button: 0,
      })
      expect(screen.getByText('1 selected')).toBeInTheDocument()
    })
  })

  describe('playlist filter', () => {
    it('narrows the track list to the playlist, ignoring entries not in the library', () => {
      renderPage()
      fireEvent.change(screen.getByLabelText('Playlist'), { target: { value: 'p1' } })

      expect(screen.getByText('Downloaded Song')).toBeInTheDocument()
      expect(screen.getByText('Fourth')).toBeInTheDocument()
      expect(screen.queryByText('Ripped Song')).not.toBeInTheDocument()
      // The playlist's third entry has no library track, so there is nothing to
      // manage and no row for it.
      expect(screen.queryByText('Not owned')).not.toBeInTheDocument()
    })

    it('narrows albums and artists to the ones the playlist covers', () => {
      renderPage()
      fireEvent.change(screen.getByLabelText('Playlist'), { target: { value: 'p1' } })

      fireEvent.click(screen.getByRole('button', { name: 'Albums' }))
      // Both playlist tracks sit on al1.
      expect(screen.getByText('First Album')).toBeInTheDocument()
      fireEvent.click(screen.getByRole('button', { name: 'Artists' }))
      expect(screen.getByText('Alpha')).toBeInTheDocument()
    })

    it('combines with the text filter', () => {
      renderPage()
      fireEvent.change(screen.getByLabelText('Playlist'), { target: { value: 'p1' } })
      fireEvent.change(screen.getByLabelText('Filter tracks'), { target: { value: 'fourth' } })
      expect(screen.getByText('Fourth')).toBeInTheDocument()
      expect(screen.queryByText('Downloaded Song')).not.toBeInTheDocument()
    })

    it('drops the selection when the playlist changes, since the rows change with it', () => {
      renderPage()
      fireEvent.click(screen.getByRole('checkbox', { name: 'Select Ripped Song' }))
      expect(screen.getByText('1 selected')).toBeInTheDocument()

      fireEvent.change(screen.getByLabelText('Playlist'), { target: { value: 'p1' } })
      expect(screen.queryByText('1 selected')).not.toBeInTheDocument()
    })

    it('goes back to the whole library when the filter is cleared', () => {
      renderPage()
      fireEvent.change(screen.getByLabelText('Playlist'), { target: { value: 'p1' } })
      expect(screen.queryByText('Ripped Song')).not.toBeInTheDocument()

      fireEvent.change(screen.getByLabelText('Playlist'), { target: { value: '' } })
      expect(screen.getByText('Ripped Song')).toBeInTheDocument()
    })
  })
})

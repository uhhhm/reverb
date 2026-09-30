import { fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { RenameEntityDialog } from '../components/RenameEntityDialog'
import { RenameTrackDialog } from '../components/RenameTrackDialog'
import { BatchRenameDialog } from '../components/BatchRenameDialog'
import { useSongs } from './libraryApi'
import { useArtistDetail, useAlbumDetail } from './coverageApi'
import { useSyncedPlaylist } from './syncedPlaylistApi'
import { makeTrack } from '../test/factories'

const track = makeTrack({ id: 'song', title: 'Before', artist: 'Artist', album: 'Before' })
function useViews() {
  return { songs: useSongs(), artist: useArtistDetail('library', 'artist'),
    album: useAlbumDetail('library', 'album'), playlist: useSyncedPlaylist('playlist') }
}
afterEach(() => vi.unstubAllGlobals())

describe('library rename cache lifecycle', () => {
  it.each(['album', 'artist', 'track', 'batch', 'partial batch', 'failed write'] as const)(
    '%s refreshes accepted changes across active and inactive views', async (action) => {
      let name = 'Before'
      vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) => {
        if (init.method !== 'GET') {
          if (action === 'failed write') return new Response(JSON.stringify({ error: 'rejected write' }), { status: 500 })
          name = 'After'
          return new Response(JSON.stringify(action === 'partial batch'
            ? { applied: 1, errors: { rejected: 'rejected item' } } : { applied: 1 }), { status: 200 })
        }
        const path = _url.replace('/api/v1', '')
        const renamed = { ...track, title: name }
        const row = { state: 'owned', title: name, libraryTrack: renamed }
        const body = path === '/library/songs' ? [renamed]
          : path.startsWith('/artist/') ? { name, albums: [] }
          : { name, tracks: [row] }
        return new Response(JSON.stringify(body), { status: 200 })
      }))
      const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
      const wrapper = ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
      const warmed = renderHook(useViews, { wrapper })
      await waitFor(() => expect(Object.values(warmed.result.current).every((q) => q.isSuccess)).toBe(true))
      warmed.unmount()
      // The list stays active; detail and playlist readers are cached but inactive.
      const list = renderHook(() => useSongs(), { wrapper })
      const close = vi.fn()
      const batch = action === 'batch' || action === 'partial batch'
      render(batch ? <BatchRenameDialog subject={{ kind: 'tracks', items: [track, { ...track, id: 'rejected' }] }} onClose={close} />
        : action === 'track' ? <RenameTrackDialog track={track} onClose={close} />
        : <RenameEntityDialog kind={action === 'artist' ? 'artist' : 'album'} id="entity" currentName="Before" onClose={close} />, { wrapper })
      if (batch) {
        fireEvent.change(screen.getByLabelText('Find'), { target: { value: 'Before' } })
        fireEvent.change(screen.getByLabelText('Replace with'), { target: { value: 'After' } })
      } else fireEvent.change(screen.getByLabelText(action === 'track' ? 'Title' : 'Name'), { target: { value: 'After' } })
      fireEvent.click(screen.getByRole('button', { name: batch ? 'Apply 2 changes' : action === 'track' ? 'Save name' : 'Save' }))
      if (action === 'failed write' || action === 'partial batch') {
        await screen.findByRole('alert')
        expect(close).not.toHaveBeenCalled()
      } else await waitFor(() => expect(close).toHaveBeenCalled())
      const reopened = renderHook(useViews, { wrapper })
      const expected = action === 'failed write' ? 'Before' : 'After'
      await waitFor(() => {
        expect(list.result.current.data?.[0].title).toBe(expected)
        expect(reopened.result.current.artist.data?.name).toBe(expected)
        expect(reopened.result.current.album.data?.tracks[0].title).toBe(expected)
        expect(reopened.result.current.playlist.data?.tracks[0].title).toBe(expected)
      })
      reopened.unmount()
      list.unmount()
      client.clear()
    })
})

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { MiniPlayer } from './MiniPlayer'
import { engine } from '../lib/playerStore'
import { queueState } from '../test/fakeQueue'
import { useUI } from '../lib/uiStore'
import type { Track } from '../lib/types'

/** Something playing, as the core would answer a new list; the queue transport is not under test here. */
function playNow(tracks: Track[], index: number) {
  engine.apply(queueState(tracks, index), { autoplay: true })
}

vi.mock('../lib/useAlbumPalette', () => ({ useAlbumPalette: vi.fn(() => null) }))

function track(id: string): Track {
  return {
    id, title: 'Song ' + id, albumId: 'al', album: 'Album', artistId: 'ar', artist: 'Artist',
    coverArtId: 'co', trackNumber: 1, discNumber: 1, durationMs: 200000, bitRate: 320,
    suffix: 'mp3', contentType: 'audio/mpeg',
  }
}

describe('MiniPlayer', () => {
  beforeEach(() => {
    useUI.setState({ nowPlayingOpen: false })
  })

  it('renders null when nothing is playing', () => {
    act(() => { playNow([], 0) })
    const { container } = render(<MiniPlayer />)
    expect(container.firstChild).toBeNull()
  })

  it('shows the current track and expands to the fullscreen overlay on tap', () => {
    act(() => { playNow([track('1')], 0) })
    render(<MiniPlayer />)
    expect(screen.getByText('Song 1')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('mini-player-expand'))
    expect(useUI.getState().nowPlayingOpen).toBe(true)
  })

  it('the play/pause button calls toggle and does NOT expand', () => {
    act(() => { playNow([track('1')], 0) })
    const spy = vi.spyOn(engine, 'toggle')
    render(<MiniPlayer />)
    fireEvent.click(screen.getByRole('button', { name: /^(play|pause)$/i }))
    expect(spy).toHaveBeenCalledTimes(1)
    expect(useUI.getState().nowPlayingOpen).toBe(false)
    spy.mockRestore()
  })
})

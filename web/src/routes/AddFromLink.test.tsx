import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import AddFromLink from './AddFromLink'

const mockResolveLink = vi.fn()
const mockAddFromLink = vi.fn()
const mockAddFromLinksBatch = vi.fn()
const mockUseSyncedPlaylists = vi.fn()
const mockPush = vi.fn()
const mockNavigate = vi.fn()

vi.mock('../lib/linkApi', () => ({
  resolveLink: (...args: unknown[]) => mockResolveLink(...args),
  addFromLink: (...args: unknown[]) => mockAddFromLink(...args),
  addFromLinksBatch: (...args: unknown[]) => mockAddFromLinksBatch(...args),
}))

vi.mock('../lib/syncedPlaylistApi', () => ({
  useSyncedPlaylists: (...args: unknown[]) => mockUseSyncedPlaylists(...args),
}))

vi.mock('../lib/toastStore', () => ({
  useToastStore: (sel: (s: { push: typeof mockPush }) => unknown) => sel({ push: mockPush }),
}))

vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <AddFromLink />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('AddFromLink route', () => {
  beforeEach(() => {
    mockUseSyncedPlaylists.mockReturnValue({
      data: [
        { id: 'pl1', name: 'My Playlist' },
        { id: 'pl2', name: 'Second' },
      ],
      isLoading: false,
    })
    mockResolveLink.mockResolvedValue({
      kind: 'track',
      source: 'spotify',
      externalId: 'sp123',
      title: 'Spotify track sp123',
      artist: 'Test Artist',
      album: 'Test Album',
      coverUrl: 'https://example.com/cover.jpg',
      url: 'https://open.spotify.com/track/sp123',
    })
    mockAddFromLink.mockResolvedValue({
      resolve: {
        kind: 'track',
        source: 'spotify',
        externalId: 'sp123',
        title: 'Spotify track sp123',
        artist: 'Test Artist',
        album: 'Test Album',
        url: 'https://open.spotify.com/track/sp123',
      },
      catalogId: 'trk_link_sp123',
      job: { id: 'j1' },
    })
    mockAddFromLinksBatch.mockResolvedValue({
      results: [{ url: 'https://open.spotify.com/track/sp123', resolve: { kind: 'track', source: 'spotify', externalId: 'sp123', title: 'Spotify track sp123', artist: 'Test Artist', album: 'Test Album', url: 'https://open.spotify.com/track/sp123' }, catalogId: 'trk_link_sp123', job: { id: 'j1' } }],
    })
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  it('renders heading Add from link', () => {
    wrap()
    expect(screen.getByRole('heading', { level: 1, name: /add from link/i })).toBeInTheDocument()
  })

  it('has a multi-line URL input', () => {
    wrap()
    expect(screen.getByLabelText(/spotify or youtube urls/i)).toBeInTheDocument()
  })

  it('validates empty URL client side on Resolve', async () => {
    wrap()
    fireEvent.click(screen.getByRole('button', { name: /^resolve$/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/enter a url/i)
    expect(mockResolveLink).not.toHaveBeenCalled()
  })

  it('shows error for unsupported URL (422) on resolve', async () => {
    const err = Object.assign(new Error('unsupported URL'), { status: 422, name: 'ApiError' })
    const { ApiError } = await import('../lib/api')
    mockResolveLink.mockRejectedValue(new ApiError('POST', '/links/resolve', 422))
    wrap()
    const input = screen.getByLabelText(/spotify or youtube urls/i)
    fireEvent.change(input, { target: { value: 'https://example.com/foo' } })
    fireEvent.click(screen.getByRole('button', { name: /^resolve$/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/unsupported url/i)
    expect(err.message).toBeDefined()
  })

  it('resolves and shows preview card with title, artist, album, source, kind, cover', async () => {
    wrap()
    const input = screen.getByLabelText(/spotify or youtube urls/i)
    fireEvent.change(input, { target: { value: 'https://open.spotify.com/track/sp123' } })
    fireEvent.click(screen.getByRole('button', { name: /^resolve$/i }))
    const card = await screen.findByTestId('preview-card')
    expect(card).toHaveTextContent('Spotify track sp123')
    expect(card).toHaveTextContent('Test Artist')
    expect(card).toHaveTextContent('Test Album')
    expect(card).toHaveTextContent('spotify')
    expect(card).toHaveTextContent('track')
    expect(screen.getByRole('img', { name: /spotify track sp123/i })).toHaveAttribute('src', 'https://example.com/cover.jpg')
  })

  it('renders playlist dropdown with playlists', () => {
    wrap()
    expect(screen.getByLabelText(/add to playlist/i)).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'My Playlist' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Second' })).toBeInTheDocument()
  })

  it('has Download now checkbox default checked and helper text', () => {
    wrap()
    const cb = screen.getByLabelText(/download now/i) as HTMLInputElement
    expect(cb.checked).toBe(true)
    expect(screen.getByText(/the download runs on the server device/i)).toBeInTheDocument()
    expect(screen.getByText(/syncs to your canonical library/i)).toBeInTheDocument()
    expect(screen.getAllByText(/source-native/i).length).toBeGreaterThan(0)
  })

  it('Add from link calls batch and toasts success', async () => {
    wrap()
    const input = screen.getByLabelText(/spotify or youtube urls/i)
    fireEvent.change(input, { target: { value: 'https://open.spotify.com/track/sp123' } })
    fireEvent.click(screen.getByRole('button', { name: /^add from link$/i }))
    await waitFor(() => expect(mockAddFromLinksBatch).toHaveBeenCalledWith([{ url: 'https://open.spotify.com/track/sp123', playlistId: undefined, download: true, quality: 'high' }]))
    await waitFor(() => expect(mockPush).toHaveBeenCalledWith('Added 1 link', 'success'))
  })

  it('Add from link with playlist selected passes playlistId', async () => {
    mockAddFromLinksBatch.mockResolvedValue({
      results: [{ url: 'https://open.spotify.com/track/sp123', resolve: { kind: 'track', source: 'spotify', externalId: 'sp123', title: 't', artist: 'a', album: '', url: 'https://open.spotify.com/track/sp123' }, catalogId: 'trk_link_sp123', playlistId: 'pl1', job: { id: 'j1' } }],
    })
    wrap()
    const input = screen.getByLabelText(/spotify or youtube urls/i)
    fireEvent.change(input, { target: { value: 'https://open.spotify.com/track/sp123' } })
    const sel = screen.getByLabelText(/add to playlist/i) as HTMLSelectElement
    fireEvent.change(sel, { target: { value: 'pl1' } })
    fireEvent.click(screen.getByRole('button', { name: /^add from link$/i }))
    await waitFor(() => expect(mockAddFromLinksBatch).toHaveBeenCalledWith([{ url: 'https://open.spotify.com/track/sp123', playlistId: 'pl1', download: true, quality: 'high' }]))
    await waitFor(() => expect(mockNavigate).toHaveBeenCalledWith('/playlist/pl1'))
  })

  it('Download checkbox unchecked passes download false', async () => {
    wrap()
    const input = screen.getByLabelText(/spotify or youtube urls/i)
    fireEvent.change(input, { target: { value: 'https://open.spotify.com/track/sp123' } })
    const cb = screen.getByLabelText(/download now/i)
    fireEvent.click(cb)
    fireEvent.click(screen.getByRole('button', { name: /^add from link$/i }))
    await waitFor(() => expect(mockAddFromLinksBatch).toHaveBeenCalledWith([{ url: 'https://open.spotify.com/track/sp123', playlistId: undefined, download: false, quality: 'high' }]))
  })

  it('handles 404 playlist not found', async () => {
    mockAddFromLinksBatch.mockResolvedValue({
      results: [{ url: 'https://open.spotify.com/track/sp123', error: 'playlist not found' }],
    })
    wrap()
    const input = screen.getByLabelText(/spotify or youtube urls/i)
    fireEvent.change(input, { target: { value: 'https://open.spotify.com/track/sp123' } })
    fireEvent.click(screen.getByRole('button', { name: /^add from link$/i }))
    expect(await screen.findByTestId('add-results')).toHaveTextContent(/playlist not found/i)
  })

  it('handles 422 unsupported on add', async () => {
    mockAddFromLinksBatch.mockResolvedValue({
      results: [{ url: 'https://example.com/foo', error: 'unsupported URL' }],
    })
    wrap()
    const input = screen.getByLabelText(/spotify or youtube urls/i)
    fireEvent.change(input, { target: { value: 'https://example.com/foo' } })
    fireEvent.click(screen.getByRole('button', { name: /^add from link$/i }))
    expect(await screen.findByTestId('add-results')).toHaveTextContent(/unsupported url/i)
  })

  it('disables Add until at least one link is entered', () => {
    wrap()
    expect(screen.getByRole('button', { name: /^add from link$/i })).toBeDisabled()
  })

  it('shows success status when added to canonical library without playlist', async () => {
    mockAddFromLinksBatch.mockResolvedValue({
      results: [{ url: 'https://open.spotify.com/track/sp123', resolve: { kind: 'track', source: 'spotify', externalId: 'sp123', title: 't', artist: 'a', album: '', url: 'https://open.spotify.com/track/sp123' }, catalogId: 'trk_link_sp123' }],
    })
    wrap()
    const input = screen.getByLabelText(/spotify or youtube urls/i)
    fireEvent.change(input, { target: { value: 'https://open.spotify.com/track/sp123' } })
    fireEvent.click(screen.getByRole('button', { name: /^add from link$/i }))
    expect(await screen.findByRole('status')).toHaveTextContent(/added 1 link/i)
  })

  it('respects vocab: does not contain forbidden terms', () => {
    const { container } = wrap()
    const text = container.textContent?.toLowerCase() ?? ''
    expect(text).not.toContain('paste link')
    expect(text).not.toContain('url import')
    expect(text).not.toContain('client')
    expect(text).not.toContain('peer')
    expect(text).not.toContain('mirror')
    expect(text).not.toMatch(/\bcache\b/)
  })

  it('uses context vocab: contains required terms', () => {
    const { container } = wrap()
    const text = container.textContent?.toLowerCase() ?? ''
    expect(text).toContain('add from link')
    expect(text).toContain('download')
    expect(text).toContain('source-native')
    expect(text).toContain('canonical library')
    expect(text).toContain('playlist')
    expect(text).toContain('device')
    expect(text).toContain('server')
  })

  it('shows loading state on Resolve', async () => {
    let resolve: () => void
    mockResolveLink.mockReturnValue(new Promise((r) => (resolve = () => r({ kind: 'track', source: 'spotify', externalId: 'x', title: 't', artist: 'a', album: '', url: 'u' }))))
    wrap()
    const input = screen.getByLabelText(/spotify or youtube urls/i)
    fireEvent.change(input, { target: { value: 'https://open.spotify.com/track/x' } })
    fireEvent.click(screen.getByRole('button', { name: /^resolve$/i }))
    expect(await screen.findByText(/resolving/i)).toBeInTheDocument()
    resolve!()
  })
  it('adds every pasted link and reports each outcome via batch', async () => {
    mockAddFromLinksBatch.mockResolvedValue({
      results: [
        { url: 'https://a', catalogId: 'trk_a' },
        { url: 'https://bad', error: 'unsupported URL' },
        { url: 'https://c', catalogId: 'trk_c' },
      ],
    })

    wrap()
    fireEvent.change(screen.getByLabelText(/spotify or youtube urls/i), {
      target: { value: 'https://a\nhttps://bad\nhttps://c' },
    })
    expect(screen.getByRole('button', { name: /add 3 links/i })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /add 3 links/i }))

    await waitFor(() => expect(mockAddFromLinksBatch).toHaveBeenCalledTimes(1))
    expect(mockAddFromLinksBatch).toHaveBeenCalledWith([
      { url: 'https://a', playlistId: undefined, download: true, quality: 'high' },
      { url: 'https://bad', playlistId: undefined, download: true, quality: 'high' },
      { url: 'https://c', playlistId: undefined, download: true, quality: 'high' },
    ])
    const results = await screen.findByTestId('add-results')
    expect(results).toHaveTextContent('https://a')
    expect(results).toHaveTextContent(/unsupported url/i)
    expect(results).toHaveTextContent('https://c')
    await waitFor(() => expect(mockPush).toHaveBeenCalledWith('Added 2 of 3 links', 'error'))
  })

  it('previews every pasted link', async () => {
    mockResolveLink.mockReset()
    mockResolveLink
      .mockResolvedValueOnce({ kind: 'track', source: 'spotify', externalId: '1', title: 'First', artist: 'A', album: '', url: 'https://a' })
      .mockResolvedValueOnce({ kind: 'track', source: 'spotify', externalId: '2', title: 'Second', artist: 'B', album: '', url: 'https://b' })

    wrap()
    fireEvent.change(screen.getByLabelText(/spotify or youtube urls/i), {
      target: { value: 'https://a\nhttps://b' },
    })
    fireEvent.click(screen.getByRole('button', { name: /^resolve$/i }))

    await waitFor(() => expect(screen.getAllByTestId('preview-card')).toHaveLength(2))
    const cards = screen.getAllByTestId('preview-card')
    expect(cards[0]).toHaveTextContent('First')
    expect(cards[1]).toHaveTextContent('Second')
  })

  it('sends each link its own trim range via batch', async () => {
    mockAddFromLinksBatch.mockResolvedValue({ results: [{ url: 'https://www.youtube.com/watch?v=one', catalogId: 'trk_a' }, { url: 'https://www.youtube.com/watch?v=two', catalogId: 'trk_b' }] })

    wrap()
    fireEvent.change(screen.getByLabelText(/spotify or youtube urls/i), {
      target: { value: 'https://www.youtube.com/watch?v=one\nhttps://www.youtube.com/watch?v=two' },
    })

    fireEvent.click(
      screen.getByRole('button', { name: /advanced options for https:\/\/www\.youtube\.com\/watch\?v=one/i }),
    )
    fireEvent.change(
      screen.getByLabelText(/start time for https:\/\/www\.youtube\.com\/watch\?v=one/i),
      { target: { value: '1:30' } },
    )

    fireEvent.click(screen.getByRole('button', { name: /add 2 links/i }))
    await waitFor(() => expect(mockAddFromLinksBatch).toHaveBeenCalledTimes(1))

    expect(mockAddFromLinksBatch).toHaveBeenCalledWith([
      { url: 'https://www.youtube.com/watch?v=one', playlistId: undefined, download: true, quality: 'high', startTime: '1:30' },
      { url: 'https://www.youtube.com/watch?v=two', playlistId: undefined, download: true, quality: 'high' },
    ])
  })

  it('reports how many chapters a split produced', async () => {
    mockAddFromLinksBatch.mockResolvedValue({
      results: [{ url: 'https://www.youtube.com/watch?v=chap', catalogId: 'trk_a', jobs: [{ id: 'j1' }, { id: 'j2' }, { id: 'j3' }] }],
    })

    wrap()
    fireEvent.change(screen.getByLabelText(/spotify or youtube urls/i), {
      target: { value: 'https://www.youtube.com/watch?v=chap' },
    })
    fireEvent.click(screen.getByRole('button', { name: /^add from link$/i }))

    const results = await screen.findByTestId('add-results')
    expect(results).toHaveTextContent(/as 3 chapters/i)
  })

  it('offers no trim options for a Spotify link', () => {
    wrap()
    fireEvent.change(screen.getByLabelText(/spotify or youtube urls/i), {
      target: { value: 'https://open.spotify.com/track/sp1' },
    })
    expect(screen.queryByTestId('link-options')).not.toBeInTheDocument()
  })
})

describe('AddFromLink audio quality', () => {
  it('defaults the quality select to the configured download quality', () => {
    wrap()
    const select = screen.getByLabelText(/audio quality/i) as HTMLSelectElement
    expect(select.value).toBe('high')
    expect(screen.getByText(/capped by what the source serves/i)).toBeInTheDocument()
  })

  it('passes a per-download quality override to batch', async () => {
    mockAddFromLinksBatch.mockResolvedValue({ results: [{ url: 'https://open.spotify.com/track/sp123', catalogId: 'trk_1' }] })
    wrap()
    fireEvent.change(screen.getByLabelText(/spotify or youtube url/i), {
      target: { value: 'https://open.spotify.com/track/sp123' },
    })
    fireEvent.change(screen.getByLabelText(/audio quality/i), { target: { value: 'best' } })
    fireEvent.click(screen.getByRole('button', { name: /add from link/i }))
    await waitFor(() => expect(mockAddFromLinksBatch).toHaveBeenCalled())
    expect(mockAddFromLinksBatch).toHaveBeenCalledWith([{ url: 'https://open.spotify.com/track/sp123', playlistId: undefined, download: true, quality: 'best' }])
  })

  it('hides the quality select when the download is not going to run', () => {
    wrap()
    fireEvent.click(screen.getByLabelText(/download now/i))
    expect(screen.queryByLabelText(/audio quality/i)).toBeNull()
  })

})

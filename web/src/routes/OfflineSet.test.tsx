import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import OfflineSet from './OfflineSet'

const mockUseOfflineSet = vi.fn()
const mockSetOfflineSet = vi.fn()
const mockUseSyncStatus = vi.fn()

vi.mock('../lib/offlineSetApi', () => ({
  useOfflineSet: (...args: unknown[]) => mockUseOfflineSet(...args),
  setOfflineSet: (...args: unknown[]) => mockSetOfflineSet(...args),
  listOfflineSet: vi.fn(),
  removeOfflineSet: vi.fn(),
}))

vi.mock('../lib/syncApi', () => ({
  useSyncStatus: (...args: unknown[]) => mockUseSyncStatus(...args),
  getSyncStatus: vi.fn(),
}))

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <OfflineSet />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('OfflineSet route', () => {
  beforeEach(() => {
    mockUseSyncStatus.mockReturnValue({ data: { revision: 5, deviceCount: 2 } })
    mockSetOfflineSet.mockResolvedValue({ playlistId: 'pl1', enabled: true, updatedAt: 123 })
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  it('renders heading Offline set', () => {
    mockUseOfflineSet.mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() })
    wrap()
    expect(screen.getByRole('heading', { name: /offline set/i })).toBeInTheDocument()
  })

  it('shows empty state when no playlists offline', () => {
    mockUseOfflineSet.mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() })
    wrap()
    expect(screen.getByText('No playlists offline')).toBeInTheDocument()
  })

  it('renders list of offline playlists', () => {
    mockUseOfflineSet.mockReturnValue({
      data: [
        { playlistId: 'pl1', enabled: true, updatedAt: 1000, playlistName: 'My Playlist' },
        { playlistId: 'pl2', enabled: false, updatedAt: 2000, playlistName: 'Second' },
      ],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    })
    wrap()
    expect(screen.getByText('My Playlist')).toBeInTheDocument()
    expect(screen.getByText('Second')).toBeInTheDocument()
    // toggles: first enabled, second disabled
    const switches = screen.getAllByRole('switch')
    expect(switches[0]).toHaveAttribute('aria-checked', 'true')
    expect(switches[1]).toHaveAttribute('aria-checked', 'false')
  })

  it('toggles enabled calls setOfflineSet', async () => {
    mockUseOfflineSet.mockReturnValue({
      data: [{ playlistId: 'pl1', enabled: true, updatedAt: 1000, playlistName: 'My Playlist' }],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    })
    wrap()
    const toggle = screen.getByRole('switch', { name: /keep my playlist offline/i })
    fireEvent.click(toggle)
    await waitFor(() => expect(mockSetOfflineSet).toHaveBeenCalledWith('pl1', false))
  })

  it('toggles disabled to enabled', async () => {
    mockUseOfflineSet.mockReturnValue({
      data: [{ playlistId: 'pl2', enabled: false, updatedAt: 2000, playlistName: 'Second' }],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    })
    wrap()
    const toggle = screen.getByRole('switch', { name: /keep second offline/i })
    fireEvent.click(toggle)
    await waitFor(() => expect(mockSetOfflineSet).toHaveBeenCalledWith('pl2', true))
  })

  it('handles error state with retry', async () => {
    const refetch = vi.fn()
    mockUseOfflineSet.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error('network error'),
      refetch,
    })
    wrap()
    expect(screen.getByRole('alert')).toHaveTextContent(/network error/i)
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    expect(refetch).toHaveBeenCalled()
  })

  it('shows toggle error alert when setOfflineSet fails', async () => {
    mockSetOfflineSet.mockRejectedValue(new Error('failed to update'))
    mockUseOfflineSet.mockReturnValue({
      data: [{ playlistId: 'pl1', enabled: true, updatedAt: 1000, playlistName: 'My Playlist' }],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    })
    wrap()
    fireEvent.click(screen.getByRole('switch', { name: /keep my playlist offline/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/failed to update/i)
  })

  it('shows sync status indicator with revision and device count', () => {
    mockUseOfflineSet.mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() })
    mockUseSyncStatus.mockReturnValue({ data: { revision: 7, deviceCount: 3 } })
    wrap()
    const status = screen.getByTestId('sync-status')
    expect(status).toHaveTextContent(/revision 7/i)
    expect(status).toHaveTextContent(/3 device\(s\)/i)
  })

  it('shows last sync time in sync status', () => {
    mockUseOfflineSet.mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() })
    mockUseSyncStatus.mockReturnValue({ data: { revision: 1, deviceCount: 1 } })
    wrap()
    const status = screen.getByTestId('sync-status')
    expect(status).toHaveTextContent(/last sync/i)
  })

  it('shows sync status unavailable when no data', () => {
    mockUseOfflineSet.mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() })
    mockUseSyncStatus.mockReturnValue({ data: undefined })
    wrap()
    expect(screen.getByTestId('sync-status')).toHaveTextContent(/sync status unavailable/i)
  })

  it('shows helper text about removing not deleting', () => {
    mockUseOfflineSet.mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() })
    wrap()
    expect(screen.getAllByText(/removing from offline set does not delete the playlist/i).length).toBeGreaterThan(0)
  })

  it('respects vocab: does not contain forbidden terms', () => {
    mockUseOfflineSet.mockReturnValue({ data: [], isLoading: false, isError: false, refetch: vi.fn() })
    const { container } = wrap()
    const text = container.textContent?.toLowerCase() ?? ''
    expect(text).not.toContain('offline library')
    expect(text).not.toContain('offline cache')
    expect(text).not.toContain('offline client')
    expect(text).not.toContain('offline node')
    expect(text).not.toContain('offline peer')
  })
})

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import UpgradeQuality from './UpgradeQuality'

const mockUseUpgradable = vi.fn()
const mockMutateAsync = vi.fn()
const mockPush = vi.fn()

vi.mock('../lib/upgradeApi', () => ({
  useUpgradable: (...a: unknown[]) => mockUseUpgradable(...a),
  useUpgradeDownload: () => ({ mutateAsync: mockMutateAsync, isPending: false }),
}))
vi.mock('../lib/settingsApi', () => ({
  useSettings: () => ({ data: { downloadQuality: 'high' } }),
}))
vi.mock('../lib/toastStore', () => ({
  useToastStore: (sel: (s: unknown) => unknown) => sel({ push: mockPush }),
}))

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <UpgradeQuality />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const rows = [
  { jobId: 'j1', source: 'spotify', externalId: 'sp1', artist: 'A', title: 'First', album: 'Al', quality: 'low' },
  { jobId: 'j2', source: 'spotify', externalId: 'sp2', artist: 'B', title: 'Second', album: 'Al', quality: 'medium' },
]

beforeEach(() => {
  vi.clearAllMocks()
  mockUseUpgradable.mockReturnValue({ data: rows, isLoading: false })
  mockMutateAsync.mockResolvedValue({})
})

describe('UpgradeQuality route', () => {
  it('lists downloads whose tier differs from the target, with their current quality', () => {
    wrap()
    expect(screen.getByText('First')).toBeInTheDocument()
    expect(screen.getByText('Second')).toBeInTheDocument()
    expect(screen.getByText(/2 not at High/i)).toBeInTheDocument()
  })

  it('defaults the target to the configured download quality', () => {
    wrap()
    expect((screen.getByLabelText(/target quality/i) as HTMLSelectElement).value).toBe('high')
  })

  it('re-downloads only the selected tracks, carrying their current quality', async () => {
    wrap()
    fireEvent.click(screen.getByLabelText('Select First'))
    fireEvent.click(screen.getByRole('button', { name: /apply to selected/i }))
    await waitFor(() => expect(mockMutateAsync).toHaveBeenCalledTimes(1))
    expect(mockMutateAsync).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'First', quality: 'high', currentQuality: 'low' }),
    )
  })

  it('select all picks up every row', async () => {
    wrap()
    fireEvent.click(screen.getByLabelText('Select all'))
    fireEvent.click(screen.getByRole('button', { name: /apply to selected/i }))
    await waitFor(() => expect(mockMutateAsync).toHaveBeenCalledTimes(2))
  })

  it('keeps going when one re-download fails and reports the shortfall', async () => {
    mockMutateAsync.mockRejectedValueOnce(new Error('nope'))
    wrap()
    fireEvent.click(screen.getByLabelText('Select all'))
    fireEvent.click(screen.getByRole('button', { name: /apply to selected/i }))
    await waitFor(() => expect(mockPush).toHaveBeenCalled())
    expect(mockMutateAsync).toHaveBeenCalledTimes(2)
    expect(mockPush).toHaveBeenCalledWith('Queued 1 of 2 re-downloads', 'error')
  })

  it('shows an empty state when every download is already at the tier', () => {
    mockUseUpgradable.mockReturnValue({ data: [], isLoading: false })
    wrap()
    expect(screen.getByText(/nothing to change/i)).toBeInTheDocument()
  })
})

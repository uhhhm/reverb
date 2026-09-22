import { beforeEach, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ManualSyncControl } from './ManualSyncControl'
import { triggerSync, getSyncStatus } from '../lib/syncApi'
import { useSyncStore } from '../lib/syncStore'

vi.mock('../lib/syncApi', () => ({ triggerSync: vi.fn(), getSyncStatus: vi.fn() }))

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><ManualSyncControl /></QueryClientProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(triggerSync).mockResolvedValue({ status: 'started' })
  vi.mocked(getSyncStatus).mockResolvedValue({ revision: 0, deviceCount: 2 })
  useSyncStore.setState({ syncing: false })
})

it('triggers manual sync and disables the button during the round', async () => {
  renderPage()
  await screen.findByRole('button', { name: 'Sync now' })
  fireEvent.click(screen.getByRole('button', { name: 'Sync now' }))
  await waitFor(() => expect(triggerSync).toHaveBeenCalledTimes(1))
  act(() => useSyncStore.getState().setSyncing(true))
  expect(screen.getByRole('button', { name: 'Syncing…' })).toBeDisabled()
  act(() => useSyncStore.getState().setSyncing(false))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Sync now' })).toBeEnabled())
})

it('shows a failed sync request', async () => {
  vi.mocked(triggerSync).mockRejectedValue(new Error('Sync unavailable'))
  renderPage()
  await screen.findByRole('button', { name: 'Sync now' })
  fireEvent.click(screen.getByRole('button', { name: 'Sync now' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Sync unavailable')
  expect(screen.getByRole('button', { name: 'Sync now' })).toBeEnabled()
})

it('recovers pending and completed sync status without any WebSocket events', async () => {
  const pending = { id: 1, state: 'pending' as const, startedAt: 1000, finishedAt: 0, durationMs: 0, peers: 0, succeeded: 0, errors: [] }
  vi.mocked(triggerSync).mockResolvedValue({ status: 'started', round: pending })
  renderPage()
  await screen.findByRole('button', { name: 'Sync now' })
  vi.mocked(getSyncStatus).mockResolvedValue({ revision: 0, deviceCount: 2, round: pending })
  fireEvent.click(screen.getByRole('button', { name: 'Sync now' }))
  expect(await screen.findByText('Sync pending…')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Syncing…' })).toBeDisabled()
  vi.mocked(getSyncStatus).mockResolvedValue({ revision: 1, deviceCount: 2, round: { ...pending, state: 'completed', finishedAt: 2000, durationMs: 1000, peers: 1, succeeded: 1 } })
  expect(await screen.findByText(/Library exchange finished with 1 device/, {}, { timeout: 2500 })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Sync now' })).toBeEnabled()
})

it('shows device failures instead of a successful request acknowledgement', async () => {
  vi.mocked(getSyncStatus).mockResolvedValue({ revision: 0, deviceCount: 2, round: { id: 1, state: 'failed', startedAt: 1000, finishedAt: 2000, durationMs: 1000, peers: 1, succeeded: 0, errors: ['Laptop: pairing required'] } })
  renderPage()
  expect(await screen.findByText('Laptop: pairing required')).toBeInTheDocument()
  expect(screen.getByText(/Sync finished with errors/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Sync now' })).toBeEnabled()
})

it('explains when there are no paired sync devices', async () => {
  vi.mocked(getSyncStatus).mockResolvedValue({ revision: 0, deviceCount: 1, round: { id: 1, state: 'no_peers', startedAt: 1000, finishedAt: 2000, durationMs: 0, peers: 0, succeeded: 0, errors: [] } })
  renderPage()
  expect(await screen.findByText(/No paired devices to sync with/)).toBeInTheDocument()
})

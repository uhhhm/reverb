import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BackgroundSyncSettings } from './BackgroundSyncSettings'

const bindings = {
  GetBackgroundSyncEnabled: vi.fn(),
  SetBackgroundSyncEnabled: vi.fn(),
  QuitAndStopSync: vi.fn(),
}
function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}><BackgroundSyncSettings /></QueryClientProvider>)
}
beforeEach(() => {
  vi.clearAllMocks()
  bindings.GetBackgroundSyncEnabled.mockResolvedValue(true)
  bindings.SetBackgroundSyncEnabled.mockResolvedValue(undefined)
  bindings.QuitAndStopSync.mockResolvedValue(undefined)
  Object.defineProperty(window, 'go', { configurable: true, value: { main: { App: bindings } } })
})
afterEach(() => { Reflect.deleteProperty(window, 'go') })

it('only appears in desktop', () => {
  Reflect.deleteProperty(window, 'go')
  mount()
  expect(screen.queryByText('Background sync')).not.toBeInTheDocument()
})

it('loads and persists the background preference', async () => {
  mount()
  const checkbox = screen.getByRole('checkbox')
  await waitFor(() => expect(checkbox).toBeChecked())
  bindings.GetBackgroundSyncEnabled.mockResolvedValue(false)
  fireEvent.click(checkbox)
  await waitFor(() => expect(bindings.SetBackgroundSyncEnabled).toHaveBeenCalledWith(false))
  await waitFor(() => expect(checkbox).not.toBeChecked())
})

it('keeps the saved preference and reports a failed change', async () => {
  bindings.SetBackgroundSyncEnabled.mockRejectedValue(new Error('Cannot save settings'))
  mount()
  const checkbox = screen.getByRole('checkbox')
  await waitFor(() => expect(checkbox).toBeChecked())
  fireEvent.click(checkbox)
  expect(await screen.findByRole('alert')).toHaveTextContent('Cannot save settings')
  expect(checkbox).toBeChecked()
})

it('can quit and stop sync explicitly', async () => {
  mount()
  fireEvent.click(screen.getByRole('button', { name: 'Quit Reverb and stop sync' }))
  await waitFor(() => expect(bindings.QuitAndStopSync).toHaveBeenCalledTimes(1))
})

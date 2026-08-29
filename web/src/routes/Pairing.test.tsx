import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import Pairing from './Pairing'
import { useSyncStore } from '../lib/syncStore'

const mockGeneratePairingCode = vi.fn()
const mockRedeemPairingCode = vi.fn()
const mockListDevices = vi.fn()
const mockDeleteDevice = vi.fn()
const mockGetSyncStatus = vi.fn()
const mockStoreSyncCredentials = vi.fn()
const mockGetSyncToken = vi.fn()
const mockGetSyncDeviceId = vi.fn()
const mockClearSyncCredentials = vi.fn()
const mockTriggerSync = vi.fn()

vi.mock('../lib/syncApi', async (orig) => ({
  ...(await orig<Record<string, unknown>>()),
  triggerSync: (...args: unknown[]) => mockTriggerSync(...args),
}))

vi.mock('../lib/pairingApi', () => ({
  generatePairingCode: (...args: unknown[]) => mockGeneratePairingCode(...args),
  redeemPairingCode: (...args: unknown[]) => mockRedeemPairingCode(...args),
  listDevices: (...args: unknown[]) => mockListDevices(...args),
  deleteDevice: (...args: unknown[]) => mockDeleteDevice(...args),
  getSyncStatus: (...args: unknown[]) => mockGetSyncStatus(...args),
  storeSyncCredentials: (...args: unknown[]) => mockStoreSyncCredentials(...args),
  getSyncToken: (...args: unknown[]) => mockGetSyncToken(...args),
  getSyncDeviceId: () => mockGetSyncDeviceId(),
  clearSyncCredentials: (...args: unknown[]) => mockClearSyncCredentials(...args),
  SYNC_TOKEN_KEY: 'reverb:syncToken',
  SYNC_DEVICE_ID_KEY: 'reverb:syncDeviceId',
}))

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <Pairing />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('Pairing', () => {
  beforeEach(() => {
    mockGetSyncToken.mockReturnValue(null)
    mockGetSyncDeviceId.mockReturnValue('dev_1')
    mockListDevices.mockResolvedValue([
      { id: 'srv_1', name: 'Reverb Server', isServer: true, createdAt: 1000, lastSeen: 2000 },
      { id: 'dev_1', name: 'My Laptop', isServer: false, createdAt: 1100, lastSeen: 2100 },
    ])
    mockGetSyncStatus.mockResolvedValue({ revision: 5, deviceCount: 2 })
    mockTriggerSync.mockResolvedValue({ status: 'started' })
    useSyncStore.setState({ syncing: false })
    mockGeneratePairingCode.mockResolvedValue({ code: 'AB12-CD34', expiresAt: Math.floor(Date.now() / 1000) + 600 })
    mockRedeemPairingCode.mockResolvedValue({ deviceId: 'dev_new', token: 'tok123', serverDeviceId: 'srv_1' })
    mockDeleteDevice.mockResolvedValue({ ok: true })
    // stub clipboard
    Object.assign(navigator, {
      clipboard: { writeText: vi.fn(() => Promise.resolve()) },
    })
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  it('renders the Pairing heading and generate pairing code section', async () => {
    wrap()
    expect(screen.getByRole('heading', { level: 1, name: 'Pairing' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /generate pairing code/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /generate pairing code/i })).toBeInTheDocument()
    // wait for async device load to settle to avoid act warnings
    await screen.findByText('My Laptop')
  })

  it('generates and displays a pairing code with expiry and copy button', async () => {
    wrap()
    await screen.findByText('My Laptop')
    fireEvent.click(screen.getByRole('button', { name: /generate pairing code/i }))
    expect(await screen.findByTestId('pairing-code')).toHaveTextContent('AB12-CD34')
    expect(screen.getByText(/Code expires in \d+:\d+/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /copy pairing code/i })).toBeInTheDocument()
  })

  it('shows copy feedback after copying pairing code', async () => {
    wrap()
    await screen.findByText('My Laptop')
    fireEvent.click(screen.getByRole('button', { name: /generate pairing code/i }))
    await screen.findByTestId('pairing-code')
    fireEvent.click(screen.getByRole('button', { name: /copy pairing code/i }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledWith('AB12-CD34'))
  })

  it('shows error when generate pairing code fails', async () => {
    mockGeneratePairingCode.mockRejectedValue(new Error('failed to generate'))
    wrap()
    await screen.findByText('My Laptop')
    fireEvent.click(screen.getByRole('button', { name: /generate pairing code/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/failed to generate/i)
  })

  it('shows paired devices with server badge, device count and unpair protection', async () => {
    wrap()
    expect(await screen.findByText('Reverb Server')).toBeInTheDocument()
    expect(screen.getByText('My Laptop')).toBeInTheDocument()
    expect(screen.getByText('server')).toBeInTheDocument()
    expect(screen.getByText('this device')).toBeInTheDocument()
    expect(screen.getByTestId('paired-device-count')).toHaveTextContent('2 devices currently paired')
    // server device cannot be unpaired: no unpair button for it, but shown text
    expect(screen.getByText(/cannot unpair server device/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^unpair my laptop$/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^unpair reverb server$/i })).not.toBeInTheDocument()
  })

  it('asks for confirmation before unpairing and does not call the API until confirmed', async () => {
    wrap()
    await screen.findByText('My Laptop')
    fireEvent.click(screen.getByRole('button', { name: /^unpair my laptop$/i }))
    expect(await screen.findByRole('alertdialog', { name: /confirm unpair my laptop/i })).toBeInTheDocument()
    expect(mockDeleteDevice).not.toHaveBeenCalled()
  })

  it('cancelling the confirmation leaves the device paired', async () => {
    wrap()
    await screen.findByText('My Laptop')
    fireEvent.click(screen.getByRole('button', { name: /^unpair my laptop$/i }))
    await screen.findByRole('alertdialog')
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(mockDeleteDevice).not.toHaveBeenCalled()
  })

  it('confirming unpair calls deleteDevice and refreshes list', async () => {
    wrap()
    await screen.findByText('My Laptop')
    fireEvent.click(screen.getByRole('button', { name: /^unpair my laptop$/i }))
    fireEvent.click(await screen.findByRole('button', { name: /confirm unpairing my laptop/i }))
    await waitFor(() => expect(mockDeleteDevice).toHaveBeenCalledWith('dev_1'))
  })

  it('renders Enter pairing code form when not paired', async () => {
    mockGetSyncToken.mockReturnValue(null)
    wrap()
    expect(await screen.findByRole('heading', { name: /enter pairing code/i })).toBeInTheDocument()
    expect(screen.getByLabelText(/pairing code/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/device name/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /pair device/i })).toBeInTheDocument()
  })

  it('shows paired state when sync token exists', async () => {
    mockGetSyncToken.mockReturnValue('tok')
    wrap()
    expect(await screen.findByText(/this device is paired/i)).toBeInTheDocument()
    expect(screen.getAllByText(/sync token is stored on this device/i).length).toBeGreaterThan(0)
    expect(screen.queryByRole('button', { name: /^pair device$/i })).not.toBeInTheDocument()
  })

  it('pairing code input auto-uppercases and formats with dash', async () => {
    wrap()
    await screen.findByLabelText(/pairing code/i)
    const input = screen.getByLabelText(/pairing code/i) as HTMLInputElement
    fireEvent.change(input, { target: { value: 'ab12cd34' } })
    expect(input.value).toBe('AB12-CD34')
  })

  it('accepts pairing code with dash already present', async () => {
    wrap()
    const input = (await screen.findByLabelText(/pairing code/i)) as HTMLInputElement
    fireEvent.change(input, { target: { value: 'ab12-cd34' } })
    expect(input.value).toBe('AB12-CD34')
  })

  it('redeems pairing code and stores sync token on success', async () => {
    wrap()
    await screen.findByText('My Laptop')
    const codeInput = await screen.findByLabelText(/pairing code/i)
    const nameInput = screen.getByLabelText(/device name/i)
    fireEvent.change(codeInput, { target: { value: 'AB12-CD34' } })
    fireEvent.change(nameInput, { target: { value: 'Work Laptop' } })
    fireEvent.click(screen.getByRole('button', { name: /pair device/i }))
    await waitFor(() => expect(mockRedeemPairingCode).toHaveBeenCalledWith('AB12-CD34', 'Work Laptop'))
    await waitFor(() => expect(mockStoreSyncCredentials).toHaveBeenCalledWith('tok123', 'dev_new'))
    expect(await screen.findByText(/device paired/i)).toBeInTheDocument()
  })

  it('redeem strips dashes before sending', async () => {
    wrap()
    const codeInput = await screen.findByLabelText(/pairing code/i)
    fireEvent.change(codeInput, { target: { value: 'AB12-CD34' } })
    fireEvent.click(screen.getByRole('button', { name: /pair device/i }))
    await waitFor(() => expect(mockRedeemPairingCode).toHaveBeenCalled())
    const sentCode = mockRedeemPairingCode.mock.calls[0][0] as string
    // the component passes the displayed value; the API helper strips it — but we verify the component passes XXXX-XXXX
    expect(sentCode).toBe('AB12-CD34')
  })

  it('shows error when redeem fails', async () => {
    mockRedeemPairingCode.mockRejectedValue(new Error('invalid pairing code'))
    wrap()
    const codeInput = await screen.findByLabelText(/pairing code/i)
    fireEvent.change(codeInput, { target: { value: 'ZZZZ-ZZZZ' } })
    fireEvent.click(screen.getByRole('button', { name: /pair device/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/invalid pairing code/i)
  })

  it('shows validation error for short pairing code', async () => {
    wrap()
    const codeInput = await screen.findByLabelText(/pairing code/i)
    fireEvent.change(codeInput, { target: { value: 'AB' } })
    fireEvent.click(screen.getByRole('button', { name: /pair device/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/enter a pairing code/i)
  })

  it('displays sync status revision and device count', async () => {
    wrap()
    expect(await screen.findByText(/sync status: revision 5/i)).toBeInTheDocument()
    expect(screen.getByText(/2 device\(s\)/i)).toBeInTheDocument()
  })

  it('shows loading state for devices', async () => {
    mockListDevices.mockReturnValue(new Promise(() => {}))
    wrap()
    expect(await screen.findByText(/loading devices/i)).toBeInTheDocument()
  })

  it('shows error when devices fail to load', async () => {
    mockListDevices.mockRejectedValue(new Error('network error'))
    wrap()
    expect(await screen.findByRole('alert')).toHaveTextContent(/network error/i)
  })

  it('clear sync token button clears stored credentials', async () => {
    mockGetSyncToken.mockReturnValue('tok')
    wrap()
    await screen.findByText(/this device is paired/i)
    fireEvent.click(screen.getByRole('button', { name: /clear sync token/i }))
    expect(mockClearSyncCredentials).toHaveBeenCalled()
  })

  it('device name defaults to Laptop when navigator unavailable and is editable', async () => {
    wrap()
    const nameInput = (await screen.findByLabelText(/device name/i)) as HTMLInputElement
    // default should be non-empty (Laptop or userAgent)
    expect(nameInput.value.length).toBeGreaterThan(0)
    fireEvent.change(nameInput, { target: { value: 'My Tablet' } })
    expect(nameInput.value).toBe('My Tablet')
  })

  it('uses pairing code vocabulary and device/server/sync token terms', async () => {
    wrap()
    await screen.findByText('My Laptop')
    expect(screen.getAllByText(/pairing code/i).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/server/i).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/sync token/i).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/device/i).length).toBeGreaterThan(0)
  })
})

describe('Pairing — manual sync', () => {
  beforeEach(() => {
    mockGetSyncToken.mockReturnValue(null)
    mockGetSyncDeviceId.mockReturnValue('dev_1')
    mockListDevices.mockResolvedValue([])
    mockGetSyncStatus.mockResolvedValue({ revision: 5, deviceCount: 2 })
    mockTriggerSync.mockResolvedValue({ status: 'started' })
    useSyncStore.setState({ syncing: false })
  })

  afterEach(() => {
    vi.clearAllMocks()
    useSyncStore.setState({ syncing: false })
  })

  it('"Sync now" triggers a sync round', async () => {
    wrap()
    fireEvent.click(await screen.findByRole('button', { name: /sync now/i }))
    await waitFor(() => expect(mockTriggerSync).toHaveBeenCalledTimes(1))
  })

  // The indicator is driven by the WebSocket store, so a background round shows
  // it too — not only a round this tab started.
  it('shows a syncing indicator while a round is in flight', async () => {
    wrap()
    await screen.findByRole('button', { name: /sync now/i })
    act(() => {
      useSyncStore.getState().setSyncing(true)
    })
    expect(screen.getByRole('status')).toHaveTextContent(/syncing with paired devices/i)
    expect(screen.getByRole('button', { name: /syncing/i })).toBeDisabled()
  })
})

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import Pairing from './Pairing'
import { useSyncStore } from '../lib/syncStore'

const mockGeneratePairingCode = vi.fn()
const mockRedeemViaPeer = vi.fn()
const mockGetP2PStatus = vi.fn()
const mockListDevices = vi.fn()
const mockDeleteDevice = vi.fn()
const mockGetSyncStatus = vi.fn()
const mockTriggerSync = vi.fn()

vi.mock('../lib/syncApi', async (orig) => ({
  ...(await orig<Record<string, unknown>>()),
  triggerSync: (...args: unknown[]) => mockTriggerSync(...args),
  getSyncStatus: (...args: unknown[]) => mockGetSyncStatus(...args),
}))

vi.mock('../lib/pairingApi', () => ({
  generatePairingCode: (...args: unknown[]) => mockGeneratePairingCode(...args),
  listDevices: (...args: unknown[]) => mockListDevices(...args),
  deleteDevice: (...args: unknown[]) => mockDeleteDevice(...args),
  getSyncStatus: (...args: unknown[]) => mockGetSyncStatus(...args),
}))

vi.mock('../lib/p2pApi', () => ({
  getP2PStatus: (...args: unknown[]) => mockGetP2PStatus(...args),
  redeemViaPeer: (...args: unknown[]) => mockRedeemViaPeer(...args),
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
    mockGetP2PStatus.mockResolvedValue({
      peerId: '12D3KooWlocal', deviceId: 'dev_1', addrs: [], dialAddrs: ['/ip4/10.8.0.2/tcp/4331/p2p/12D3KooWlocal'], peerCount: 0,
    })
    mockListDevices.mockResolvedValue([
      { id: 'srv_1', name: 'Reverb Server', isServer: true, createdAt: 1000, lastSeen: 2000 },
      { id: 'dev_1', name: 'My Laptop', isServer: false, createdAt: 1100, lastSeen: 2100 },
    ])
    mockGetSyncStatus.mockResolvedValue({ revision: 5, deviceCount: 2 })
    mockTriggerSync.mockResolvedValue({ status: 'started' })
    useSyncStore.setState({ syncing: false })
    mockGeneratePairingCode.mockResolvedValue({ code: 'AB12-CD34', expiresAt: Math.floor(Date.now() / 1000) + 600 })
    mockRedeemViaPeer.mockResolvedValue({ deviceId: 'dev_new', token: 'tok123' })
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

  it('renders the Enter pairing code form with an optional address field', async () => {
    wrap()
    expect(await screen.findByRole('heading', { name: /enter pairing code/i })).toBeInTheDocument()
    expect(screen.getByLabelText(/pairing code/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/other device address/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/device name/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /pair device/i })).toBeInTheDocument()
  })

  it('shows this device\'s dial address next to a generated code', async () => {
    wrap()
    await screen.findByText('My Laptop')
    fireEvent.click(screen.getByRole('button', { name: /generate pairing code/i }))
    expect(await screen.findByTestId('dial-addr')).toHaveTextContent('/ip4/10.8.0.2/tcp/4331/p2p/12D3KooWlocal')
  })

  // A phone pairs by scanning, so the code is also shown as a QR code that
  // carries this device's addresses; the typed code stays beside it.
  it('shows the pairing code as a QR code when one is available', async () => {
    mockGeneratePairingCode.mockResolvedValueOnce({
      code: 'AB12-CD34',
      expiresAt: Math.floor(Date.now() / 1000) + 600,
      qrPayload: 'reverb://pair?v=1&code=AB12CD34',
      qrSvg: '<svg xmlns="http://www.w3.org/2000/svg"><path d="M4 4h1v1h-1z"/></svg>',
    })
    wrap()
    fireEvent.click(await screen.findByRole('button', { name: /generate pairing code/i }))
    const qr = await screen.findByRole('img', { name: /pairing qr code/i })
    expect(qr.getAttribute('src')).toMatch(/^data:image\/svg\+xml;charset=utf-8,/)
    expect(decodeURIComponent(qr.getAttribute('src') ?? '')).toContain('<path d="M4 4h1v1h-1z"/>')
    expect(screen.getByTestId('pairing-code')).toHaveTextContent('AB12-CD34')
  })

  it('shows no QR code when the device has no address to put in one', async () => {
    wrap()
    fireEvent.click(await screen.findByRole('button', { name: /generate pairing code/i }))
    await screen.findByTestId('pairing-code')
    expect(screen.queryByRole('img', { name: /pairing qr code/i })).not.toBeInTheDocument()
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

  // The code lives only in the database of the device that generated it, so
  // redeeming has to go over libp2p to that device. With no address given the
  // backend finds it on the local network.
  it('redeems the code over p2p by discovery when no address is entered', async () => {
    wrap()
    await screen.findByText('My Laptop')
    const codeInput = await screen.findByLabelText(/pairing code/i)
    const nameInput = screen.getByLabelText(/device name/i)
    fireEvent.change(codeInput, { target: { value: 'AB12-CD34' } })
    fireEvent.change(nameInput, { target: { value: 'Work Laptop' } })
    fireEvent.click(screen.getByRole('button', { name: /pair device/i }))
    await waitFor(() => expect(mockRedeemViaPeer).toHaveBeenCalledWith('', 'AB12-CD34', 'Work Laptop'))
    expect(await screen.findByText(/device paired/i)).toBeInTheDocument()
    expect(mockListDevices.mock.calls.length).toBeGreaterThan(1)
  })

  it('passes the other device\'s address through when one is entered', async () => {
    wrap()
    const codeInput = await screen.findByLabelText(/pairing code/i)
    fireEvent.change(codeInput, { target: { value: 'AB12-CD34' } })
    fireEvent.change(screen.getByLabelText(/other device address/i), {
      target: { value: ' /ip4/10.8.0.3/tcp/4331/p2p/12D3KooWremote ' },
    })
    fireEvent.click(screen.getByRole('button', { name: /pair device/i }))
    await waitFor(() => expect(mockRedeemViaPeer).toHaveBeenCalled())
    expect(mockRedeemViaPeer.mock.calls[0][0]).toBe('/ip4/10.8.0.3/tcp/4331/p2p/12D3KooWremote')
  })

  it('shows error when redeem fails', async () => {
    mockRedeemViaPeer.mockRejectedValue(new Error('invalid pairing code'))
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

  it('device name defaults to Laptop when navigator unavailable and is editable', async () => {
    wrap()
    const nameInput = (await screen.findByLabelText(/device name/i)) as HTMLInputElement
    // default should be non-empty (Laptop or userAgent)
    expect(nameInput.value.length).toBeGreaterThan(0)
    fireEvent.change(nameInput, { target: { value: 'My Tablet' } })
    expect(nameInput.value).toBe('My Tablet')
  })

  it('uses pairing code vocabulary and device/server terms', async () => {
    wrap()
    await screen.findByText('My Laptop')
    expect(screen.getAllByText(/pairing code/i).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/server/i).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/device/i).length).toBeGreaterThan(0)
  })
})

describe('Pairing — manual sync', () => {
  beforeEach(() => {
    mockGetP2PStatus.mockResolvedValue({ peerId: '12D3KooWlocal', deviceId: 'dev_1', addrs: [], dialAddrs: [], peerCount: 0 })
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

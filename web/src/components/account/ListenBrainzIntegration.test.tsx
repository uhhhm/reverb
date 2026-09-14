import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

const mockGetLinks = vi.fn()
const mockConnect = vi.fn()
const mockDisconnect = vi.fn()

vi.mock('../../lib/scrobbleApi', () => ({
  getLinks: (...args: unknown[]) => mockGetLinks(...args),
  lastfmAuthUrl: vi.fn(),
  lastfmComplete: vi.fn(),
  lastfmDisconnect: vi.fn(),
  listenbrainzConnect: (...args: unknown[]) => mockConnect(...args),
  listenbrainzDisconnect: (...args: unknown[]) => mockDisconnect(...args),
  ScrobbleError: class ScrobbleError extends Error {
    code: string
    constructor(code: string, message: string) {
      super(message)
      this.name = 'ScrobbleError'
      this.code = code
    }
  },
}))

import { IntegrationsSection } from './IntegrationsSection'
import { ScrobbleError } from '../../lib/scrobbleApi'

beforeEach(() => {
  mockGetLinks.mockResolvedValue({ configured: true, links: [] })
  mockConnect.mockResolvedValue({ username: 'lbuser' })
  mockDisconnect.mockResolvedValue(undefined)
})

afterEach(() => vi.clearAllMocks())

function tokenInput() {
  return screen.getByLabelText(/listenbrainz user token/i)
}

describe('ListenBrainz integration', () => {
  it('is off by default and says listens are public', async () => {
    render(<IntegrationsSection />)
    await waitFor(() => expect(tokenInput()).toBeInTheDocument())
    expect(screen.getByText(/listens on listenbrainz are public/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^connect$/i })).toBeDisabled()
    expect(mockConnect).not.toHaveBeenCalled()
  })

  it('connects with a pasted token and never shows it again', async () => {
    render(<IntegrationsSection />)
    await waitFor(() => expect(tokenInput()).toBeInTheDocument())
    expect(tokenInput()).toHaveAttribute('type', 'password')

    fireEvent.change(tokenInput(), { target: { value: '  secret-token  ' } })
    fireEvent.click(screen.getByRole('button', { name: /^connect$/i }))

    await waitFor(() => expect(screen.getByText(/connected as lbuser/i)).toBeInTheDocument())
    expect(mockConnect).toHaveBeenCalledWith('secret-token')
    expect(screen.queryByDisplayValue(/secret-token/)).not.toBeInTheDocument()
  })

  it('explains a rejected token', async () => {
    mockConnect.mockRejectedValue(new ScrobbleError('invalid_token', 'no'))
    render(<IntegrationsSection />)
    await waitFor(() => expect(tokenInput()).toBeInTheDocument())

    fireEvent.change(tokenInput(), { target: { value: 'bad' } })
    fireEvent.click(screen.getByRole('button', { name: /^connect$/i }))

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/didn.t accept that token/i))
  })

  it('disconnects a connected account', async () => {
    mockGetLinks.mockResolvedValue({
      configured: true,
      links: [{ provider: 'listenbrainz', username: 'lbuser', status: 'active' }],
    })
    render(<IntegrationsSection />)
    await waitFor(() => expect(screen.getByText(/connected as lbuser/i)).toBeInTheDocument())

    // Last.fm is not connected here, so the only Disconnect is ListenBrainz's.
    fireEvent.click(screen.getByRole('button', { name: /disconnect/i }))

    await waitFor(() => expect(tokenInput()).toBeInTheDocument())
    expect(mockDisconnect).toHaveBeenCalledTimes(1)
  })
})

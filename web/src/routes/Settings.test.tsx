/**
 * Settings page — Integrations (Last.fm) + Appearance. The profile / security /
 * sessions sections were removed along with the multi-user auth system.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import type { ReactElement } from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { AdapterInstance } from '../lib/adaptersApi'

// ── Scrobble (Last.fm) mock ───────────────────────────────────────────────────
vi.mock('../lib/scrobbleApi', () => ({
  getLinks: vi.fn(async () => ({ configured: true, links: [] })),
  lastfmAuthUrl: vi.fn(async () => ({ authUrl: 'https://last.fm/auth', token: 'tok' })),
  lastfmComplete: vi.fn(async () => ({ username: 'lastfmuser' })),
  lastfmDisconnect: vi.fn(async () => undefined),
  getLastfmConfig: vi.fn(async () => ({ apiKey: '', apiSecretSet: false })),
  setLastfmConfig: vi.fn(async () => undefined),
  ScrobbleError: class ScrobbleError extends Error {
    code: string
    constructor(code: string, message: string) {
      super(message)
      this.name = 'ScrobbleError'
      this.code = code
    }
  },
}))

// ── Settings API mock ─────────────────────────────────────────────────────────
const mockMutate = vi.fn()
const mockUpdateAdapter = vi.fn(() => Promise.resolve({ data: {}, pendingRestart: false }))
const mockUseAdapters = vi.fn(() => ({ data: [] as AdapterInstance[] }))

vi.mock('../lib/settingsApi', () => ({
  useSettings: vi.fn(() => ({
    data: { accentColor: '#F0354B', dynamicBackground: true, libraryBackendMode: 'built-in' },
  })),
  useUpdateSettings: vi.fn(() => ({ mutate: mockMutate })),
  putSettings: vi.fn(() =>
    Promise.resolve({ accentColor: '#F0354B', dynamicBackground: true, libraryBackendMode: 'built-in' }),
  ),
  applyAccent: vi.fn(),
}))

vi.mock('../lib/adaptersApi', () => ({
  useAdapters: () => mockUseAdapters(),
  updateAdapter: (...args: Parameters<typeof mockUpdateAdapter>) => mockUpdateAdapter(...args),
}))

import Settings from './Settings'

function wrap(ui: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>,
  )
}

// ── Tab bar ───────────────────────────────────────────────────────────────────
describe('Settings — tab bar', () => {
  beforeEach(() => {
    mockMutate.mockClear()
  })
  afterEach(() => vi.clearAllMocks())

  it('renders a page heading "Settings"', () => {
    wrap(<Settings />)
    expect(screen.getByRole('heading', { name: /^settings$/i })).toBeInTheDocument()
  })

  it('renders the Integrations and Appearance tab buttons', () => {
    wrap(<Settings />)
    expect(screen.getByRole('button', { name: /^integrations$/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^appearance$/i })).toBeInTheDocument()
  })

  it('defaults to the Integrations tab on first render', () => {
    wrap(<Settings />)
    expect(screen.getByRole('heading', { name: /integrations/i })).toBeInTheDocument()
  })
})

// ── Integrations tab ──────────────────────────────────────────────────────────
describe('Settings — Integrations tab', () => {
  afterEach(() => vi.clearAllMocks())

  it('renders the Integrations heading under the Integrations tab', () => {
    wrap(<Settings />)
    expect(screen.getByRole('heading', { name: /integrations/i })).toBeInTheDocument()
  })

  it('shows the Connect Last.fm button (server configured)', async () => {
    wrap(<Settings />)
    expect(await screen.findByRole('button', { name: /connect last\.fm/i })).toBeInTheDocument()
  })
})

// ── Appearance tab ────────────────────────────────────────────────────────────
describe('Settings — Appearance tab', () => {
  beforeEach(() => {
    mockMutate.mockClear()
  })
  afterEach(() => vi.clearAllMocks())

  function openAppearanceTab() {
    wrap(<Settings />)
    fireEvent.click(screen.getByRole('button', { name: /^appearance$/i }))
  }

  it('shows the accent swatches on the Appearance tab', () => {
    openAppearanceTab()
    expect(screen.getByRole('button', { name: /red \(default\)/i })).toBeInTheDocument()
  })

  it('shows the dynamic background toggle on the Appearance tab', () => {
    openAppearanceTab()
    expect(screen.getByRole('switch', { name: /dynamic album background/i })).toBeInTheDocument()
  })

  it('toggling dynamic background calls useUpdateSettings mutate', async () => {
    openAppearanceTab()
    const toggle = screen.getByRole('switch', { name: /dynamic album background/i })
    fireEvent.click(toggle)
    await waitFor(() => expect(mockMutate).toHaveBeenCalledWith({ dynamicBackground: false }))
  })
})
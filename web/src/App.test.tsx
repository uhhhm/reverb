import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { vi } from 'vitest'
import App from './App'
import { useAuthStore } from './lib/authStore'

vi.mock('./lib/useAlbumPalette', () => ({ useAlbumPalette: () => null }))
vi.mock('./lib/realtimeWiring', () => ({ useRealtime: () => {} }))

// Stub heavy route components so App routing tests don't need API mocks.
vi.mock('./routes/Album', () => ({ default: () => <div>Album page</div> }))
vi.mock('./routes/Artist', () => ({ default: () => <div>Artist page</div> }))
vi.mock('./routes/SyncedPlaylist', () => ({ default: () => <div>SyncedPlaylist page</div> }))
vi.mock('./routes/Admin', () => ({ default: () => <div>Admin page</div> }))
vi.mock('./routes/Home', () => ({ default: () => <div>Home page</div> }))

// Stub refresh() so the boot-hydrate call is a no-op (no network in routing tests).
function seedMe() {
  useAuthStore.setState({
    me: { id: 'u', username: 'u', roleId: 'r', roleName: 'R', isOwner: true, capabilities: ['is_admin'], createdAt: 1700000000 },
    loading: false,
    refresh: async () => {},
  })
}

beforeEach(() => {
  useAuthStore.setState({ me: null, loading: false, refresh: async () => {} })
})

test('renders the app shell regardless of auth (household owner, no login gate)', async () => {
  seedMe()
  render(
    <MemoryRouter initialEntries={['/search']}>
      <App />
    </MemoryRouter>,
  )
  // Routes are lazy-loaded behind Suspense, so await the shell.
  expect(await screen.findByTestId('app-shell-root')).toBeInTheDocument()
})

test('/album/:id redirects to /album/library/:id and renders Album page', async () => {
  seedMe()
  render(
    <MemoryRouter initialEntries={['/album/abc123']}>
      <App />
    </MemoryRouter>,
  )
  expect(await screen.findByText('Album page')).toBeInTheDocument()
})

test('/artist/:id redirects to /artist/library/:id and renders Artist page', async () => {
  seedMe()
  render(
    <MemoryRouter initialEntries={['/artist/xyz456']}>
      <App />
    </MemoryRouter>,
  )
  expect(await screen.findByText('Artist page')).toBeInTheDocument()
})

test('/playlist/:id renders SyncedPlaylist page directly', async () => {
  seedMe()
  render(
    <MemoryRouter initialEntries={['/playlist/p42']}>
      <App />
    </MemoryRouter>,
  )
  expect(await screen.findByText('SyncedPlaylist page')).toBeInTheDocument()
})

test('/synced-playlist/:id redirects to /playlist/:id and renders SyncedPlaylist page', async () => {
  seedMe()
  render(
    <MemoryRouter initialEntries={['/synced-playlist/p42']}>
      <App />
    </MemoryRouter>,
  )
  expect(await screen.findByText('SyncedPlaylist page')).toBeInTheDocument()
})

test('/admin renders the Admin page', async () => {
  seedMe()
  render(
    <MemoryRouter initialEntries={['/admin']}>
      <App />
    </MemoryRouter>,
  )
  expect(await screen.findByText('Admin page')).toBeInTheDocument()
})
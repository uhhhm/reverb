import { test, expect } from '@playwright/test'
import { installApiMocks } from './mocks'
import type { Route } from '@playwright/test'

test('offline set shows empty state when no playlists offline', async ({ page }) => {
  const authed = { value: true }
  await installApiMocks(page, authed)

  await page.route('**/api/v1/sync/status', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ revision: 5, deviceCount: 2 }),
    }),
  )

  await page.route('**/api/v1/offline-set', (route: Route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      })
    }
    return route.continue()
  })

  await page.route('**/api/v1/offline-set/**', (route: Route) => {
    if (route.request().method() === 'PUT') {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ playlistId: 'pl1', enabled: true, updatedAt: Math.floor(Date.now() / 1000) }),
      })
    }
    return route.continue()
  })

  await page.routeWebSocket('**/api/v1/ws', () => {})

  await page.goto('/offline-set')
  await expect(page.getByTestId('app-shell-root')).toBeVisible()
  await expect(page.getByRole('heading', { level: 1, name: 'Offline set' })).toBeVisible()
  await expect(page.getByText('No playlists offline')).toBeVisible()
  const status = page.getByTestId('sync-status')
  await expect(status).toContainText('Revision 5')
  await expect(status).toContainText('2 device(s)')
})

test('offline set toggle updates server', async ({ page }) => {
  const authed = { value: true }
  await installApiMocks(page, authed)

  await page.route('**/api/v1/sync/status', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ revision: 5, deviceCount: 1 }),
    }),
  )

  const offlineSet: { playlistId: string; enabled: boolean; updatedAt: number; playlistName?: string }[] = [
    { playlistId: 'pl1', enabled: true, updatedAt: 1000, playlistName: 'My Playlist' },
  ]

  await page.route('**/api/v1/offline-set', async (route: Route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(offlineSet),
      })
    }
    return route.continue()
  })

  await page.route('**/api/v1/offline-set/**', async (route: Route) => {
    if (route.request().method() === 'PUT') {
      const url = route.request().url()
      const id = decodeURIComponent(url.split('/api/v1/offline-set/')[1] ?? '')
      let body: { enabled?: boolean } = {}
      try {
        body = JSON.parse(route.request().postData() ?? '{}')
      } catch {
        // ignore
      }
      const entry = offlineSet.find((e) => e.playlistId === id)
      if (entry) {
        entry.enabled = !!body.enabled
        entry.updatedAt = Math.floor(Date.now() / 1000)
      } else {
        offlineSet.push({
          playlistId: id,
          enabled: !!body.enabled,
          updatedAt: Math.floor(Date.now() / 1000),
          playlistName: id,
        })
      }
      const updated = offlineSet.find((e) => e.playlistId === id)!
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(updated),
      })
    }
    if (route.request().method() === 'DELETE') {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ ok: true }),
      })
    }
    return route.continue()
  })

  await page.routeWebSocket('**/api/v1/ws', () => {})

  await page.goto('/offline-set')
  await expect(page.getByText('My Playlist')).toBeVisible()
  const toggle = page.getByRole('switch', { name: 'Keep My Playlist offline' })
  await expect(toggle).toHaveAttribute('aria-checked', 'true')

  const putPromise = page.waitForResponse(
    (r) => r.url().includes('/api/v1/offline-set/') && r.request().method() === 'PUT',
  )
  await toggle.click()
  await putPromise

  // After toggle, switch should be off (state flipped to false and re-fetched)
  await expect(toggle).toHaveAttribute('aria-checked', 'false')
})

test('SyncedPlaylist keeps offline toggle', async ({ page }) => {
  const authed = { value: true }
  await installApiMocks(page, authed)

  await page.route('**/api/v1/sync/status', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ revision: 1, deviceCount: 1 }),
    }),
  )

  const offlineSet2: { playlistId: string; enabled: boolean; updatedAt: number; playlistName?: string }[] = [
    { playlistId: 'pl1', enabled: false, updatedAt: 1000, playlistName: 'My Mix' },
  ]

  await page.route('**/api/v1/offline-set', async (route: Route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(offlineSet2),
      })
    }
    return route.continue()
  })

  await page.route('**/api/v1/offline-set/**', async (route: Route) => {
    if (route.request().method() === 'PUT') {
      const url = route.request().url()
      const id = decodeURIComponent(url.split('/api/v1/offline-set/')[1] ?? '')
      let body: { enabled?: boolean } = {}
      try {
        body = JSON.parse(route.request().postData() ?? '{}')
      } catch {
        // ignore
      }
      const entry = offlineSet2.find((e) => e.playlistId === id)
      if (entry) {
        entry.enabled = !!body.enabled
      } else {
        offlineSet2.push({
          playlistId: id,
          enabled: !!body.enabled,
          updatedAt: Math.floor(Date.now() / 1000),
          playlistName: id,
        })
      }
      const updated = offlineSet2.find((e) => e.playlistId === id)!
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(updated),
      })
    }
    return route.continue()
  })

  const detail = {
    id: 'pl1',
    source: 'spotify',
    externalId: 'ext-pl-1',
    name: 'My Mix',
    coverUrl: '',
    syncEnabled: false,
    syncIntervalSec: 0,
    autoDownload: false,
    lastSyncedAt: 0,
    trackCount: 1,
    mode: 'once',
    ownedCount: 1,
    totalCount: 1,
    tracks: [
      {
        state: 'full',
        libraryTrack: {
          id: 't1',
          title: 'Owned Song',
          artist: 'Artist A',
          album: 'My Mix',
          albumId: '',
          artistId: '',
          coverArtId: '',
          trackNumber: 1,
          discNumber: 1,
          durationMs: 200000,
          bitRate: 0,
          suffix: '',
          contentType: '',
        },
        title: 'Owned Song',
        artist: 'Artist A',
        trackNumber: 1,
        durationMs: 200000,
        key: { source: 'spotify', externalId: 'e1' },
      },
    ],
  }

  await page.route('**/api/v1/playlists/pl1', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(detail) }),
  )

  // Also handle generic playlists list (not used here but avoid 404)
  await page.route('**/api/v1/playlists', (route: Route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([{ id: 'pl1', name: 'My Mix' }]),
      })
    }
    return route.continue()
  })

  await page.routeWebSocket('**/api/v1/ws', () => {})

  await page.goto('/playlist/pl1')
  await expect(page.getByTestId('app-shell-root')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'My Mix' })).toBeVisible()

  const offlineToggle = page.getByRole('switch', { name: 'Keep offline' })
  await expect(offlineToggle).toBeVisible()
  await expect(offlineToggle).toHaveAttribute('aria-checked', 'false')

  const putPromise = page.waitForResponse(
    (r) => r.url().includes('/api/v1/offline-set/pl1') && r.request().method() === 'PUT',
  )
  await offlineToggle.click()
  await putPromise

  await expect(offlineToggle).toHaveAttribute('aria-checked', 'true')
  // Helper text present
  await expect(page.getByText(/Removing from offline set does not delete the playlist/)).toBeVisible()
})

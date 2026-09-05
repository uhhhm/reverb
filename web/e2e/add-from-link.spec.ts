import { test, expect } from '@playwright/test'
import { installApiMocks } from './mocks'
import type { Route } from '@playwright/test'

test('add from link: resolve URL and add to canonical library', async ({ page }) => {
  const authed = { value: true }
  await installApiMocks(page, authed)

  await page.route('**/api/v1/playlists', (route: Route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      })
    }
    return route.continue()
  })

  await page.route('**/api/v1/links/resolve', (route: Route) => {
    if (route.request().method() === 'POST') {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          kind: 'track',
          source: 'spotify',
          externalId: '123',
          title: 'T',
          artist: 'A',
          album: 'Al',
          url: 'https://open.spotify.com/track/123',
          coverUrl: '',
        }),
      })
    }
    return route.continue()
  })

  await page.route('**/api/v1/links/add-batch', (route: Route) => {
    expect(route.request().postDataJSON()).toMatchObject({
      items: [{ url: 'https://open.spotify.com/track/123', download: true }],
    })
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ results: [{ url: 'https://open.spotify.com/track/123', catalogId: 'trk_link_123' }] }),
    })
  })

  await page.routeWebSocket('**/api/v1/ws', () => {})

  await page.goto('/add-from-link')
  await expect(page.getByTestId('app-shell-root')).toBeVisible()
  await expect(page.getByRole('heading', { level: 1, name: 'Add from link' })).toBeVisible()

  const input = page.getByRole('textbox', { name: 'Spotify or YouTube URLs' })
  await expect(input).toBeVisible()
  await input.fill('https://open.spotify.com/track/123')

  // Download toggle default checked
  const downloadNow = page.getByLabel('Download now')
  await expect(downloadNow).toBeChecked()

  await page.getByRole('button', { name: 'Resolve' }).click()

  await expect(page.getByTestId('preview-card')).toBeVisible()
  await expect(page.getByTestId('preview-card')).toContainText('T')
  await expect(page.getByTestId('preview-card')).toContainText('A')
  await expect(page.getByTestId('preview-card')).toContainText('Al')
  await expect(page.getByTestId('preview-card')).toContainText('spotify')
  await expect(page.getByTestId('preview-card')).toContainText('track')

  // Add from link
  await page.getByRole('main').getByRole('button', { name: 'Add from link', exact: true }).click()

  // Success reports the completed batch.
  await expect(page.getByText('Added 1 link to your library.')).toBeVisible()
})

test('add from link: shows cover and playlist helper', async ({ page }) => {
  const authed = { value: true }
  await installApiMocks(page, authed)

  await page.route('**/api/v1/playlists', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) }),
  )

  await page.route('**/api/v1/links/resolve', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        kind: 'track',
        source: 'spotify',
        externalId: '123',
        title: 'T',
        artist: 'A',
        album: 'Al',
        url: 'https://open.spotify.com/track/123',
      }),
    }),
  )

  await page.route('**/api/v1/links/add', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        resolve: {
          kind: 'track',
          source: 'spotify',
          externalId: '123',
          title: 'T',
          artist: 'A',
          album: 'Al',
          url: 'https://open.spotify.com/track/123',
        },
        catalogId: 'trk_link_123',
      }),
    }),
  )

  await page.routeWebSocket('**/api/v1/ws', () => {})

  await page.goto('/add-from-link')
  await expect(page.getByTestId('app-shell-root')).toBeVisible()

  await expect(page.getByLabel('Add to playlist')).toBeVisible()
  await expect(page.getByText(/Choose a playlist or add to your canonical library/)).toBeVisible()
  await expect(page.getByText(/One link per line/)).toBeVisible()
  await expect(page.getByText(/source-native/).first()).toBeVisible()
})

import { test, expect } from '@playwright/test'
import { installApiMocks } from './mocks'
import type { Route } from '@playwright/test'

test('pairing: generate a pairing code and redeem one against the issuing peer', async ({ page }) => {
  const authed = { value: true }
  await installApiMocks(page, authed)

  await page.route('**/api/v1/sync/status', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ revision: 1, deviceCount: 1 }),
    }),
  )

  await page.route('**/api/v1/pairing/devices', (route: Route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      })
    }
    return route.continue()
  })

  await page.route('**/api/v1/pairing/devices/**', (route: Route) => {
    if (route.request().method() === 'DELETE') {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ ok: true }),
      })
    }
    return route.continue()
  })

  await page.route('**/api/v1/pairing/code', (route: Route) => {
    if (route.request().method() === 'POST') {
      const expiresAt = Math.floor(Date.now() / 1000) + 600
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ code: 'ABCD-1234', expiresAt }),
      })
    }
    return route.continue()
  })

  // Pairing happens over libp2p against the device that issued the code, not
  // against this device's own HTTP API — the code lives only in the issuer's
  // database. /p2p/pair/redeem is the endpoint the page actually calls.
  await page.route('**/api/v1/p2p/pair/redeem', (route: Route) => {
    if (route.request().method() === 'POST') {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ deviceId: 'dev_123', token: 'abc' }),
      })
    }
    return route.continue()
  })

  await page.route('**/api/v1/p2p/status', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        peerId: 'peer_self',
        deviceId: 'dev_self',
        addrs: [],
        dialAddrs: ['/ip4/127.0.0.1/tcp/4331/p2p/peer_self'],
        peerCount: 0,
        vector: {},
        hlc: 0,
      }),
    }),
  )

  await page.routeWebSocket('**/api/v1/ws', () => {})

  await page.goto('/pairing')
  await expect(page.getByTestId('app-shell-root')).toBeVisible()
  await expect(page.getByRole('heading', { level: 1, name: 'Pairing' })).toBeVisible()

  // No devices yet
  await expect(page.getByText(/No devices paired yet/)).toBeVisible()

  // Generate pairing code
  await page.getByRole('button', { name: 'Generate pairing code' }).click()
  await expect(page.getByTestId('pairing-code')).toHaveText('ABCD-1234')
  await expect(page.getByText(/Code expires in/)).toBeVisible()

  // Fill redeem form and pair (use textbox role to avoid matching Copy button)
  await page.getByRole('textbox', { name: 'Pairing code' }).fill('ABCD-1234')
  // Device name defaults to navigator userAgent slice; ensure it has a value
  const deviceNameInput = page.getByRole('textbox', { name: 'Device name' })
  await expect(deviceNameInput).not.toBeEmpty()
  // Overwrite with a deterministic name
  await deviceNameInput.fill('My Device')
  const redeemed = page.waitForRequest(
    (req) => req.url().includes('/api/v1/p2p/pair/redeem') && req.method() === 'POST',
  )
  await page.getByRole('button', { name: 'Pair device' }).click()

  // The page offers the code to the peer, naming this device.
  const request = await redeemed
  expect(request.postDataJSON()).toEqual({ peerId: '', code: 'ABCD-1234', deviceName: 'My Device' })

  // UI shows paired success and clears the code so it cannot be redeemed twice.
  await expect(page.getByText(/Device paired/)).toBeVisible()
  await expect(page.getByRole('textbox', { name: 'Pairing code' })).toBeEmpty()
})

test('pairing: pairing code input formats with dash', async ({ page }) => {
  const authed = { value: true }
  await installApiMocks(page, authed)

  await page.route('**/api/v1/sync/status', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ revision: 1, deviceCount: 0 }),
    }),
  )
  await page.route('**/api/v1/pairing/devices', (route: Route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) }),
  )
  await page.route('**/api/v1/pairing/code', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ code: 'ABCD-1234', expiresAt: Math.floor(Date.now() / 1000) + 600 }),
    }),
  )
  await page.route('**/api/v1/p2p/pair/redeem', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ deviceId: 'dev_123', token: 'abc' }),
    }),
  )
  await page.routeWebSocket('**/api/v1/ws', () => {})

  await page.goto('/pairing')
  await expect(page.getByTestId('app-shell-root')).toBeVisible()

  const input = page.getByRole('textbox', { name: 'Pairing code' })
  await input.fill('abcd1234')
  await expect(input).toHaveValue('ABCD-1234')
})

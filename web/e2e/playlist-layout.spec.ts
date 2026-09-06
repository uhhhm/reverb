import { test, expect } from '@playwright/test'
import { installApiMocks, installPlaylistSyncMocks, installPlaylistSyncWsMock } from './mocks'

test('playlist search and sorting update visible rows', async ({ page }) => {
  await installApiMocks(page, { value: true })
  await installPlaylistSyncMocks(page)
  await installPlaylistSyncWsMock(page)
  await page.goto('/playlist/sp1')
  await expect(page.getByRole('heading', { name: 'My Mix' })).toBeVisible()
  await page.getByLabel('Search in playlist').fill('Missing')
  await expect(page.getByText('Synced Missing Song', { exact: true })).toBeVisible()
  await expect(page.getByText('Synced Owned Song', { exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Clear playlist search' }).click()
  await page.getByLabel('Sort playlist').selectOption('title')
  const rows = page.locator('[draggable]')
  await expect(rows.first()).toContainText('Synced Missing Song')
  await expect(rows.last()).toContainText('Synced Owned Song')
  await page.getByLabel('Search in playlist').fill('does not exist')
  await expect(page.getByText('No matching songs', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Play My Mix', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: 'Clear search', exact: true }).click()
  await expect(rows).toHaveCount(2)
})

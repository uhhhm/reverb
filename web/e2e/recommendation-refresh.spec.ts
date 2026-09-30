import { test, expect, type Page } from '@playwright/test'
import { installApiMocks } from './mocks'
import type { components } from '../src/lib/generated/api'

type Schemas = components['schemas']

// Navigate inside the same document, as browser history does. A new document
// would discard QueryClient and conceal the cached-view regression.
async function visit(page: Page, path: string) {
  await page.evaluate((url) => {
    window.history.pushState({}, '', url)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, path)
  await expect(page).toHaveURL(path)
}

test('marks, undo and settings refresh warmed recommendation views without reloading', async ({ page }, testInfo) => {
  test.setTimeout(90_000)
  await installApiMocks(page, { value: true })
  await page.routeWebSocket('**/api/v1/ws', () => {})
  let documents = 0
  page.on('request', (request) => { if (request.resourceType() === 'document') documents++ })
  const requests: { method: string; path: string; body: unknown }[] = []
  let failNextMark = false
  let marks: Schemas['NotInterestedMark'][] = []
  let settings: Schemas['RecommendationSettings'] = { adventurousness: 50, onlineRecommendations: true }
  const songs: Schemas['RecommendedTrack'][] = [
    { source: 'library', externalId: 'marked-song', title: 'Marked Song', artist: 'Track Artist', album: '', durationMs: 180000, type: 'track' },
    { source: 'library', externalId: 'artist-song', title: 'Artist Song', artist: 'Marked Artist', album: '', durationMs: 190000, type: 'track' },
    { source: 'library', externalId: 'safe-song', title: 'Safe Song', artist: 'Safe Artist', album: '', durationMs: 200000, type: 'track' },
  ]
  const artists: Schemas['ExternalArtist'][] = [
    { source: 'library', externalId: 'marked-artist', name: 'Marked Artist' },
    { source: 'library', externalId: 'safe-artist', name: 'Safe Artist' },
  ]
  const tracks = () => settings.onlineRecommendations ? [
    ...songs.filter((s) => !marks.some((m) => m.kind === 'track' ? m.title === s.title : m.artist === s.artist)),
    ...(settings.adventurousness === 80 ? [{ ...songs[2], externalId: 'new-song', title: 'New Discovery' }] : []),
  ] : []
  const suggestedArtists = () => settings.onlineRecommendations
    ? artists.filter((a) => !marks.some((m) => m.kind === 'artist' && m.artist === a.name)) : []
  const mix = (kind: Schemas['MixKind']): Schemas['Mix'] => ({
    kind, period: '2026-09-28', tracks: tracks(), available: settings.onlineRecommendations, refreshing: false,
  })
  const playlist: Schemas['SyncedPlaylistDetail'] = {
    id: 'cache-playlist', source: 'local', externalId: '', name: 'Cache playlist', mode: 'once',
    syncEnabled: false, syncIntervalSec: 0, autoDownload: false, lastSyncedAt: 0,
    trackCount: 0, ownedCount: 0, totalCount: 0, tracks: [],
  }
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace('/api/v1', '')
    const method = req.method()
    requests.push({ method, path: path + url.search, body: req.postDataJSON() })
    let body: unknown
    if (path === '/not-interested') {
      if (method === 'POST') {
        if (failNextMark) {
          failNextMark = false
          return route.fulfill({ status: 500, json: { error: 'fixture rejects this mark' } })
        }
        const input = req.postDataJSON() as Schemas['NotInterestedRequest']
        const mark: Schemas['NotInterestedMark'] = {
          key: input.kind === 'track' ? `track:${input.trackId}` : `artist:${input.id}`,
          kind: input.kind, title: input.title, artist: input.name ?? input.artist ?? '', markedAt: 1,
        }
        marks = [...marks, mark]
        body = mark
      } else if (method === 'DELETE') {
        marks = marks.filter((m) => m.key !== (req.postDataJSON() as { key: string }).key)
        body = null
      } else body = { marks } satisfies Schemas['NotInterestedList']
    } else if (path === '/recommendations/settings') {
      if (method === 'PUT') settings = { ...settings, ...req.postDataJSON() as Schemas['RecommendationSettingsPatch'] }
      body = settings
    } else if (path === '/recommendations/shelves') {
      body = { refreshing: false, shelves: [
        { kind: 'similarTo', seed: { artist: 'Seed Artist', title: 'Seed Song' }, tracks: tracks(), artists: [] },
        { kind: 'artistsYouMightLike', tracks: [], artists: suggestedArtists() },
      ] } satisfies Schemas['HomeShelves']
    } else if (path === '/recommendations/mixes') {
      body = { mixes: [mix('discoverWeekly'), mix('releaseRadar')] } satisfies Schemas['MixList']
    } else if (path === '/recommendations/mixes/discoverWeekly') body = mix('discoverWeekly')
    else if (path === '/recommendations/mixes/releaseRadar') body = mix('releaseRadar')
    else if (path.startsWith('/recommendations/artists/')) {
      body = { available: settings.onlineRecommendations, artists: suggestedArtists() } satisfies Schemas['SimilarArtists']
    } else if (path === '/recommendations/similar-tracks' || path.startsWith('/recommendations/playlists/')) {
      body = { available: settings.onlineRecommendations, tracks: tracks() } satisfies Schemas['SimilarTracks']
    } else if (path === '/playlists') body = [playlist]
    else if (path === '/playlists/cache-playlist') body = playlist
    else if (path === '/offline-set' || path === '/library/artists' || path === '/library/albums') body = []
    else if (path === '/artist/library/marked-artist') body = { source: 'library', id: 'marked-artist', name: 'Marked Artist', resolved: false, albums: [] }
    else if (path === '/settings') body = { accentColor: '#F0354B', dynamicBackground: false }
    else return route.fallback()
    return route.fulfill({ status: 200, json: body })
  })

  const main = page.getByRole('main')
  const similarPath = '/similar-tracks?artist=Seed+Artist&title=Seed+Song'
  const surfaces = ['/mix/discoverWeekly', '/mix/releaseRadar', '/playlist/cache-playlist', similarPath]
  const reads = () => requests.filter((r) => r.method === 'GET' && r.path.startsWith('/recommendations/')).map((r) => r.path)

  await page.goto('/')
  await expect(page.getByRole('region', { name: 'For you' }).getByText('Marked Song', { exact: true })).toBeVisible()
  await expect(main.getByText('Discover Weekly', { exact: true })).toBeVisible()
  for (const path of surfaces) {
    await visit(page, path)
    await expect(main.getByText('Marked Song', { exact: true })).toBeVisible()
  }
  await visit(page, '/artist/library/marked-artist')
  await expect(main.getByText('Safe Artist', { exact: true })).toBeVisible()
  // Warm the marks list too, so successful mutations must expire it.
  await visit(page, '/settings')
  await main.getByRole('button', { name: 'Not interested', exact: true }).click()
  await expect(main.getByText('Nothing marked.', { exact: false })).toBeVisible()

  await visit(page, '/mix/discoverWeekly')
  const beforeMark = reads().length
  await main.getByRole('button', { name: 'More actions for Marked Song', exact: true }).click()
  await page.getByRole('menuitem', { name: /^Not interested/ }).click()
  await expect(main.getByText('Marked Song', { exact: true })).toHaveCount(0)
  expect(reads().slice(beforeMark)).toContain('/recommendations/mixes/discoverWeekly')

  await visit(page, '/artist/library/marked-artist')
  await main.getByRole('button', { name: 'Not interested', exact: true }).click()
  await expect(main.getByRole('button', { name: 'Marked not interested', exact: true })).toBeVisible()
  await expect(main.getByText('Safe Artist', { exact: true })).toBeVisible()
  // The marked artist disappears from similar artists while its detail stays.
  await expect(main.getByText('Marked Artist', { exact: true })).toHaveCount(1)

  const revisitStart = reads().length
  for (const path of surfaces) {
    await visit(page, path)
    await expect(main.getByText('Safe Song', { exact: true })).toBeVisible()
    await expect(main.getByText('Marked Song', { exact: true })).toHaveCount(0)
    await expect(main.getByText('Artist Song', { exact: true })).toHaveCount(0)
  }
  await visit(page, '/')
  await expect(page.getByRole('region', { name: 'For you' }).getByText('Safe Song', { exact: true })).toBeVisible()
  await expect(main.getByText('Marked Song', { exact: true })).toHaveCount(0)
  await expect(main.getByText('Artist Song', { exact: true })).toHaveCount(0)
  await expect(main.getByText('Marked Artist', { exact: true })).toHaveCount(0)
  await expect(main.getByText('1 songs', { exact: true })).toHaveCount(2)
  const revisits = reads().slice(revisitStart)
  for (const path of ['/recommendations/mixes/discoverWeekly', '/recommendations/mixes/releaseRadar',
    '/recommendations/playlists/cache-playlist/suggestions?page=0', '/recommendations/similar-tracks?artist=Seed+Artist&title=Seed+Song',
    '/recommendations/shelves', '/recommendations/mixes']) expect(revisits).toContain(path)

  // A server rejection shows the existing error and leaves visible data alone.
  await visit(page, '/mix/discoverWeekly')
  failNextMark = true
  const failedStart = requests.length
  await main.getByRole('button', { name: 'More actions for Safe Song', exact: true }).click()
  await page.getByRole('menuitem', { name: /^Not interested/ }).click()
  await expect(page.getByText('Could not mark that Not interested', { exact: true })).toBeVisible()
  await expect(main.getByText('Safe Song', { exact: true })).toBeVisible()
  expect(requests.slice(failedStart).filter((r) => r.method === 'GET')).toEqual([])

  await visit(page, '/settings')
  await main.getByRole('button', { name: 'Not interested', exact: true }).click()
  await expect(main.getByRole('button', { name: 'Undo not interested in Marked Artist' })).toBeVisible()
  await main.getByRole('button', { name: 'Undo not interested in Marked Song' }).click()
  await expect(main.getByRole('button', { name: 'Undo not interested in Marked Song' })).toHaveCount(0)
  for (const path of surfaces) {
    await visit(page, path)
    await expect(main.getByText('Marked Song', { exact: true })).toBeVisible()
    await expect(main.getByText('Artist Song', { exact: true })).toHaveCount(0)
  }

  await visit(page, '/settings')
  await main.getByRole('button', { name: 'Recommendations', exact: true }).click()
  const slider = main.getByRole('slider', { name: 'Adventurousness' })
  await slider.focus()
  await slider.press('End')
  for (let i = 0; i < 4; i++) await slider.press('ArrowLeft')
  await expect(slider).toHaveValue('80')
  await expect.poll(() => settings.adventurousness).toBe(80)
  for (const path of surfaces) {
    await visit(page, path)
    await expect(main.getByText('New Discovery', { exact: true })).toBeVisible()
  }
  await visit(page, '/')
  await expect(main.getByText('New Discovery', { exact: true })).toBeVisible()
  await expect(main.getByText('3 songs', { exact: true })).toHaveCount(2)

  await visit(page, '/settings')
  await main.getByRole('button', { name: 'Recommendations', exact: true }).click()
  await main.getByRole('switch', { name: 'Online recommendations' }).click()
  await expect(main.getByRole('switch', { name: 'Online recommendations' })).toHaveAttribute('aria-checked', 'false')
  for (const path of surfaces) {
    await visit(page, path)
    await expect(main.getByText('New Discovery', { exact: true })).toHaveCount(0)
    await expect(main.getByText('Marked Song', { exact: true })).toHaveCount(0)
  }
  await visit(page, similarPath)
  await expect(main.getByText("Similar tracks aren't available", { exact: true })).toBeVisible()
  await visit(page, '/')
  await expect(main.getByText('Discover Weekly', { exact: true })).toHaveCount(0)
  await expect(main.getByText('New Discovery', { exact: true })).toHaveCount(0)

  expect(documents).toBe(1)
  expect(requests.filter((r) => r.method !== 'GET' &&
    (r.path.startsWith('/recommendations/') || r.path.startsWith('/player/') || r.path.startsWith('/playlists/')))
    .every((r) => r.method === 'PUT' && r.path === '/recommendations/settings')).toBe(true)
  await testInfo.attach('recommendation-requests', { body: JSON.stringify(requests, null, 2), contentType: 'application/json' })
  await page.screenshot({ path: testInfo.outputPath('recommendation-refresh.png'), fullPage: true })
})

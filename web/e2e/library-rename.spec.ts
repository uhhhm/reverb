import { test, expect, type Page, type WebSocketRoute } from '@playwright/test'
import { installApiMocks } from './mocks'
import type { Track, AlbumDetailTrack } from '../src/lib/types'

async function visit(page: Page, path: string) {
  await page.evaluate((url) => {
    window.history.pushState({}, '', url)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, path)
  await expect(page).toHaveURL(path)
}

test('individual, partial batch and incoming renames refresh warmed library views', async ({ page }, info) => {
  test.setTimeout(90_000)
  page.setDefaultTimeout(10_000)
  await installApiMocks(page, { value: true })
  let socket: WebSocketRoute | undefined
  await page.routeWebSocket('**/api/v1/ws', (ws) => { socket = ws })
  let documents = 0
  page.on('request', (req) => { if (req.resourceType() === 'document') documents++ })
  const requests: { method: string; path: string; body: unknown }[] = []
  const originals: Track[] = ['One', 'Two'].map((title, i) => ({
    id: `song-${i}`, title: `Song ${title}`, artist: 'Original Artist', artistId: 'artist',
    album: 'Original Album', albumId: 'album', coverArtId: '', durationMs: 180000,
    trackNumber: i + 1, discNumber: 1, bitRate: 320, suffix: 'mp3', contentType: 'audio/mpeg',
  }))
  let artistName = ''
  let albumName = ''
  const edits: Record<string, Partial<Track>> = {}
  let partial = false
  let fail = false
  const songs = () => originals.map((t) => ({ ...t, artist: artistName || t.artist, album: albumName || t.album, ...edits[t.id] }))
  const rows = (): AlbumDetailTrack[] => songs().map((t) => ({
    state: 'full', title: t.title, artist: t.artist, album: t.album,
    trackNumber: t.trackNumber, durationMs: t.durationMs, libraryTrack: t,
  }))
  const album = () => ({ id: 'album', name: albumName || 'Original Album', artist: artistName || 'Original Artist', artistId: 'artist', year: 2025, songCount: 2, durationMs: 360000, coverArtId: '' })
  const playlist = () => ({ id: 'playlist', name: 'Rename playlist', source: 'local', externalId: '',
    mode: 'once', syncEnabled: false, syncIntervalSec: 0, autoDownload: false,
    lastSyncedAt: 0, trackCount: 2, ownedCount: 2, totalCount: 2, tracks: rows() })
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace('/api/v1', '')
    const method = req.method()
    requests.push({ method, path: path + url.search, body: req.postDataJSON() })
    let body: unknown
    if (method !== 'GET' && path.includes('/name')) {
      if (fail) { fail = false; return route.fulfill({ status: 500, json: { error: 'fixture rejected rename' } }) }
      const input = req.postDataJSON() as { name?: string; title?: string; artist?: string }
      if (path.startsWith('/library/album/')) albumName = input.name ?? ''
      else if (path.startsWith('/library/artist/')) artistName = input.name ?? ''
      else edits['song-0'] = { ...(input.title ? { title: input.title } : {}), ...(input.artist ? { artist: input.artist } : {}) }
      body = input
    } else if (path === '/library/rename/batch') {
      const input = req.postDataJSON() as { tracks?: { id: string; title?: string }[]; albums?: { id: string; name: string }[]; artists?: { id: string; name: string }[] }
      let applied = 0
      for (const item of input.tracks ?? []) {
        if (partial && item.id === 'song-1') continue
        edits[item.id] = { ...edits[item.id], title: item.title }
        applied++
      }
      for (const item of input.albums ?? []) { albumName = item.name; applied++ }
      for (const item of input.artists ?? []) { artistName = item.name; applied++ }
      body = { applied, ...(partial ? { errors: { 'song-1': 'fixture rejects second song' } } : {}) }
      partial = false
    } else if (path === '/library/songs') body = songs()
    else if (path === '/library/artists') body = [{ id: 'artist', name: artistName || 'Original Artist', albumCount: 1, coverArtId: '' }]
    else if (path === '/library/albums') body = [album()]
    else if (path === '/artist/library/artist') body = { source: 'library', id: 'artist', name: artistName || 'Original Artist', resolved: false,
      albums: [{ source: 'library', externalId: 'album', name: album().name, year: 2025, kind: 'album', totalTracks: 2, libraryAlbumId: 'album' }] }
    else if (path === '/album/library/album') body = { ...album(), source: 'library', libraryAlbumId: 'album', ownedCount: 2, totalCount: 2, tracks: rows() }
    else if (path === '/playlists') body = [playlist()]
    else if (path === '/playlists/playlist') body = playlist()
    else if (path === '/offline-set') body = []
    else if (path.endsWith('/coverage')) return route.fulfill({ status: 204 })
    else return route.fallback()
    return route.fulfill({ status: 200, json: body })
  })
  const main = page.getByRole('main')
  const surfaces = ['/library', '/album/library/album', '/playlist/playlist']
  async function verifySongs(first: string, second = 'Song Two', artist = artistName || 'Original Artist', albumTitle = albumName || 'Original Album') {
    for (const path of surfaces) {
      await visit(page, path)
      await expect(main.getByText(first, { exact: true })).toBeVisible()
      await expect(main.getByText(second, { exact: true })).toBeVisible()
      await expect(main.getByText(artist, { exact: true }).first()).toBeVisible()
      if (path !== '/playlist/playlist') await expect(main.getByText(albumTitle, { exact: true }).first()).toBeVisible()
    }
    await visit(page, '/artist/library/artist')
    await expect(main.getByRole('heading', { name: artist, exact: true })).toBeVisible()
    await expect(main.getByText(albumTitle, { exact: true })).toBeVisible()
  }
  async function renameEntity(name: string) {
    await main.getByRole('button', { name: /^Rename / }).click()
    const dialog = page.getByTestId('rename-entity-dialog')
    await dialog.getByLabel('Name', { exact: true }).fill(name)
    await dialog.getByRole('button', { name: 'Save', exact: true }).click()
    await expect(dialog).toHaveCount(0)
  }
  await page.goto('/library')
  await verifySongs('Song One')
  // Artist detail is active and the other three views are warm but inactive.
  await renameEntity('Renamed Artist')
  await expect(main.getByRole('heading', { name: 'Renamed Artist' })).toBeVisible()
  await verifySongs('Song One')
  await visit(page, '/album/library/album')
  await renameEntity('Renamed Album')
  await verifySongs('Song One')
  await visit(page, '/library')
  await main.getByRole('button', { name: 'More actions for Song One', exact: true }).click()
  await page.getByRole('menuitem', { name: /^Edit details/ }).click()
  const trackDialog = page.getByRole('dialog')
  await trackDialog.getByLabel('Title', { exact: true }).fill('Solo One')
  await trackDialog.getByRole('button', { name: 'Save name' }).click()
  await expect(trackDialog).toHaveCount(0)
  await verifySongs('Solo One')

  // A rejected write must leave both the dialog error and truthful old names.
  await visit(page, '/album/library/album')
  fail = true
  await main.getByRole('button', { name: 'Rename Renamed Album' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Rejected Album')
  await page.getByRole('dialog').getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByRole('alert')).toHaveText('PUT /library/album/album/name -> 500')
  await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click()
  await verifySongs('Solo One')

  async function batch(tab: 'Tracks' | 'Albums' | 'Artists', find: string, replacement: string, names: string[], shouldFail = false) {
    await visit(page, '/manage-tracks')
    await main.getByRole('button', { name: tab, exact: true }).click()
    for (const name of names) {
      if (tab === 'Tracks') await main.getByRole('checkbox', { name: `Select ${name}`, exact: true }).check()
      else await main.getByRole('button', { name: `Select ${name}`, exact: true }).click()
    }
    await main.getByRole('button', { name: 'Rename…', exact: true }).click()
    const dialog = page.getByTestId('batch-rename-dialog')
    await dialog.getByRole('checkbox', { name: 'Regular expression', exact: true }).check()
    await dialog.getByLabel('Find', { exact: true }).fill(find)
    await dialog.getByLabel('Replace with', { exact: true }).fill(replacement)
    await dialog.getByRole('button', { name: /^Apply/ }).click()
    if (shouldFail) {
      await expect(dialog.getByRole('alert')).toContainText('1 saved. Failed renames: song-1: fixture rejects second song')
      await expect(main.getByRole('checkbox', { name: 'Select Batch One', exact: true })).toBeVisible()
      await expect(main.getByRole('checkbox', { name: 'Select Song Two', exact: true })).toBeVisible()
      await dialog.getByRole('button', { name: 'Cancel' }).click()
    } else await expect(dialog).toHaveCount(0)
  }
  partial = true
  await batch('Tracks', '^(Solo|Song) ', 'Batch ', ['Solo One', 'Song Two'], true)
  await verifySongs('Batch One')
  await batch('Tracks', '^Song ', 'Batch ', ['Song Two'])
  await verifySongs('Batch One', 'Batch Two')

  // Clear a per-track override so later artist edits can cascade again.
  await visit(page, '/library')
  await main.getByRole('button', { name: 'More actions for Batch One', exact: true }).click()
  await page.getByRole('menuitem', { name: /^Edit details/ }).click()
  await expect(page.getByRole('dialog').getByLabel('Title', { exact: true })).toHaveValue('Batch One')
  await expect(page.getByRole('dialog').getByLabel('Artist', { exact: true })).toHaveValue('Renamed Artist')
  await page.getByRole('dialog').getByLabel('Title', { exact: true }).fill('')
  await page.getByRole('dialog').getByLabel('Artist', { exact: true }).fill('')
  await page.getByRole('dialog').getByRole('button', { name: 'Save name' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await verifySongs('Song One', 'Batch Two')
  await batch('Albums', '^Renamed', 'Batch', ['Renamed Album'])
  await verifySongs('Song One', 'Batch Two')
  await batch('Artists', '^Renamed', 'Batch', ['Renamed Artist'])
  await verifySongs('Song One', 'Batch Two')

  await visit(page, '/album/library/album')
  await renameEntity('')
  await verifySongs('Song One', 'Batch Two', 'Batch Artist', 'Original Album')

  // Simulate an accepted edit on another device and deliver the existing event.
  await expect.poll(() => !!socket).toBe(true)
  artistName = 'Incoming Artist'
  albumName = 'Incoming Album'
  edits['song-0'] = { title: 'Incoming One' }
  const incomingStart = requests.length
  socket!.send(JSON.stringify({ type: 'library.updated', payload: { artistIds: ['artist'], albumIds: ['album'] } }))
  await expect(main.getByRole('heading', { name: 'Incoming Artist' })).toBeVisible()
  await verifySongs('Incoming One', 'Batch Two')
  const reads = requests.slice(incomingStart).filter((r) => r.method === 'GET').map((r) => r.path)
  for (const path of ['/library/songs', '/artist/library/artist', '/album/library/album', '/playlists/playlist']) expect(reads).toContain(path)
  expect(documents).toBe(1)
  await info.attach('rename-requests', { body: JSON.stringify(requests, null, 2), contentType: 'application/json' })
  await page.screenshot({ path: info.outputPath('library-rename.png'), fullPage: true })
})

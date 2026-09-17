import { test, expect, type Page } from '@playwright/test'
import { installApiMocks, installWsMock } from './mocks'
import type { Track } from '../src/lib/types'

// PCM silence exercises native decoding, media clocks, seeks, and event order
// without a live music service or sound from the test runner.
function wav(seconds: number): Buffer {
  const samples = Math.max(1, Math.round(seconds * 8000))
  const data = Buffer.alloc(44 + samples * 2)
  data.write('RIFF', 0)
  data.writeUInt32LE(data.length - 8, 4)
  data.write('WAVEfmt ', 8)
  data.writeUInt32LE(16, 16)
  data.writeUInt16LE(1, 20)
  data.writeUInt16LE(1, 22)
  data.writeUInt32LE(8000, 24)
  data.writeUInt32LE(16000, 28)
  data.writeUInt16LE(2, 32)
  data.writeUInt16LE(16, 34)
  data.write('data', 36)
  data.writeUInt32LE(samples * 2, 40)
  return data
}

function track(id: string, overrides: Partial<Track> = {}): Track {
  return {
    id, title: `Track ${id}`, artist: 'Playback test', album: '', albumId: '',
    artistId: '', coverArtId: '', trackNumber: 1, discNumber: 1,
    durationMs: 30000, bitRate: 128, suffix: 'wav', contentType: 'audio/wav',
    ...overrides,
  }
}

async function openPlaylist(page: Page, tracks: Track[]) {
  await installApiMocks(page, { value: true }, { mockAudio: false })
  await installWsMock(page)
  await page.addInitScript(() => {
    const audios: HTMLAudioElement[] = []
    Object.assign(window, { playbackAudios: audios })
    const NativeAudio = window.Audio
    window.Audio = class extends NativeAudio {
      constructor() {
        super()
        audios.push(this)
      }
    }
  })
  await page.route('**/api/v1/playlists/playback', (route) => route.fulfill({ json: {
    id: 'playback', name: 'Playback test', source: 'local', externalId: 'playback',
    tracks: tracks.map((t) => ({ state: 'full', libraryTrack: t, title: t.title, artist: t.artist, durationMs: t.durationMs })),
    totalCount: tracks.length, ownedCount: tracks.length, trackCount: tracks.length,
  } }))
  await page.route(/\/api\/v1\/(?:external\/)?stream\//, (route) => {
    const url = new URL(route.request().url())
    const id = url.pathname.split('/').at(-1)
    const t = tracks.find((item) => item.id === id || item.externalStream?.externalId === id)!
    const seconds = t.durationMs / 1000 - Math.floor(Number(url.searchParams.get('t') || 0) / 1000)
    const body = wav(seconds)
    const range = /^bytes=(\d+)-(\d*)$/.exec(route.request().headers().range ?? '')
    const headers: Record<string, string> = { 'Accept-Ranges': 'bytes' }
    if (range) {
      const start = Number(range[1])
      const end = range[2] ? Math.min(Number(range[2]), body.length - 1) : body.length - 1
      headers['Content-Range'] = `bytes ${start}-${end}/${body.length}`
      return route.fulfill({ status: 206, headers, contentType: 'audio/wav', body: body.subarray(start, end + 1) })
    }
    return route.fulfill({ status: 200, headers, contentType: 'audio/wav', body })
  })
  await page.route('**/api/v1/library/track/*/duration', (route) => route.fulfill({ json: {} }))
  await page.goto('/playlist/playback')
  await page.getByRole('button', { name: 'Play Playback test' }).click()
}

async function audioState(page: Page) {
  return page.evaluate(() => {
    const a = (window as unknown as { playbackAudios: HTMLAudioElement[] }).playbackAudios[0]
    return { src: a.src, paused: a.paused, time: a.currentTime, ended: a.ended, error: a.error?.code }
  })
}

test('native audio advances from local to external, pauses, skips, and clears', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  await openPlaylist(page, [
    track('local', { durationMs: 1000 }),
    track('external', { externalStream: { source: 'deezer', externalId: 'external' } }),
    track('last'),
  ])
  const bar = page.getByTestId('player-bar')
  await expect(bar.getByText('Track external', { exact: true })).toBeVisible()
  await expect.poll(async () => (await audioState(page)).time).toBeGreaterThan(0.1)
  await bar.getByRole('button', { name: 'Pause', exact: true }).click()
  expect((await audioState(page)).paused).toBe(true)
  await bar.getByRole('button', { name: 'Next', exact: true }).click()
  await expect(bar.getByText('Track last', { exact: true })).toBeVisible()
  expect((await audioState(page)).paused).toBe(true)
  await bar.getByRole('button', { name: 'Play', exact: true }).click()
  await expect.poll(async () => (await audioState(page)).time).toBeGreaterThan(0.1)
  await bar.getByRole('button', { name: 'Previous', exact: true }).click()
  await expect(bar.getByText('Track external', { exact: true })).toBeVisible()
  await bar.getByRole('button', { name: 'Queue', exact: true }).click()
  await page.getByTestId('now-playing-panel').getByRole('button', { name: 'Clear', exact: true }).click()
  await expect.poll(async () => (await audioState(page)).src).toBe('')
  expect((await audioState(page)).paused).toBe(true)
  expect(errors).toEqual([])
})

test('native audio stops at the final crop and replays the cropped window', async ({ page }) => {
  await openPlaylist(page, [track('crop', { durationMs: 4000, cropStartMs: 1000, cropEndMs: 1800 })])
  const bar = page.getByTestId('player-bar')
  await expect(bar.getByRole('button', { name: 'Play', exact: true })).toBeVisible()
  const stopped = await audioState(page)
  expect(stopped.paused).toBe(true)
  expect(stopped.time).toBeLessThan(2.5)
  await bar.getByRole('button', { name: 'Play', exact: true }).click()
  await expect(bar.getByRole('button', { name: 'Pause', exact: true })).toBeVisible()
  expect((await audioState(page)).time).toBeLessThan(1.8)
})

test('repeat one restarts the full resource after a backend seek', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  await openPlaylist(page, [track('seek', { durationMs: 7000, suffix: 'opus', contentType: 'audio/ogg' })])
  const bar = page.getByTestId('player-bar')
  await bar.getByRole('button', { name: 'Enable repeat', exact: true }).click()
  await bar.getByRole('button', { name: 'Repeat all — click for repeat one', exact: true }).click()
  await bar.getByRole('slider', { name: 'Seek', exact: true }).press('ArrowRight')
  await expect.poll(async () => (await audioState(page)).src).toContain('?t=')
  await expect.poll(async () => (await audioState(page)).src, { timeout: 6000 }).toMatch(/\/stream\/seek$/)
  await expect.poll(async () => (await audioState(page)).time).toBeGreaterThan(0.1)
  expect((await audioState(page)).time).toBeLessThan(5)
  expect(errors).toEqual([])
})

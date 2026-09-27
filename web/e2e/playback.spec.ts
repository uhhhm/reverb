import { test, expect, type Page } from '@playwright/test'
import { installApiMocks, installWsMock, playerRequests } from './mocks'
import type { Track } from '../src/lib/types'

// Each run keeps its trace as the artifact: every player request and the audio
// element's state around each seek, pause and skip.
test.use({ trace: 'on' })

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
  // The bar offers Play before the queue answer has loaded anything, so the
  // label alone does not say playback reached the crop end: wait for the
  // element itself to stop there. Pressing the toggle any earlier would
  // pause the autoplay instead of replaying.
  await expect(bar.getByText('Track crop', { exact: true })).toBeVisible()
  await expect.poll(async () => {
    const a = await audioState(page)
    return a.paused && a.time >= 1.7
  }, { intervals: [50] }).toBe(true)
  expect((await audioState(page)).time).toBeLessThan(2.5)
  await expect(bar.getByRole('button', { name: 'Play', exact: true })).toBeVisible()
  await bar.getByRole('button', { name: 'Play', exact: true }).click()
  await expect(bar.getByRole('button', { name: 'Pause', exact: true })).toBeVisible()
  // The replay plays the window again from its start, not the trimmed intro.
  await expect.poll(async () => {
    const a = await audioState(page)
    return !a.paused && a.time >= 0.95 && a.time < 1.8
  }, { intervals: [50] }).toBe(true)
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

// The OS drives Reverb through navigator.mediaSession: the lock screen, media
// keys and the system media overlay. Chromium offers no way to press those, so
// the page keeps the handlers Reverb registers and the test calls them as the
// OS would.
async function captureMediaSession(page: Page) {
  await page.addInitScript(() => {
    const handlers: Record<string, MediaSessionActionHandler | null> = {}
    Object.assign(window, { mediaSessionHandlers: handlers })
    const ms = navigator.mediaSession
    const register = ms.setActionHandler.bind(ms)
    ms.setActionHandler = (action, handler) => {
      handlers[action] = handler
      register(action, handler)
    }
  })
}

async function osAction(page: Page, action: MediaSessionAction, seekTime?: number) {
  await page.evaluate(([a, t]) => {
    const handlers = (window as unknown as { mediaSessionHandlers: Record<string, MediaSessionActionHandler> }).mediaSessionHandlers
    handlers[a as string]({ action: a as MediaSessionAction, seekTime: t as number | undefined })
  }, [action, seekTime] as const)
}

type Sample = { entryId: string; positionMs: number; playing: boolean; seeking: boolean }

test.describe('OS media controls', () => {
  test('every seek reaches the core as a seek before playback continues from it', async ({ page }, testInfo) => {
    const errors: string[] = []
    page.on('pageerror', (error) => errors.push(error.message))
    await captureMediaSession(page)
    await openPlaylist(page, [track('long', { durationMs: 30000 }), track('after', { durationMs: 30000 })])
    const bar = page.getByTestId('player-bar')
    await expect(bar.getByText('Track long', { exact: true })).toBeVisible()
    await expect.poll(async () => (await audioState(page)).time).toBeGreaterThan(0.5)

    // A lock-screen scrub forward.
    await osAction(page, 'seekto', 12)
    await expect.poll(async () => (await audioState(page)).time).toBeGreaterThanOrEqual(12)
    // Paused and resumed from the OS, then dragged in Reverb's own bar, then
    // scrubbed back from the lock screen.
    await osAction(page, 'pause')
    await expect.poll(async () => (await audioState(page)).paused).toBe(true)
    await expect(bar.getByRole('button', { name: 'Play', exact: true })).toBeVisible()
    await osAction(page, 'play')
    await expect.poll(async () => (await audioState(page)).paused).toBe(false)
    const beforeBarSeek = (await audioState(page)).time
    // Two 5s steps: each sits right at the core's largest step that still
    // reads as playing, so only the seeking mark keeps it from counting.
    const seekBar = bar.getByRole('slider', { name: 'Seek', exact: true })
    await seekBar.press('ArrowRight')
    await seekBar.press('ArrowRight')
    await expect.poll(async () => (await audioState(page)).time).toBeGreaterThan(beforeBarSeek + 8)
    await osAction(page, 'seekto', 4)
    await expect.poll(async () => (await audioState(page)).time).toBeLessThan(8)
    // One more ordinary sample from the last target.
    await expect.poll(() => playerRequests.filter((r) => r.op === 'progress').at(-1)?.body.seeking, { timeout: 5000 }).toBe(false)

    const samples = playerRequests.filter((r) => r.op === 'progress').map((r) => r.body as unknown as Sample)
    await testInfo.attach('player-requests.json', { body: JSON.stringify(playerRequests, null, 2), contentType: 'application/json' })
    const seeks = samples.filter((s) => s.seeking).map((s) => Math.round(s.positionMs / 1000))
    expect(seeks[0]).toBe(12)
    expect(seeks.at(-1)).toBe(4)
    expect(seeks.length).toBe(4)
    expect(samples.every((s) => s.entryId === samples[0].entryId)).toBe(true)
    // No sample from a new position arrives before the seek to it: between
    // consecutive samples of one entry, any jump larger than a playback tick
    // is itself marked as a seek.
    for (let i = 1; i < samples.length; i++) {
      const [prev, cur] = [samples[i - 1], samples[i]]
      if (prev.entryId !== cur.entryId) continue
      const step = cur.positionMs - prev.positionMs
      if (step > 1500 || step < -250) expect(cur, `sample ${i} jumps ${step}ms without saying it seeked`).toMatchObject({ seeking: true })
    }
    expect(errors).toEqual([])
  })
})

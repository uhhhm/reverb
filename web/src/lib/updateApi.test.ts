import { describe, it, expect, vi, afterEach } from 'vitest'
import { fetchVersionInfo, fetchLatestRelease } from './updateApi'

function stubJson(body: unknown) {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(body), { status: 200 })))
}

describe('updateApi', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('fetchVersionInfo returns the server-configured update repo', async () => {
    stubJson({ version: '1.2.3', updateRepo: 'me/fork' })
    expect(await fetchVersionInfo()).toEqual({ version: '1.2.3', updateRepo: 'me/fork' })
  })

  it('fetchVersionInfo passes through an empty repo (updates disabled)', async () => {
    stubJson({ version: '1.2.3', updateRepo: '' })
    expect((await fetchVersionInfo()).updateRepo).toBe('')
  })

  it('fetchLatestRelease queries the given repo', async () => {
    stubJson({ tag_name: 'v2.0.0', body: 'notes', assets: [] })
    const rel = await fetchLatestRelease('me/fork')
    expect(fetch).toHaveBeenCalledWith(
      'https://api.github.com/repos/me/fork/releases/latest',
      expect.anything(),
    )
    expect(rel.tag).toBe('v2.0.0')
  })
})

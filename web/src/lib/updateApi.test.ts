import { describe, it, expect, vi, afterEach } from 'vitest'
import { fetchVersionInfo, fetchUpdateState, installUpdate } from './updateApi'

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

  it('fetchUpdateState fills in the fields the server omits', async () => {
    stubJson({ currentVersion: 'v1.0.0', staged: 'v2.0.0' })
    const st = await fetchUpdateState()
    expect(fetch).toHaveBeenCalledWith('/api/v1/update', expect.objectContaining({ method: 'GET' }))
    expect(st.staged).toBe('v2.0.0')
    expect(st.downloading).toBe(false)
    expect(st.progress).toBe(0)
  })

  it('installUpdate posts to the install endpoint', async () => {
    stubJson({ status: 'restarting' })
    await installUpdate()
    expect(fetch).toHaveBeenCalledWith(
      '/api/v1/update/install',
      expect.objectContaining({ method: 'POST' }),
    )
  })
})

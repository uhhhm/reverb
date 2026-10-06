import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { listOfflineSet, setOfflineSet } from './offlineSetApi'

describe('offlineSetApi', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify([]), { status: 200 })),
    )
  })
  afterEach(() => vi.unstubAllGlobals())

  it('listOfflineSet GETs /offline-set', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(
          JSON.stringify([{ playlistId: 'pl1', enabled: true, updatedAt: 123, playlistName: 'My Playlist' }]),
          { status: 200 },
        ),
      ),
    )
    const out = await listOfflineSet()
    expect(fetch).toHaveBeenCalledWith('/api/v1/offline-set', expect.objectContaining({ method: 'GET' }))
    expect(out[0].playlistId).toBe('pl1')
    expect(out[0].enabled).toBe(true)
    expect(out[0].updatedAt).toBe(123)
  })

  it('setOfflineSet PUTs /offline-set/{playlistId} with enabled true', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(JSON.stringify({ playlistId: 'pl1', enabled: true, updatedAt: 999 }), { status: 200 }),
      ),
    )
    const out = await setOfflineSet('pl1', true)
    expect(fetch).toHaveBeenCalledWith('/api/v1/offline-set/pl1', expect.objectContaining({ method: 'PUT' }))
    const call = (fetch as unknown as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(JSON.parse((call[1] as RequestInit).body as string)).toEqual({ enabled: true })
    expect(out.playlistId).toBe('pl1')
    expect(out.enabled).toBe(true)
  })

  it('setOfflineSet PUTs with enabled false', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(JSON.stringify({ playlistId: 'pl1', enabled: false, updatedAt: 1000 }), { status: 200 }),
      ),
    )
    await setOfflineSet('pl1', false)
    const call = (fetch as unknown as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(JSON.parse((call[1] as RequestInit).body as string)).toEqual({ enabled: false })
  })

  it('setOfflineSet URL-encodes playlistId', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(JSON.stringify({ playlistId: 'a/b c', enabled: true, updatedAt: 1 }), { status: 200 }),
      ),
    )
    await setOfflineSet('a/b c', true)
    expect(fetch).toHaveBeenCalledWith('/api/v1/offline-set/a%2Fb%20c', expect.objectContaining({ method: 'PUT' }))
  })
})

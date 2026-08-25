import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { resolveLink, addFromLink } from './linkApi'

describe('linkApi', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(
          JSON.stringify({
            kind: 'track',
            source: 'spotify',
            externalId: 'sp123',
            title: 'Spotify track sp123',
            artist: 'Unknown',
            album: '',
            url: 'https://open.spotify.com/track/sp123',
          }),
          { status: 200 },
        ),
      ),
    )
  })

  afterEach(() => vi.unstubAllGlobals())

  it('resolveLink POSTs /links/resolve with url', async () => {
    const res = await resolveLink('https://open.spotify.com/track/sp123')
    expect(fetch).toHaveBeenCalledWith(
      '/api/v1/links/resolve',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ url: 'https://open.spotify.com/track/sp123' }),
      }),
    )
    expect(res.source).toBe('spotify')
    expect(res.externalId).toBe('sp123')
  })

  it('addFromLink POSTs /links/add with url and opts', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(
          JSON.stringify({
            resolve: {
              kind: 'track',
              source: 'spotify',
              externalId: 'sp123',
              title: 'Spotify track sp123',
              artist: 'Unknown',
              album: '',
              url: 'https://open.spotify.com/track/sp123',
            },
            catalogId: 'trk_link_sp123',
            playlistId: 'pl1',
            job: { id: 'j1' },
          }),
          { status: 200 },
        ),
      ),
    )
    const res = await addFromLink('https://open.spotify.com/track/sp123', {
      playlistId: 'pl1',
      download: true,
    })
    expect(fetch).toHaveBeenCalledWith(
      '/api/v1/links/add',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ url: 'https://open.spotify.com/track/sp123', playlistId: 'pl1', download: true }),
      }),
    )
    expect(res.catalogId).toBe('trk_link_sp123')
    expect(res.playlistId).toBe('pl1')
  })

  it('addFromLink omits undefined opts', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(
          JSON.stringify({
            resolve: {
              kind: 'track',
              source: 'youtube',
              externalId: 'yt1',
              title: 'YouTube track yt1',
              artist: 'Unknown',
              album: '',
              url: 'https://www.youtube.com/watch?v=yt1',
            },
            catalogId: 'trk_link_yt1',
          }),
          { status: 200 },
        ),
      ),
    )
    await addFromLink('https://www.youtube.com/watch?v=yt1')
    expect(fetch).toHaveBeenCalledWith(
      '/api/v1/links/add',
      expect.objectContaining({
        body: JSON.stringify({ url: 'https://www.youtube.com/watch?v=yt1' }),
      }),
    )
  })

  it('addFromLink with download false sends download false', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(
          JSON.stringify({
            resolve: {
              kind: 'track',
              source: 'spotify',
              externalId: 'sp2',
              title: 'Spotify track sp2',
              artist: 'Unknown',
              album: '',
              url: 'https://open.spotify.com/track/sp2',
            },
            catalogId: 'trk_link_sp2',
          }),
          { status: 200 },
        ),
      ),
    )
    await addFromLink('https://open.spotify.com/track/sp2', { download: false })
    expect(fetch).toHaveBeenCalledWith(
      '/api/v1/links/add',
      expect.objectContaining({
        body: JSON.stringify({ url: 'https://open.spotify.com/track/sp2', download: false }),
      }),
    )
  })

  it('propagates ApiError on 422 unsupported', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify({ error: 'unsupported URL' }), { status: 422 })),
    )
    await expect(resolveLink('https://example.com/foo')).rejects.toMatchObject({ status: 422 })
  })

  it('propagates ApiError on 404 playlist not found for add', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify({ error: 'playlist not found' }), { status: 404 })),
    )
    await expect(
      addFromLink('https://open.spotify.com/track/sp123', { playlistId: 'missing' }),
    ).rejects.toMatchObject({ status: 404 })
  })
})

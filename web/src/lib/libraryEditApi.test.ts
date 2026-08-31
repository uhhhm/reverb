import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { batchRename, clearCovers, uploadCovers } from './libraryEditApi'

describe('libraryEditApi', () => {
  afterEach(() => vi.unstubAllGlobals())

  describe('batchRename', () => {
    it('POSTs to /api/v1/library/rename/batch with JSON body', async () => {
      const fetchMock = vi.fn(async () =>
        new Response(JSON.stringify({ applied: 2 }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
      vi.stubGlobal('fetch', fetchMock)

      const res = await batchRename({ tracks: [{ id: 't1', title: 'New' }] })
      expect(res.applied).toBe(2)
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/library/rename/batch',
        expect.objectContaining({ method: 'POST' }),
      )
      const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
      expect(JSON.parse(init.body as string)).toEqual({ tracks: [{ id: 't1', title: 'New' }] })
    })
  })

  describe('clearCovers', () => {
    it('issues DELETE to /api/v1/library/covers with {"targets":["album:a1"]}', async () => {
      const fetchMock = vi.fn(async () =>
        new Response(JSON.stringify({ applied: 1 }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
      vi.stubGlobal('fetch', fetchMock)

      await clearCovers([{ kind: 'album', id: 'a1' }])
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/library/covers',
        expect.objectContaining({ method: 'DELETE' }),
      )
      const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
      expect(JSON.parse(init.body as string)).toEqual({ targets: ['album:a1'] })
    })

    it('formats multiple targets as kind:id', async () => {
      const fetchMock = vi.fn(async () =>
        new Response(JSON.stringify({ applied: 2 }), { status: 200 }),
      )
      vi.stubGlobal('fetch', fetchMock)

      await clearCovers([
        { kind: 'album', id: 'a1' },
        { kind: 'track', id: 't1' },
      ])
      const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
      expect(JSON.parse(init.body as string)).toEqual({ targets: ['album:a1', 'track:t1'] })
    })
  })

  describe('uploadCovers', () => {
    beforeEach(() => {
      // jsdom has no File in some Node env globals — ensure it exists
    })

    it('POSTs multipart FormData with image and target entries', async () => {
      let capturedForm: FormData | null = null
      const fetchMock = vi.fn(async (_url: string, init?: RequestInit) => {
        capturedForm = init?.body as FormData
        return new Response(JSON.stringify({ applied: 2, coverArtId: 'custom:abc.png' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      })
      vi.stubGlobal('fetch', fetchMock)

      const file = new File(['img'], 'cover.png', { type: 'image/png' })
      const targets = [
        { kind: 'album' as const, id: 'a1' },
        { kind: 'track' as const, id: 't1' },
      ]
      const res = await uploadCovers(file, targets)

      expect(res.coverArtId).toBe('custom:abc.png')
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/library/covers',
        expect.objectContaining({ method: 'POST' }),
      )
      expect(capturedForm).not.toBeNull()
      const form = capturedForm as unknown as FormData
      // one image entry
      expect(form.get('image')).not.toBeNull()
      // one target entry per target via getAll
      expect(form.getAll('target')).toEqual(['album:a1', 'track:t1'])
    })

    it('throws on non-ok response', async () => {
      const fetchMock = vi.fn(async () =>
        new Response(JSON.stringify({ error: 'too large' }), {
          status: 400,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
      vi.stubGlobal('fetch', fetchMock)

      const file = new File(['img'], 'cover.png', { type: 'image/png' })
      await expect(uploadCovers(file, [{ kind: 'album', id: 'a1' }])).rejects.toThrow('too large')
    })

    it('throws with generic message when error body is empty', async () => {
      const fetchMock = vi.fn(async () => new Response('', { status: 500 }))
      vi.stubGlobal('fetch', fetchMock)

      const file = new File(['img'], 'cover.png', { type: 'image/png' })
      await expect(uploadCovers(file, [{ kind: 'album', id: 'a1' }])).rejects.toThrow('upload failed')
    })
  })
})

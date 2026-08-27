import { describe, it, expect, afterEach } from 'vitest'
import { mediaBase } from './mediaBase'
import { streamUrl } from './libraryApi'

afterEach(() => {
  delete window.__REVERB_PORT__
})

describe('mediaBase', () => {
  it('is relative when no port is injected (browser)', () => {
    expect(mediaBase()).toBe('')
    expect(streamUrl('t1')).toBe('/api/v1/stream/t1')
  })

  it('points at the loopback listener when a port is injected (desktop)', () => {
    window.__REVERB_PORT__ = 41234
    expect(mediaBase()).toBe('http://127.0.0.1:41234')
    expect(streamUrl('t1')).toBe('http://127.0.0.1:41234/api/v1/stream/t1')
  })

  it('encodes the track id', () => {
    expect(streamUrl('a b/c')).toBe('/api/v1/stream/a%20b%2Fc')
  })
})

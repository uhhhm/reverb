import { describe, it, expect } from 'vitest'
import { parseLinks, isYouTubeLink } from './parseLinks'

describe('parseLinks', () => {
  it('returns nothing for blank input', () => {
    expect(parseLinks('')).toEqual([])
    expect(parseLinks('   \n  ')).toEqual([])
  })

  it('splits on newlines', () => {
    expect(parseLinks('https://a\nhttps://b')).toEqual(['https://a', 'https://b'])
  })

  it('splits on spaces and commas too', () => {
    expect(parseLinks('https://a, https://b https://c')).toEqual([
      'https://a',
      'https://b',
      'https://c',
    ])
  })

  it('drops duplicates so a double paste does not download twice', () => {
    expect(parseLinks('https://a\nhttps://b\nhttps://a')).toEqual(['https://a', 'https://b'])
  })
})

describe('isYouTubeLink', () => {
  it('accepts the YouTube hosts yt-dlp can trim', () => {
    expect(isYouTubeLink('https://www.youtube.com/watch?v=abc')).toBe(true)
    expect(isYouTubeLink('https://youtube.com/watch?v=abc')).toBe(true)
    expect(isYouTubeLink('https://music.youtube.com/watch?v=abc')).toBe(true)
    expect(isYouTubeLink('https://youtu.be/abc')).toBe(true)
  })

  it('rejects everything else', () => {
    expect(isYouTubeLink('https://open.spotify.com/track/x')).toBe(false)
    expect(isYouTubeLink('not a url')).toBe(false)
    expect(isYouTubeLink('')).toBe(false)
  })
})

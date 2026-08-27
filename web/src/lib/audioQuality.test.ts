import { describe, it, expect } from 'vitest'
import { qualityForBitrate, isUpgrade, qualityLabel } from './audioQuality'

describe('audioQuality', () => {
  it('classifies a bitrate into the lowest tier that could produce it', () => {
    expect(qualityForBitrate(0)).toBe('')
    expect(qualityForBitrate(128)).toBe('low')
    expect(qualityForBitrate(143)).toBe('medium') // typical YouTube Opus
    expect(qualityForBitrate(320)).toBe('high')
  })

  it('only counts a strictly higher tier as an upgrade', () => {
    expect(isUpgrade('low', 'high')).toBe(true)
    expect(isUpgrade('high', 'best')).toBe(true)
    expect(isUpgrade('high', 'high')).toBe(false)
    expect(isUpgrade('best', 'low')).toBe(false)
  })

  it('treats unknown current quality as upgradable', () => {
    expect(isUpgrade('', 'high')).toBe(true)
  })

  it('labels tiers', () => {
    expect(qualityLabel('best')).toBe('Best')
  })
})

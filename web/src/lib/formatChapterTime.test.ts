import { describe, it, expect } from 'vitest'
import { formatChapterTime } from './formatChapterTime'

describe('formatChapterTime', () => {
  it('formats under an hour as M:SS', () => {
    expect(formatChapterTime(0)).toBe('0:00')
    expect(formatChapterTime(9)).toBe('0:09')
    expect(formatChapterTime(90)).toBe('1:30')
    expect(formatChapterTime(599)).toBe('9:59')
  })

  it('formats an hour or more as H:MM:SS', () => {
    expect(formatChapterTime(3600)).toBe('1:00:00')
    expect(formatChapterTime(3750)).toBe('1:02:30')
  })

  it('clamps negatives and drops fractions', () => {
    expect(formatChapterTime(-5)).toBe('0:00')
    expect(formatChapterTime(90.9)).toBe('1:30')
  })
})

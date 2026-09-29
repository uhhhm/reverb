import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const css = readFileSync(resolve(__dirname, './index.css'), 'utf8')

describe('design tokens', () => {
  it('keeps the configurable accent defaulting to red #F0354B channels', () => {
    expect(css).toMatch(/--color-accent:\s*240 53 75/)
  })
  it('disables motion under prefers-reduced-motion', () => {
    expect(css).toMatch(/prefers-reduced-motion:\s*reduce/)
  })
})

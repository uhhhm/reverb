import { describe, it, expect, afterEach } from 'vitest'
import { THEMES, applyTheme, normalizeThemeId, isThemeId } from './themes'

afterEach(() => {
  document.documentElement.removeAttribute('data-theme')
  window.localStorage.clear()
})

describe('themes', () => {
  it('offers a light theme alongside the dark ones', () => {
    expect(isThemeId('light')).toBe(true)
    expect(THEMES.map((t) => t.id)).toContain('light')
  })

  it('applies the theme to <html> and remembers it', () => {
    applyTheme('light')
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(window.localStorage.getItem('reverb:theme')).toBe('light')
  })

  it('falls back to the default for an unknown theme', () => {
    expect(normalizeThemeId('solarized')).toBe('default-dark')
    expect(normalizeThemeId(undefined)).toBe('default-dark')
  })

  it('gives every theme a preview so the picker can render it', () => {
    for (const theme of THEMES) {
      expect(theme.label).toBeTruthy()
      expect(theme.preview.base).toMatch(/^#[0-9a-f]{6}$/i)
      expect(theme.preview.raised).toMatch(/^#[0-9a-f]{6}$/i)
      expect(theme.preview.text).toMatch(/^#[0-9a-f]{6}$/i)
    }
  })
})

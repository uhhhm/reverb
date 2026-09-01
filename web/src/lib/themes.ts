export type ThemeId =
  | 'default-dark'
  | 'light'
  | 'catppuccin-mocha'
  | 'catppuccin-macchiato'
  | 'catppuccin-frappe'

export interface ThemeMeta {
  id: ThemeId
  label: string
  description: string
  // preview swatches for the picker (outer → inner → card)
  preview: { base: string; surface: string; raised: string; text: string }
}

export const DEFAULT_THEME: ThemeId = 'default-dark'

export const THEMES: ThemeMeta[] = [
  {
    id: 'default-dark',
    label: 'Default Dark',
    description: 'Pure black — original',
    preview: { base: '#000000', surface: '#121212', raised: '#181818', text: '#ffffff' },
  },
  {
    id: 'light',
    label: 'Light',
    description: 'Clean & bright',
    preview: { base: '#e7e7ec', surface: '#f4f4f7', raised: '#ffffff', text: '#17171c' },
  },
  {
    id: 'catppuccin-mocha',
    label: 'Catppuccin Mocha',
    description: 'Cozy — the original',
    preview: { base: '#11111b', surface: '#181825', raised: '#1e1e2e', text: '#cdd6f4' },
  },
  {
    id: 'catppuccin-macchiato',
    label: 'Catppuccin Macchiato',
    description: 'Medium contrast, soothing',
    preview: { base: '#181926', surface: '#1e2030', raised: '#24273a', text: '#cad3f5' },
  },
  {
    id: 'catppuccin-frappe',
    label: 'Catppuccin Frappé',
    description: 'Muted & soft',
    preview: { base: '#232634', surface: '#292c3c', raised: '#303446', text: '#c6d0f5' },
  },
]

const VALID_THEMES = new Set<string>(THEMES.map((t) => t.id))

export function isThemeId(v: string): v is ThemeId {
  return VALID_THEMES.has(v)
}

export function normalizeThemeId(v: string | undefined | null): ThemeId {
  if (v && isThemeId(v)) return v
  return DEFAULT_THEME
}

// applyTheme sets the data-theme attribute on <html> so the CSS vars in
// index.css ([data-theme="..."]) take effect live. Falls back to default-dark
// for any unknown value (e.g. stale DB entry).
export function applyTheme(themeId: string): void {
  const normalized = normalizeThemeId(themeId)
  const root = document.documentElement
  if (normalized === DEFAULT_THEME) {
    // Keep the attribute explicit so selectors are predictable, but :root
    // already holds the same defaults — removing it would also work.
    root.dataset.theme = DEFAULT_THEME
  } else {
    root.dataset.theme = normalized
  }
  try {
    localStorage.setItem('reverb:theme', normalized)
  } catch {
    // ignore — storage may be unavailable in some contexts
  }
}

// applyThemeFromCache applies the theme from localStorage synchronously before
// the settings fetch completes, to avoid a flash of the default theme.
export function applyThemeFromCache(): void {
  try {
    const cached = localStorage.getItem('reverb:theme')
    if (cached && isThemeId(cached)) {
      document.documentElement.dataset.theme = cached
    }
  } catch {
    // ignore
  }
}

import { THEMES, applyTheme, type ThemeId, normalizeThemeId } from '../lib/themes'
import { useSettings, useUpdateSettings } from '../lib/settingsApi'

export function ThemePicker() {
  const settings = useSettings()
  const updateSettings = useUpdateSettings()
  const activeId = normalizeThemeId(settings.data?.theme)

  function selectTheme(id: ThemeId) {
    applyTheme(id)
    updateSettings.mutate({ theme: id })
  }

  return (
    <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
      {THEMES.map((theme) => {
        const active = theme.id === activeId
        return (
          <button
            key={theme.id}
            type="button"
            aria-label={theme.label}
            aria-pressed={active}
            onClick={() => selectTheme(theme.id)}
            className={[
              'group flex flex-col gap-2 rounded-xl border-2 p-2.5 text-left transition-all',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-canvas',
              active
                ? 'border-accent bg-raised shadow-float'
                : 'border-border-subtle bg-surface hover:border-text-muted/30 hover:bg-raised',
            ].join(' ')}
          >
            {/* Mini preview: outer base → surface header → raised card */}
            <div
              className="h-[52px] w-full overflow-hidden rounded-lg border"
              style={{ background: theme.preview.base, borderColor: theme.preview.surface }}
            >
              <div className="h-3 w-full" style={{ background: theme.preview.surface }} />
              <div className="px-2 py-1.5">
                <div
                  className="h-5 w-full rounded-md px-1.5 py-1"
                  style={{ background: theme.preview.raised }}
                >
                  <div className="h-1.5 w-3/4 rounded-full opacity-90" style={{ background: theme.preview.text }} />
                  <div className="mt-1 h-1 w-1/2 rounded-full opacity-50" style={{ background: theme.preview.text }} />
                </div>
              </div>
            </div>
            <div className="min-w-0">
              <div
                className={[
                  'text-xs font-bold leading-tight',
                  active ? 'text-text-primary' : 'text-text-primary',
                ].join(' ')}
              >
                {theme.label}
              </div>
              <div className="text-[11px] leading-tight text-text-muted mt-0.5">{theme.description}</div>
            </div>
          </button>
        )
      })}
    </div>
  )
}

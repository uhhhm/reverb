import { useEffect, useState } from 'react'
import { fetchLatestRelease, fetchVersionInfo, isNewer } from '../lib/updateApi'

export interface UpdateBannerProps {
  tag: string
  onDismiss: () => void
  onUpdate: () => void
}

// Wails runtime type (minimal slice we use). The actual Wails runtime exposes
// window.runtime.EventsOn and window.runtime.EventsOff.
interface WailsRuntime {
  EventsOn: (event: string, cb: (data: unknown) => void) => void
  EventsOff: (event: string) => void
}

declare global {
  interface Window {
    runtime?: WailsRuntime
    // Wails v2 also exposes window.wails at runtime; keep both checks for compat.
    wails?: unknown
  }
}

export function UpdateBanner({ tag: propTag, onDismiss, onUpdate }: UpdateBannerProps) {
  const [internalTag, setInternalTag] = useState<string | null>(null)

  // Listen for wails event or poll GitHub when no prop tag is driving the banner.
  // If propTag is provided, it is authoritative; internalTag only matters when
  // the component is mounted without a tag (e.g. AppShell integration).
  useEffect(() => {
    if (propTag) return

    // Wails desktop: subscribe to "update:available" emitted by Go poller.
    const runtime = window.runtime
    if (runtime?.EventsOn) {
      const handler = (data: unknown) => {
        const tag = typeof data === 'string' ? data : (data as { tag?: string })?.tag
        if (tag) setInternalTag(tag)
      }
      runtime.EventsOn('update:available', handler)
      return () => {
        try {
          runtime.EventsOff('update:available')
        } catch {
          /* ignore */
        }
      }
    }

    // Web fallback: poll version + GitHub latest every 6h.
    let cancelled = false
    async function poll() {
      try {
        const { version, updateRepo } = await fetchVersionInfo()
        if (!updateRepo) return
        const rel = await fetchLatestRelease(updateRepo)
        if (!cancelled && isNewer(version, rel.tag)) {
          setInternalTag(rel.tag)
        }
      } catch {
        /* log-only */
      }
    }
    void poll()
    const id = window.setInterval(() => void poll(), 6 * 60 * 60 * 1000)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [propTag])

  const tag = propTag || internalTag || ''
  if (!tag) return null

  return (
    <div
      data-testid="update-banner"
      role="status"
      aria-live="polite"
      className="flex items-center justify-between gap-3 px-4 py-3 bg-accent text-on-accent rounded-lg"
    >
      <span className="text-sm font-medium">Update available: {tag}</span>
      <div className="flex items-center gap-2 flex-none">
        <button
          type="button"
          onClick={onUpdate}
          className="inline-flex items-center justify-center rounded-full bg-white text-accent px-3 py-1.5 text-sm font-semibold hover:opacity-90"
        >
          Download &amp; Restart
        </button>
        <button
          type="button"
          aria-label="Dismiss"
          onClick={() => {
            setInternalTag(null)
            onDismiss()
          }}
          className="inline-flex items-center justify-center rounded-full border border-white/40 px-3 py-1.5 text-sm font-semibold hover:bg-white/10"
        >
          Dismiss
        </button>
      </div>
    </div>
  )
}

import { useState } from 'react'
import { Button } from './ui/Button'
import { Checkbox } from './ui/Checkbox'
import { listChapters, type Chapter, type LinkOptions } from '../lib/linkApi'
import { formatChapterTime } from '../lib/formatChapterTime'

interface LinkOptionsPanelProps {
  url: string
  value: LinkOptions
  onChange: (next: LinkOptions) => void
}

/**
 * Per-link trim / chapter-split options for a YouTube link.
 *
 * Trimming and chapter splitting are mutually exclusive: once a video is cut to
 * a range its chapter boundaries no longer mean anything, so selecting one
 * clears and disables the other.
 */
export function LinkOptionsPanel({ url, value, onChange }: LinkOptionsPanelProps) {
  const [open, setOpen] = useState(false)
  const [chapters, setChapters] = useState<Chapter[] | null>(null)
  const [chaptersLoading, setChaptersLoading] = useState(false)
  const [chaptersError, setChaptersError] = useState<string | null>(null)

  const trimmed = Boolean(value.startTime || value.endTime)
  const split = Boolean(value.splitChapters)

  async function loadChapters() {
    setChaptersLoading(true)
    setChaptersError(null)
    try {
      setChapters(await listChapters(url))
    } catch (e) {
      setChapters(null)
      setChaptersError(e instanceof Error ? e.message : 'Could not read chapters')
    } finally {
      setChaptersLoading(false)
    }
  }

  const summary = split
    ? 'split into chapters'
    : trimmed
      ? `${value.startTime || 'start'} → ${value.endTime || 'end'}`
      : 'whole video'

  return (
    <div className="rounded-md border border-border-subtle bg-surface">
      <button
        type="button"
        aria-expanded={open}
        aria-label={`Advanced options for ${url}`}
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs text-text-secondary hover:text-text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent rounded-md"
      >
        <span aria-hidden="true">{open ? '▾' : '▸'}</span>
        <span className="font-semibold">Advanced</span>
        <span className="min-w-0 flex-1 truncate text-text-muted">{summary}</span>
      </button>

      {open && (
        <div className="space-y-4 border-t border-border-subtle px-3 py-3">
          {/* Trim */}
          <fieldset className="space-y-2" disabled={split}>
            <legend className="text-xs font-semibold text-text-primary">Trim to a time range</legend>
            <div className="flex items-center gap-2">
              <label className="sr-only" htmlFor={`start-${url}`}>
                Start time
              </label>
              <input
                id={`start-${url}`}
                aria-label={`Start time for ${url}`}
                placeholder="0:00"
                value={value.startTime ?? ''}
                onChange={(e) => onChange({ ...value, startTime: e.target.value })}
                className="w-24 rounded-md border border-border-subtle bg-input px-2 py-1 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent focus:ring-1 focus:ring-accent disabled:opacity-50"
              />
              <span aria-hidden="true" className="text-text-muted">
                →
              </span>
              <label className="sr-only" htmlFor={`end-${url}`}>
                End time
              </label>
              <input
                id={`end-${url}`}
                aria-label={`End time for ${url}`}
                placeholder="end"
                value={value.endTime ?? ''}
                onChange={(e) => onChange({ ...value, endTime: e.target.value })}
                className="w-24 rounded-md border border-border-subtle bg-input px-2 py-1 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent focus:ring-1 focus:ring-accent disabled:opacity-50"
              />
              {trimmed && (
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={`Clear time range for ${url}`}
                  onClick={() => onChange({ ...value, startTime: '', endTime: '' })}
                >
                  Clear
                </Button>
              )}
            </div>
            <p className="text-xs text-text-muted">
              Seconds, M:SS or H:MM:SS. Leave either side blank for the start or end of the video.
            </p>
          </fieldset>

          {/* Chapter split */}
          <div className="space-y-2 border-t border-border-subtle pt-3">
            <label className="flex items-center gap-2">
              <Checkbox
                label={`Split into chapters for ${url}`}
                checked={split}
                disabled={trimmed}
                onChange={(next) =>
                  onChange(next ? { splitChapters: true, startTime: '', endTime: '' } : { ...value, splitChapters: false })
                }
              />
              <span className="text-xs font-semibold text-text-primary">Split into chapters</span>
            </label>
            <p className="text-xs text-text-muted">
              {trimmed
                ? 'Clear the time range first — a trimmed video has no meaningful chapters.'
                : 'Each chapter becomes its own track, with the video title as the album.'}
            </p>

            {split && (
              <div className="space-y-2">
                <Button
                  variant="secondary"
                  size="sm"
                  aria-label={`Preview chapters for ${url}`}
                  disabled={chaptersLoading}
                  onClick={() => void loadChapters()}
                >
                  {chaptersLoading ? 'Reading…' : 'Preview chapters'}
                </Button>
                {chaptersError && (
                  <p role="alert" className="text-xs text-error">
                    {chaptersError}
                  </p>
                )}
                {chapters && chapters.length === 0 && (
                  <p className="text-xs text-error">This video has no chapters to split on.</p>
                )}
                {chapters && chapters.length > 0 && (
                  <ol data-testid="chapter-list" className="space-y-0.5 text-xs text-text-secondary">
                    {chapters.map((c, i) => (
                      <li key={`${c.startSec}-${i}`} className="flex gap-2">
                        <span className="tabular-nums text-text-muted">
                          {formatChapterTime(c.startSec)}
                        </span>
                        <span className="min-w-0 flex-1 truncate">{c.title}</span>
                      </li>
                    ))}
                  </ol>
                )}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

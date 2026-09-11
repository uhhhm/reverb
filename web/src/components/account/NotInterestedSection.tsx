import { Button, Skeleton } from '../ui'
import { useNotInterested, useUndoNotInterested } from '../../lib/notInterestedApi'

/** Settings panel — the tracks and artists Reverb will never recommend. */
export function NotInterestedSection() {
  const { data, isLoading } = useNotInterested()
  const undo = useUndoNotInterested()
  const marks = data?.marks ?? []

  return (
    <div className="space-y-4 py-5">
      <div>
        <div className="text-sm font-bold text-text-primary">Not interested</div>
        <div className="mt-0.5 text-xs text-text-secondary">
          Tracks and artists Reverb will never recommend. A mark applies on every paired device, and so
          does undoing it.
        </div>
      </div>

      {isLoading ? (
        <div className="space-y-2">
          <Skeleton className="h-10 w-full" rounded="md" />
          <Skeleton className="h-10 w-full" rounded="md" />
        </div>
      ) : marks.length === 0 ? (
        <p className="text-sm text-text-muted">
          Nothing marked. Use &ldquo;Not interested&rdquo; in a track&apos;s menu or on an artist page.
        </p>
      ) : (
        <ul className="divide-y divide-border-subtle">
          {marks.map((m) => {
            const name = m.kind === 'track' ? m.title || m.artist : m.artist
            return (
              <li key={m.key} className="flex items-center gap-4 py-3">
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-semibold text-text-primary">{name}</div>
                  <div className="truncate text-xs text-text-muted">
                    {m.kind === 'track' ? `Track · ${m.artist}` : 'Artist'}
                  </div>
                </div>
                <Button
                  variant="ghost"
                  size="md"
                  disabled={undo.isPending}
                  onClick={() => undo.mutate(m.key)}
                  aria-label={`Undo not interested in ${name}`}
                >
                  Undo
                </Button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

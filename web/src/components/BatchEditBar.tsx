import { Button } from './ui'

interface BatchEditBarProps {
  count: number
  /** Hidden when nothing supports artwork, e.g. an artist selection. */
  canSetCover?: boolean
  onRename: () => void
  onSetCover?: () => void
  onSelectAll: () => void
  onClear: () => void
}

/**
 * The action bar for a multi-select. It docks to the bottom of the page so it
 * stays reachable while the user keeps scrolling and selecting.
 */
export function BatchEditBar({
  count,
  canSetCover = false,
  onRename,
  onSetCover,
  onSelectAll,
  onClear,
}: BatchEditBarProps) {
  if (count === 0) return null
  return (
    <div
      role="toolbar"
      aria-label="Edit selection"
      data-testid="batch-edit-bar"
      className="sticky bottom-4 z-30 mx-auto flex w-fit max-w-full flex-wrap items-center gap-2 rounded-full border border-border-subtle bg-raised px-4 py-2 shadow-pop"
    >
      <span className="px-1 text-sm font-semibold text-text-primary">{count} selected</span>
      <Button size="sm" variant="secondary" onClick={onRename}>
        Rename…
      </Button>
      {canSetCover && onSetCover && (
        <Button size="sm" variant="secondary" onClick={onSetCover}>
          Set cover…
        </Button>
      )}
      <Button size="sm" variant="ghost" onClick={onSelectAll}>
        Select all
      </Button>
      <Button size="sm" variant="ghost" onClick={onClear}>
        Clear
      </Button>
    </div>
  )
}

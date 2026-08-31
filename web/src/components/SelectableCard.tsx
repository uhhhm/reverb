import type { ReactNode } from 'react'
import type React from 'react'
import { Checkbox } from './ui'

interface SelectableCardProps {
  selected: boolean
  /** When false the card behaves normally and no checkbox is drawn. */
  selecting: boolean
  onToggle: () => void
  /**
   * Optional range and sweep handlers from selectionHandlers. They go on the
   * wrapper rather than the overlay button so a sweep entering the card is seen
   * even though pointerenter does not bubble.
   */
  gestures?: {
    onPointerDown: (e: React.PointerEvent) => void
    onPointerEnter: () => void
    onClickCapture: (e: React.MouseEvent) => void
  }
  label: string
  children: ReactNode
}

/**
 * Wraps a card in a selection checkbox without the card itself knowing about
 * selection. While selecting, a click anywhere on the card toggles it rather
 * than navigating — the whole point of a selection mode is that the same click
 * means something different.
 */
export function SelectableCard({
  selected,
  selecting,
  onToggle,
  gestures,
  label,
  children,
}: SelectableCardProps) {
  if (!selecting) return <>{children}</>
  return (
    <div className="relative" {...gestures}>
      <div
        className={[
          'rounded-lg transition-shadow',
          selected ? 'ring-2 ring-accent' : '',
        ].join(' ')}
      >
        {children}
      </div>
      {/* Sits over the card and swallows the click, so the card's own onClick
          (navigation) never fires while selecting. */}
      <button
        type="button"
        aria-label={`${selected ? 'Deselect' : 'Select'} ${label}`}
        aria-pressed={selected}
        onClick={onToggle}
        className="absolute inset-0 rounded-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
      />
      {/* Visual only — the overlay button above carries the accessible control. */}
      <span aria-hidden="true" className="pointer-events-none absolute left-4 top-4 rounded bg-canvas/80 p-1">
        <Checkbox checked={selected} onChange={onToggle} label={label} tabIndex={-1} />
      </span>
    </div>
  )
}

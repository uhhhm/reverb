import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Icon } from './ui'
import type { IconName } from './ui/Icon'
import { AddToPlaylistMenu } from './AddToPlaylistMenu'
import { useTrackUpgrade } from '../lib/useTrackUpgrade'
import { TrackQualityDialog } from './TrackQualityDialog'
import { TrackCropDialog } from './TrackCropDialog'
import { qualityLabel } from '../lib/audioQuality'
import type { Track } from '../lib/types'

interface TrackActionsMenuProps {
  track: Track
  /** Show the rename action. Omit to hide it. */
  onRename?: (track: Track) => void
}

interface MenuItem {
  icon: IconName
  label: string
  description: string
  disabled?: boolean
  onSelect: () => void
}

const PANEL_WIDTH = 264
const MARGIN = 8
/** Panel chrome plus one row; rows are two lines of text at a fixed size. */
const ITEM_HEIGHT = 56
const PANEL_PADDING = 12

function estimatedHeight(count: number): number {
  return count * ITEM_HEIGHT + PANEL_PADDING
}

/**
 * The row-level actions for an owned track, behind one "…" trigger.
 *
 * Each action carries a one-line description: the actions are not
 * self-explanatory from an icon alone (an upgrade re-downloads and replaces the
 * file), and the list is expected to grow.
 */
export function TrackActionsMenu({ track, onRename }: TrackActionsMenuProps) {
  const triggerRef = useRef<HTMLButtonElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  const [playlistOpen, setPlaylistOpen] = useState(false)
  const [qualityOpen, setQualityOpen] = useState(false)
  const [cropOpen, setCropOpen] = useState(false)
  const [pos, setPos] = useState<{ top: number; left: number }>({ top: 0, left: 0 })
  const upgrade = useTrackUpgrade(track)

  useEffect(() => {
    if (!open) return
    panelRef.current?.querySelector<HTMLElement>('button')?.focus()
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open])

  if (!track.id) return null

  const items: MenuItem[] = []
  if (onRename) {
    items.push({
      icon: 'pencil',
      label: 'Edit details',
      description: 'Correct the title, artist or album for this track',
      onSelect: () => onRename(track),
    })
  }
  items.push({
    icon: 'plus',
    label: 'Add to playlist',
    description: 'Put this track in one of your playlists',
    onSelect: () => setPlaylistOpen(true),
  })
  items.push({
    icon: 'scissors',
    label: 'Crop',
    description: 'Trim the intro or outro — your file is never modified',
    onSelect: () => setCropOpen(true),
  })
  if (upgrade.available) {
    items.push({
      icon: 'up',
      label: 'Audio quality',
      description: upgrade.current
        ? `Currently ${qualityLabel(upgrade.current)} — pick a higher or lower tier`
        : 'Pick the tier this track is fetched at',
      disabled: upgrade.isPending,
      onSelect: () => setQualityOpen(true),
    })
  }

  function openMenu() {
    const rect = triggerRef.current?.getBoundingClientRect()
    if (rect) {
      // Flip above the trigger when the panel would not fit below it, and clamp
      // so a row near the bottom of the window never pushes the menu off-screen.
      const height = estimatedHeight(items.length)
      const below = window.innerHeight - rect.bottom - MARGIN
      const above = rect.top - MARGIN
      const top =
        height <= below || below >= above
          ? Math.min(rect.bottom + 6, window.innerHeight - MARGIN - Math.min(height, below))
          : Math.max(MARGIN, rect.top - 6 - Math.min(height, above))
      setPos({
        top: Math.max(MARGIN, top),
        left: Math.max(8, Math.min(rect.right - PANEL_WIDTH, window.innerWidth - PANEL_WIDTH - 8)),
      })
    }
    setOpen(true)
  }

  function toggleMenu() {
    if (open) {
      setOpen(false)
      return
    }
    openMenu()
  }

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        aria-label={`More actions for ${track.title}`}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={(e) => {
          e.stopPropagation()
          toggleMenu()
        }}
        onDoubleClick={(e) => e.stopPropagation()}
        className={[
          'inline-grid place-items-center w-7 h-7 rounded-md',
          'text-text-muted hover:text-text-primary',
          open ? 'opacity-100' : 'opacity-0 group-hover:opacity-100',
          'transition-opacity duration-150',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:opacity-100',
        ].join(' ')}
      >
        <Icon name="more" className="w-3.5 h-3.5" />
      </button>

      {open &&
        createPortal(
          <>
            <div
              data-testid="track-actions-backdrop"
              className="fixed inset-0 z-40"
              aria-hidden="true"
              onClick={() => setOpen(false)}
            />
            <div
              ref={panelRef}
              role="menu"
              aria-label={`Actions for ${track.title}`}
              style={{
                top: pos.top,
                left: pos.left,
                width: PANEL_WIDTH,
                maxHeight: `calc(100vh - ${pos.top + MARGIN}px)`,
              }}
              className="fixed z-50 overflow-y-auto rounded-xl border border-border-subtle bg-raised p-1.5 shadow-pop"
              onClick={(e) => e.stopPropagation()}
            >
              {items.map((item) => (
                <button
                  key={item.label}
                  type="button"
                  role="menuitem"
                  disabled={item.disabled}
                  onClick={() => {
                    setOpen(false)
                    item.onSelect()
                  }}
                  className="flex w-full items-start gap-3 rounded-lg px-2.5 py-2 text-left transition-colors hover:bg-raised-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent disabled:cursor-not-allowed disabled:opacity-40"
                >
                  <span className="mt-0.5 flex h-6 w-6 flex-none items-center justify-center rounded-md bg-surface text-accent">
                    <Icon name={item.icon} className="w-3.5 h-3.5" />
                  </span>
                  <span className="min-w-0">
                    <span className="block text-sm font-semibold text-text-primary">{item.label}</span>
                    <span className="block text-xs leading-snug text-text-muted">{item.description}</span>
                  </span>
                </button>
              ))}
            </div>
          </>,
          document.body,
        )}

      {playlistOpen && <AddToPlaylistMenu track={track} onClose={() => setPlaylistOpen(false)} />}
      {qualityOpen && <TrackQualityDialog track={track} onClose={() => setQualityOpen(false)} />}
      {cropOpen && <TrackCropDialog track={track} onClose={() => setCropOpen(false)} />}
    </>
  )
}

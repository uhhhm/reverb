import { useEffect, useRef, type ReactNode } from 'react'

const FOCUSABLE = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'

interface ModalProps {
  open: boolean
  onClose: () => void
  title: string
  /** Widens the panel for content that needs it, like a preview table. */
  size?: 'md' | 'lg'
  children: ReactNode
  /** Rendered right-aligned under the content; usually Cancel plus the action. */
  footer?: ReactNode
  testId?: string
}

/**
 * A modal with the focus trap, Esc handling, and focus restore that every
 * dialog needs, so each one is only its own content.
 */
export function Modal({ open, onClose, title, size = 'md', children, footer, testId }: ModalProps) {
  const panelRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const previouslyFocused = document.activeElement as HTMLElement | null
    panelRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE)[0]?.focus()

    function handleKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        onClose()
        return
      }
      if (e.key !== 'Tab' || !panelRef.current) return
      const focusable = Array.from(
        panelRef.current.querySelectorAll<HTMLElement>(FOCUSABLE),
      ).filter((el) => !el.hasAttribute('disabled'))
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey) {
        if (document.activeElement === first) {
          e.preventDefault()
          last.focus()
        }
      } else if (document.activeElement === last) {
        e.preventDefault()
        first.focus()
      }
    }

    document.addEventListener('keydown', handleKey)
    return () => {
      document.removeEventListener('keydown', handleKey)
      previouslyFocused?.focus()
    }
  }, [open, onClose])

  if (!open) return null

  const titleId = `modal-title-${title.replace(/\s+/g, '-').toLowerCase()}`

  return (
    <>
      <div
        data-testid={testId ? `${testId}-backdrop` : 'modal-backdrop'}
        className="fixed inset-0 z-40 bg-canvas/80 backdrop-blur-sm"
        aria-hidden="true"
        onClick={onClose}
      />
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        data-testid={testId}
        className="fixed inset-0 z-50 flex items-center justify-center p-4"
      >
        <div
          className={[
            'flex max-h-[85vh] w-full flex-col rounded-xl border border-border-subtle bg-raised shadow-pop animate-scale-in',
            size === 'lg' ? 'max-w-2xl' : 'max-w-md',
          ].join(' ')}
        >
          <div className="space-y-5 overflow-y-auto p-6">
            <h2 id={titleId} className="text-lg font-extrabold tracking-tight text-text-primary">
              {title}
            </h2>
            {children}
          </div>
          {footer && (
            <div className="flex items-center justify-end gap-3 border-t border-border-subtle px-6 py-4">
              {footer}
            </div>
          )}
        </div>
      </div>
    </>
  )
}

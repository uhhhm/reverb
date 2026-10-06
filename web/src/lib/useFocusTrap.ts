import { useEffect, useRef, type RefObject } from 'react'

const FOCUSABLE = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'

/**
 * The keyboard contract every dialog needs: while active, Tab cycles within
 * panelRef, Escape calls onClose, and closing returns focus to whatever had it
 * before. focusFirst moves focus to the panel's first control on open; leave it
 * off when a field inside claims focus itself.
 */
export function useFocusTrap(
  active: boolean,
  panelRef: RefObject<HTMLElement | null>,
  onClose: () => void,
  { focusFirst = true }: { focusFirst?: boolean } = {},
): void {
  // Read through a ref so an inline onClose does not re-run the effect, which
  // would bounce focus back to the opener on every render.
  const onCloseRef = useRef(onClose)
  useEffect(() => {
    onCloseRef.current = onClose
  })

  useEffect(() => {
    if (!active) return
    const previouslyFocused = document.activeElement as HTMLElement | null
    if (focusFirst) panelRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE)[0]?.focus()

    function handleKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        onCloseRef.current()
        return
      }
      if (e.key !== 'Tab' || !panelRef.current) return
      const focusable = Array.from(panelRef.current.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
        (el) => !el.hasAttribute('disabled'),
      )
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
  }, [active, panelRef, focusFirst])
}

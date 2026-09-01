import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

/**
 * Multi-select for a list of ids, with the range and drag gestures a file
 * manager has: click one, shift-click another to take everything between, or
 * press and sweep across a run of rows.
 *
 * Selection is by id rather than by index so it survives the list being
 * refetched and reordered underneath it — which happens on every library
 * revision bump. Ranges need an order, so the ordered ids are passed to the
 * gesture calls rather than held here, where they would go stale.
 *
 * Both gestures compute their result from a base snapshot taken when the
 * gesture began, never by toggling as the pointer moves. Sweeping back over a
 * row you already crossed therefore un-does it, and a gesture that ends where
 * it started leaves the selection as it was.
 */

/** A drag either adds every row it crosses or removes them, fixed at the start. */
type DragMode = 'select' | 'deselect'

interface DragState {
  startId: string
  mode: DragMode
  base: ReadonlySet<string>
}

/** The ids between two endpoints inclusive, in either direction. */
function idsBetween(ordered: string[], a: string, b: string): string[] {
  const from = ordered.indexOf(a)
  const to = ordered.indexOf(b)
  if (from === -1 || to === -1) return []
  return from <= to ? ordered.slice(from, to + 1) : ordered.slice(to, from + 1)
}

function applyRange(
  base: ReadonlySet<string>,
  range: string[],
  mode: DragMode,
): ReadonlySet<string> {
  const next = new Set(base)
  for (const id of range) {
    if (mode === 'select') next.add(id)
    else next.delete(id)
  }
  return next
}

export function useSelection() {
  const [ids, setIds] = useState<ReadonlySet<string>>(() => new Set())
  const [dragging, setDragging] = useState(false)
  // The anchor a shift-click extends from: the last row picked on its own.
  const anchorRef = useRef<string | null>(null)
  const dragRef = useRef<DragState | null>(null)
  // Reading the selection inside a gesture without making every handler depend
  // on it, which would rebuild them on each change. The mirror is written in an
  // effect, which flushes long before a pointer event can read it.
  const idsRef = useRef(ids)
  useEffect(() => {
    idsRef.current = ids
  }, [ids])

  const toggle = useCallback((id: string) => {
    anchorRef.current = id
    setIds((prev) => {
      const next = new Set(prev)
      if (!next.delete(id)) next.add(id)
      return next
    })
  }, [])

  const clear = useCallback(() => {
    anchorRef.current = null
    setIds(new Set())
  }, [])

  const selectAll = useCallback((all: string[]) => {
    anchorRef.current = null
    setIds(new Set(all))
  }, [])

  /**
   * Shift-click: take everything from the anchor to here, on top of what is
   * already selected. With no anchor yet there is no range to take, so it falls
   * back to picking this one.
   */
  const extendTo = useCallback((id: string, ordered: string[]) => {
    const anchor = anchorRef.current
    if (anchor === null || anchor === id) {
      anchorRef.current = id
      setIds((prev) => new Set(prev).add(id))
      return
    }
    const range = idsBetween(ordered, anchor, id)
    if (range.length === 0) return
    // The anchor stays put, so shift-clicking again re-draws the range from the
    // same origin rather than walking it forward.
    setIds((prev) => applyRange(prev, range, 'select'))
  }, [])

  /**
   * Press: begin a sweep and apply it to the row under the pointer. Starting on
   * a selected row removes as it goes, which is how a file manager lets you take
   * a run back out of a selection.
   */
  const dragStart = useCallback((id: string) => {
    const base = idsRef.current
    const mode: DragMode = base.has(id) ? 'deselect' : 'select'
    dragRef.current = { startId: id, mode, base }
    anchorRef.current = id
    setDragging(true)
    setIds(applyRange(base, [id], mode))
  }, [])

  /** Sweep: everything from where the press began to here takes the drag's mode. */
  const dragOver = useCallback((id: string, ordered: string[]) => {
    const drag = dragRef.current
    if (!drag) return
    const range = idsBetween(ordered, drag.startId, id)
    if (range.length === 0) return
    setIds(applyRange(drag.base, range, drag.mode))
  }, [])

  // A sweep ends wherever the button comes up, which is often outside the list
  // and sometimes outside the window.
  useEffect(() => {
    if (!dragging) return
    function end() {
      dragRef.current = null
      setDragging(false)
    }
    window.addEventListener('pointerup', end)
    window.addEventListener('pointercancel', end)
    return () => {
      window.removeEventListener('pointerup', end)
      window.removeEventListener('pointercancel', end)
    }
  }, [dragging])

  return useMemo(
    () => ({
      ids,
      count: ids.size,
      has: (id: string) => ids.has(id),
      toggle,
      clear,
      selectAll,
      extendTo,
      dragStart,
      dragOver,
      /** True while a sweep is in progress, so the list can suppress text selection. */
      dragging,
      /** Keeps the given order, which is the order the user sees. */
      selectedFrom: <T extends { id: string }>(items: T[]) => items.filter((i) => ids.has(i.id)),
    }),
    [ids, toggle, clear, selectAll, extendTo, dragStart, dragOver, dragging],
  )
}

export type Selection = ReturnType<typeof useSelection>

/**
 * The handlers one selectable item needs for both gestures.
 *
 * The pointer owns mouse selection and the click is swallowed, so the item's own
 * control (a checkbox, a card's overlay button) cannot toggle a second time on
 * top of it. A keyboard-generated click carries `detail === 0` and is let
 * through untouched, so Space and Enter still work on that control.
 */
export function selectionHandlers(selection: Selection, id: string, ordered: string[]) {
  return {
    onPointerDown: (e: React.PointerEvent) => {
      if (e.button !== 0 || e.shiftKey) return
      selection.dragStart(id)
    },
    onPointerEnter: () => {
      if (selection.dragging) selection.dragOver(id, ordered)
    },
    onClickCapture: (e: React.MouseEvent) => {
      if (e.detail === 0) return
      e.preventDefault()
      e.stopPropagation()
      if (e.shiftKey) selection.extendTo(id, ordered)
    },
  }
}

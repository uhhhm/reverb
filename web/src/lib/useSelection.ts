import { useCallback, useMemo, useState } from 'react'

/**
 * Multi-select for a list of ids.
 *
 * Selection is by id rather than by index so it survives the list being
 * refetched and reordered underneath it — which happens on every library
 * revision bump.
 */
export function useSelection() {
  const [ids, setIds] = useState<ReadonlySet<string>>(() => new Set())

  const toggle = useCallback((id: string) => {
    setIds((prev) => {
      const next = new Set(prev)
      if (!next.delete(id)) next.add(id)
      return next
    })
  }, [])

  const clear = useCallback(() => setIds(new Set()), [])

  const selectAll = useCallback((all: string[]) => setIds(new Set(all)), [])

  return useMemo(
    () => ({
      ids,
      count: ids.size,
      has: (id: string) => ids.has(id),
      toggle,
      clear,
      selectAll,
      /** Keeps the given order, which is the order the user sees. */
      selectedFrom: <T extends { id: string }>(items: T[]) => items.filter((i) => ids.has(i.id)),
    }),
    [ids, toggle, clear, selectAll],
  )
}

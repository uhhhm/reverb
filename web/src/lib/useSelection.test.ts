import { describe, it, expect } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useSelection } from './useSelection'

describe('useSelection', () => {
  it('toggle adds then removes', () => {
    const { result } = renderHook(() => useSelection())
    expect(result.current.has('a')).toBe(false)
    expect(result.current.count).toBe(0)

    act(() => result.current.toggle('a'))
    expect(result.current.has('a')).toBe(true)
    expect(result.current.count).toBe(1)

    act(() => result.current.toggle('a'))
    expect(result.current.has('a')).toBe(false)
    expect(result.current.count).toBe(0)
  })

  it('clear empties', () => {
    const { result } = renderHook(() => useSelection())
    act(() => result.current.toggle('a'))
    act(() => result.current.toggle('b'))
    expect(result.current.count).toBe(2)

    act(() => result.current.clear())
    expect(result.current.count).toBe(0)
    expect(result.current.has('a')).toBe(false)
    expect(result.current.has('b')).toBe(false)
  })

  it('selectAll sets from a list', () => {
    const { result } = renderHook(() => useSelection())
    act(() => result.current.selectAll(['a', 'b', 'c']))
    expect(result.current.count).toBe(3)
    expect(result.current.has('a')).toBe(true)
    expect(result.current.has('b')).toBe(true)
    expect(result.current.has('c')).toBe(true)

    act(() => result.current.selectAll(['x']))
    expect(result.current.count).toBe(1)
    expect(result.current.has('x')).toBe(true)
    expect(result.current.has('a')).toBe(false)
  })

  it('selectedFrom preserves input order and filters to selected ids', () => {
    const { result } = renderHook(() => useSelection())
    act(() => result.current.selectAll(['b', 'd']))

    const items = [{ id: 'a' }, { id: 'b' }, { id: 'c' }, { id: 'd' }, { id: 'e' }]
    expect(result.current.selectedFrom(items)).toEqual([{ id: 'b' }, { id: 'd' }])

    // order is from input, not selection order
    const shuffled = [{ id: 'd' }, { id: 'b' }, { id: 'a' }]
    expect(result.current.selectedFrom(shuffled)).toEqual([{ id: 'd' }, { id: 'b' }])

    // filters out unselected
    const onlyUnselected = [{ id: 'a' }, { id: 'c' }]
    expect(result.current.selectedFrom(onlyUnselected)).toEqual([])
  })

  it('count reflects size', () => {
    const { result } = renderHook(() => useSelection())
    expect(result.current.count).toBe(0)

    act(() => result.current.toggle('a'))
    expect(result.current.count).toBe(1)

    act(() => result.current.toggle('b'))
    expect(result.current.count).toBe(2)

    act(() => result.current.toggle('a'))
    expect(result.current.count).toBe(1)

    act(() => result.current.clear())
    expect(result.current.count).toBe(0)
  })
})

describe('useSelection range and sweep gestures', () => {
  const ordered = ['a', 'b', 'c', 'd', 'e']
  const selected = (r: { has: (id: string) => boolean }) => ordered.filter((id) => r.has(id))

  it('shift-extends from the last item picked on its own', () => {
    const { result } = renderHook(() => useSelection())
    act(() => result.current.toggle('b'))
    act(() => result.current.extendTo('d', ordered))
    expect(selected(result.current)).toEqual(['b', 'c', 'd'])
  })

  it('extends backwards too', () => {
    const { result } = renderHook(() => useSelection())
    act(() => result.current.toggle('d'))
    act(() => result.current.extendTo('b', ordered))
    expect(selected(result.current)).toEqual(['b', 'c', 'd'])
  })

  it('keeps the anchor put, so a second shift-click redraws rather than walks', () => {
    const { result } = renderHook(() => useSelection())
    act(() => result.current.toggle('b'))
    act(() => result.current.extendTo('d', ordered))
    act(() => result.current.extendTo('c', ordered))
    // Still anchored at b: the range is b..c, and c..d stays from the first
    // extend because a range adds to the selection rather than replacing it.
    expect(result.current.has('b')).toBe(true)
    expect(result.current.has('c')).toBe(true)
  })

  it('adds a range on top of an existing selection', () => {
    const { result } = renderHook(() => useSelection())
    act(() => result.current.toggle('a'))
    act(() => result.current.toggle('c'))
    act(() => result.current.extendTo('e', ordered))
    expect(selected(result.current)).toEqual(['a', 'c', 'd', 'e'])
  })

  it('falls back to picking one when there is no anchor yet', () => {
    const { result } = renderHook(() => useSelection())
    act(() => result.current.extendTo('c', ordered))
    expect(selected(result.current)).toEqual(['c'])
  })

  it('sweeping from an unselected row selects everything it crosses', () => {
    const { result } = renderHook(() => useSelection())
    act(() => result.current.dragStart('b'))
    expect(result.current.dragging).toBe(true)
    act(() => result.current.dragOver('d', ordered))
    expect(selected(result.current)).toEqual(['b', 'c', 'd'])
  })

  it('sweeping from a selected row removes everything it crosses', () => {
    const { result } = renderHook(() => useSelection())
    act(() => result.current.selectAll(ordered))
    act(() => result.current.dragStart('b'))
    act(() => result.current.dragOver('d', ordered))
    expect(selected(result.current)).toEqual(['a', 'e'])
  })

  it('reverses cleanly when the sweep comes back, because it replays from a snapshot', () => {
    const { result } = renderHook(() => useSelection())
    act(() => result.current.dragStart('b'))
    act(() => result.current.dragOver('e', ordered))
    expect(selected(result.current)).toEqual(['b', 'c', 'd', 'e'])

    act(() => result.current.dragOver('c', ordered))
    expect(selected(result.current)).toEqual(['b', 'c'])

    // Back where it started: only the row under the press remains.
    act(() => result.current.dragOver('b', ordered))
    expect(selected(result.current)).toEqual(['b'])
  })

  it('leaves an existing selection outside the swept range alone', () => {
    const { result } = renderHook(() => useSelection())
    act(() => result.current.toggle('a'))
    act(() => result.current.dragStart('c'))
    act(() => result.current.dragOver('d', ordered))
    expect(selected(result.current)).toEqual(['a', 'c', 'd'])
  })

  it('ends the sweep on pointerup anywhere, including outside the list', () => {
    const { result } = renderHook(() => useSelection())
    act(() => result.current.dragStart('b'))
    expect(result.current.dragging).toBe(true)

    act(() => {
      window.dispatchEvent(new Event('pointerup'))
    })
    expect(result.current.dragging).toBe(false)

    // A move after the release is not part of any sweep.
    act(() => result.current.dragOver('e', ordered))
    expect(selected(result.current)).toEqual(['b'])
  })

  it('ignores a range whose endpoint is not in the visible order', () => {
    const { result } = renderHook(() => useSelection())
    act(() => result.current.toggle('b'))
    act(() => result.current.extendTo('zzz', ordered))
    expect(selected(result.current)).toEqual(['b'])
  })
})

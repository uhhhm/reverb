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

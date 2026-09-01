import { describe, it, expect } from 'vitest'
import { compileRule, previewChanges, EMPTY_RULE } from './findReplace'

describe('compileRule', () => {
  it('escapes literal find: "a.b" does NOT match "axb"', () => {
    const rule = compileRule({ find: 'a.b', replace: 'X', matchCase: true, useRegex: false })
    expect(rule.ok).toBe(true)
    if (!rule.ok) return
    expect(rule.apply('a.b')).toBe('X')
    expect(rule.apply('axb')).toBe('axb')
    expect(rule.apply('a.b axb a.b')).toBe('X axb X')
  })

  it('supports useRegex:true and $1 in replacement', () => {
    const rule = compileRule({ find: '(hello) (world)', replace: '$2 $1', matchCase: true, useRegex: true })
    expect(rule.ok).toBe(true)
    if (!rule.ok) return
    expect(rule.apply('hello world')).toBe('world hello')
  })

  it('supports capture groups with $1', () => {
    const rule = compileRule({ find: '(a)(b)', replace: '$2$1', matchCase: true, useRegex: true })
    expect(rule.ok).toBe(true)
    if (!rule.ok) return
    expect(rule.apply('ab')).toBe('ba')
  })

  it('returns { ok: false, error } for invalid regex', () => {
    const rule = compileRule({ find: '[', replace: 'x', matchCase: true, useRegex: true })
    expect(rule.ok).toBe(false)
    if (rule.ok) return
    expect(rule.error).toBeTruthy()
    expect(typeof rule.error).toBe('string')
  })

  it('distinguishes matchCase false vs true', () => {
    const insensitive = compileRule({ find: 'hello', replace: 'hi', matchCase: false, useRegex: false })
    const sensitive = compileRule({ find: 'hello', replace: 'hi', matchCase: true, useRegex: false })
    expect(insensitive.ok).toBe(true)
    expect(sensitive.ok).toBe(true)
    if (!insensitive.ok || !sensitive.ok) return
    expect(insensitive.apply('HELLO')).toBe('hi')
    expect(insensitive.apply('HeLLo')).toBe('hi')
    expect(sensitive.apply('HELLO')).toBe('HELLO')
    expect(sensitive.apply('hello')).toBe('hi')
  })

  it('replaces ALL occurrences in one string (global)', () => {
    const rule = compileRule({ find: 'a', replace: 'b', matchCase: true, useRegex: false })
    expect(rule.ok).toBe(true)
    if (!rule.ok) return
    expect(rule.apply('aaa')).toBe('bbb')
    expect(rule.apply('a a a')).toBe('b b b')
  })

  it('same compiled rule replaces in every string in sequence (lastIndex bug)', () => {
    const rule = compileRule({ find: 'foo', replace: 'bar', matchCase: true, useRegex: false })
    expect(rule.ok).toBe(true)
    if (!rule.ok) return
    expect(rule.apply('foo foo')).toBe('bar bar')
    expect(rule.apply('foo')).toBe('bar')
    expect(rule.apply('foo foo foo')).toBe('bar bar bar')
    expect(rule.apply('a foo b')).toBe('a bar b')
    expect(rule.apply('foo')).toBe('bar')
  })

  it('global replacement also works with regex', () => {
    const rule = compileRule({ find: 'x+', replace: 'y', matchCase: true, useRegex: true })
    expect(rule.ok).toBe(true)
    if (!rule.ok) return
    expect(rule.apply('xx x xxx')).toBe('y y y')
    // second call must still replace fully
    expect(rule.apply('xx')).toBe('y')
    expect(rule.apply('x x')).toBe('y y')
  })

  it('EMPTY_RULE is noop', () => {
    expect(EMPTY_RULE).toEqual({ find: '', replace: '', matchCase: false, useRegex: false })
    const rule = compileRule(EMPTY_RULE)
    expect(rule.ok).toBe(true)
    if (!rule.ok) return
    expect(rule.apply('anything')).toBe('anything')
  })
})

describe('previewChanges', () => {
  it('returns only entries whose value actually changes', () => {
    const items = [
      { id: '1', title: 'hello world' },
      { id: '2', title: 'no match' },
      { id: '3', title: 'hello again' },
    ]
    const rule = compileRule({ find: 'hello', replace: 'hi', matchCase: true, useRegex: false })
    expect(rule.ok).toBe(true)
    if (!rule.ok) return
    const changes = previewChanges(items, [{ name: 'Title', get: (i) => i.title }], rule)
    expect(changes).toHaveLength(2)
    expect(changes[0].before).toBe('hello world')
    expect(changes[0].after).toBe('hi world')
    expect(changes[0].item.id).toBe('1')
    expect(changes[1].item.id).toBe('3')
  })

  it('returns [] for a failed compile', () => {
    const bad = compileRule({ find: '[', replace: 'x', matchCase: true, useRegex: true })
    expect(bad.ok).toBe(false)
    const changes = previewChanges([{ id: '1', title: 'a' }], [{ name: 'Title', get: (i) => i.title }], bad)
    expect(changes).toEqual([])
  })

  it('returns [] when nothing matches', () => {
    const rule = compileRule({ find: 'xyz', replace: 'hi', matchCase: true, useRegex: false })
    expect(rule.ok).toBe(true)
    if (!rule.ok) return
    const changes = previewChanges([{ id: '1', title: 'hello' }], [{ name: 'Title', get: (i) => i.title }], rule)
    expect(changes).toEqual([])
  })
})

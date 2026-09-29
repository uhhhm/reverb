import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { Icon } from './Icon'

describe('Icon', () => {
  it('paints with currentColor so glyphs follow the theme text colour', () => {
    const { container, rerender } = render(<Icon name="search" />)
    expect(container.querySelector('svg')!.getAttribute('stroke')).toBe('currentColor')
    rerender(<Icon name="play" />)
    expect(container.querySelector('svg')!.getAttribute('fill')).toBe('currentColor')
  })
  it('is aria-hidden by default and labelled when a label is given', () => {
    const { container, rerender } = render(<Icon name="search" />)
    expect(container.querySelector('svg')!.getAttribute('aria-hidden')).toBe('true')
    rerender(<Icon name="search" aria-label="Search" />)
    const svg = container.querySelector('svg')!
    expect(svg.getAttribute('aria-hidden')).toBeNull()
    expect(svg.getAttribute('aria-label')).toBe('Search')
    expect(svg.getAttribute('role')).toBe('img')
  })
})

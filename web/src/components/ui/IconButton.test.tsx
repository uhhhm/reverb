import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { IconButton } from './IconButton'

describe('IconButton', () => {
  it('names the icon-only button by its label', () => {
    render(<IconButton name="heart" label="Like" />)
    expect(screen.getByRole('button', { name: 'Like' })).toBeInTheDocument()
  })

  it('highlights only while its toggle is active', () => {
    const { rerender } = render(<IconButton name="shuffle" label="Shuffle" active />)
    expect(screen.getByRole('button', { name: 'Shuffle' }).className).toMatch(/text-accent/)
    rerender(<IconButton name="shuffle" label="Shuffle" />)
    expect(screen.getByRole('button', { name: 'Shuffle' }).className).not.toMatch(/text-accent/)
  })

  it('does not fire onClick when disabled', () => {
    const onClick = vi.fn()
    render(<IconButton name="heart" label="Like" disabled onClick={onClick} />)
    fireEvent.click(screen.getByRole('button', { name: 'Like' }))
    expect(onClick).not.toHaveBeenCalled()
  })

  it('exposes a visible focus ring class', () => {
    render(<IconButton name="heart" label="Like" />)
    expect(screen.getByRole('button', { name: 'Like' }).className).toMatch(/focus-visible:ring/)
  })
})

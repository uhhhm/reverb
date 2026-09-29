import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Chip } from './Chip'

describe('Chip', () => {
  it('exposes its selection to assistive tech', () => {
    const { rerender } = render(<Chip>Playlists</Chip>)
    expect(screen.getByRole('button', { name: 'Playlists' })).toHaveAttribute('aria-pressed', 'false')
    rerender(<Chip selected>Playlists</Chip>)
    expect(screen.getByRole('button', { name: 'Playlists' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('exposes a visible focus ring class', () => {
    render(<Chip>Playlists</Chip>)
    expect(screen.getByRole('button', { name: 'Playlists' }).className).toMatch(/focus-visible:ring/)
  })
})

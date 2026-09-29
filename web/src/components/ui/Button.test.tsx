import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { Button } from './Button'

describe('Button', () => {
  it('does not fire onClick when disabled', () => {
    const onClick = vi.fn()
    render(<Button disabled onClick={onClick}>Go</Button>)
    fireEvent.click(screen.getByRole('button'))
    expect(onClick).not.toHaveBeenCalled()
  })

  it('exposes a visible focus ring class', () => {
    render(<Button>Go</Button>)
    expect(screen.getByRole('button').className).toMatch(/focus-visible:ring/)
  })

  it('forwards className for layout variants such as full-width auth actions', () => {
    render(<Button className="w-full">Go</Button>)
    expect(screen.getByRole('button', { name: 'Go' }).className).toMatch(/w-full/)
  })
})

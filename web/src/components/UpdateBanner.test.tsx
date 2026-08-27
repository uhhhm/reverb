import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { UpdateBanner } from './UpdateBanner'

describe('UpdateBanner', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders banner with tag', () => {
    render(<UpdateBanner tag="v1.2.3" onDismiss={vi.fn()} onUpdate={vi.fn()} />)
    expect(screen.getByTestId('update-banner')).toBeInTheDocument()
    expect(screen.getByText('Update available: v1.2.3')).toBeInTheDocument()
  })

  it('does not render when tag is empty', () => {
    const { container } = render(<UpdateBanner tag="" onDismiss={vi.fn()} onUpdate={vi.fn()} />)
    expect(screen.queryByTestId('update-banner')).not.toBeInTheDocument()
    expect(container.innerHTML).toBe('')
  })

  it('calls onDismiss when Dismiss clicked', () => {
    const onDismiss = vi.fn()
    render(<UpdateBanner tag="v1.2.3" onDismiss={onDismiss} onUpdate={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(onDismiss).toHaveBeenCalledTimes(1)
  })

  it('calls onUpdate when Download & Restart clicked', () => {
    const onUpdate = vi.fn()
    render(<UpdateBanner tag="v1.2.3" onDismiss={vi.fn()} onUpdate={onUpdate} />)
    fireEvent.click(screen.getByRole('button', { name: /Download & Restart/i }))
    expect(onUpdate).toHaveBeenCalledTimes(1)
  })

  it('renders both buttons', () => {
    render(<UpdateBanner tag="v2.0.0" onDismiss={vi.fn()} onUpdate={vi.fn()} />)
    expect(screen.getByRole('button', { name: /Download & Restart/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Dismiss' })).toBeInTheDocument()
  })
})

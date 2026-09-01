import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { UpdatePrompt } from './UpdatePrompt'
import { useUpdateStore } from '../lib/updateStore'
import { EMPTY_UPDATE_STATE, type UpdateState } from '../lib/updateApi'

function setState(patch: Partial<UpdateState>) {
  useUpdateStore.setState({ state: { ...EMPTY_UPDATE_STATE, ...patch } })
}

describe('UpdatePrompt', () => {
  beforeEach(() => {
    window.localStorage.clear()
    useUpdateStore.setState({ state: EMPTY_UPDATE_STATE, dismissed: '', installing: false })
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 200 })))
  })

  it('stays hidden while an update is only downloading', () => {
    setState({ available: 'v2.0.0', downloading: true, progress: 0.4 })
    render(<UpdatePrompt />)
    expect(screen.queryByTestId('update-prompt')).toBeNull()
  })

  it('offers a restart once the update is staged', () => {
    setState({ staged: 'v2.0.0' })
    render(<UpdatePrompt />)
    expect(screen.getByTestId('update-prompt')).toBeInTheDocument()
    expect(screen.getByText(/Reverb v2\.0\.0 is ready/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Restart now' })).toBeInTheDocument()
  })

  it('installs only when the user asks', async () => {
    setState({ staged: 'v2.0.0' })
    render(<UpdatePrompt />)
    expect(fetch).not.toHaveBeenCalledWith('/api/v1/update/install', expect.anything())

    fireEvent.click(screen.getByRole('button', { name: 'Restart now' }))
    await waitFor(() =>
      expect(fetch).toHaveBeenCalledWith(
        '/api/v1/update/install',
        expect.objectContaining({ method: 'POST' }),
      ),
    )
  })

  it('Later hides the prompt and remembers the declined version', async () => {
    setState({ staged: 'v2.0.0' })
    render(<UpdatePrompt />)
    fireEvent.click(screen.getByRole('button', { name: 'Later' }))

    expect(screen.queryByTestId('update-prompt')).toBeNull()
    expect(window.localStorage.getItem('reverb:updateDismissed')).toBe('v2.0.0')
  })

  it('prompts again for a version newer than the declined one', () => {
    useUpdateStore.setState({ dismissed: 'v2.0.0' })
    setState({ staged: 'v2.1.0' })
    render(<UpdatePrompt />)
    expect(screen.getByTestId('update-prompt')).toBeInTheDocument()
  })
})

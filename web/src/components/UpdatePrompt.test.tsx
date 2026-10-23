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

  it('shows why the install failed when the server refuses it', async () => {
    setState({ staged: 'v2.0.0' })
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(JSON.stringify({ error: 'sign updated app: exit status 1' }), { status: 409 }),
      ),
    )
    render(<UpdatePrompt />)
    fireEvent.click(screen.getByRole('button', { name: 'Restart now' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('sign updated app: exit status 1')
    expect(screen.getByRole('button', { name: 'Restart now' })).toBeEnabled()
    expect(screen.queryByText(/Restarting to finish the update/)).toBeNull()
  })

  it('still reports a refused install whose response has no message', async () => {
    setState({ staged: 'v2.0.0' })
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 503 })))
    render(<UpdatePrompt />)
    fireEvent.click(screen.getByRole('button', { name: 'Restart now' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/couldn.t install/i)
    expect(screen.getByRole('button', { name: 'Restart now' })).toBeEnabled()
  })

  it('treats the connection dropping as the app restarting, not as a failure', async () => {
    setState({ staged: 'v2.0.0' })
    vi.stubGlobal('fetch', vi.fn(async () => Promise.reject(new TypeError('Failed to fetch'))))
    render(<UpdatePrompt />)
    fireEvent.click(screen.getByRole('button', { name: 'Restart now' }))

    await waitFor(() => expect(fetch).toHaveBeenCalled())
    expect(screen.getByText(/Restarting to finish the update/)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('clears the previous failure when the user tries again', async () => {
    setState({ staged: 'v2.0.0' })
    const installs = [
      new Response(JSON.stringify({ error: 'rename: operation not permitted' }), { status: 409 }),
      new Response(JSON.stringify({ status: 'restarting' }), { status: 200 }),
    ]
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) =>
        url === '/api/v1/update/install'
          ? installs.shift()!
          : new Response(JSON.stringify({ staged: 'v2.0.0' }), { status: 200 }),
      ),
    )
    render(<UpdatePrompt />)
    fireEvent.click(screen.getByRole('button', { name: 'Restart now' }))
    await screen.findByRole('alert')

    fireEvent.click(screen.getByRole('button', { name: 'Restart now' }))
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    expect(screen.getByText(/Restarting to finish the update/)).toBeInTheDocument()
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

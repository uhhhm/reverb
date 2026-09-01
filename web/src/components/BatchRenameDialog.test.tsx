import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BatchRenameDialog } from './BatchRenameDialog'
import { makeTrack } from '../test/factories'

vi.mock('../lib/libraryEditApi', () => ({
  batchRename: vi.fn().mockResolvedValue({ applied: 1 }),
  BATCH_LIMIT: 500,
}))

import { batchRename } from '../lib/libraryEditApi'

function renderDialog(subject: React.ComponentProps<typeof BatchRenameDialog>['subject'], onClose = vi.fn(), onApplied = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <BatchRenameDialog subject={subject} onClose={onClose} onApplied={onApplied} />
    </QueryClientProvider>,
  )
  return { onClose, onApplied, qc }
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('BatchRenameDialog', () => {
  it('shows preview and Apply count after typing Find/Replace', async () => {
    const tracks = [
      makeTrack({ id: 't1', title: 'foo', artist: 'a', album: 'b' }),
      makeTrack({ id: 't2', title: 'foo2', artist: 'c', album: 'd' }),
    ]
    renderDialog({ kind: 'tracks', items: tracks })

    // initially no changes
    expect(screen.getByRole('button', { name: /Apply 0 changes/i })).toBeDisabled()
    expect(screen.getByText('Preview — 0 changes')).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText(/^Find$/i), { target: { value: 'foo' } })
    fireEvent.change(screen.getByLabelText(/Replace with/i), { target: { value: 'bar' } })

    // preview should list changed rows
    expect(await screen.findByText('Preview — 2 changes')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Apply 2 changes/i })).toBeEnabled()
    // changed rows show before/after — check both titles appear in preview (line-through before + new)
    expect(screen.getByText('foo')).toBeInTheDocument()
    expect(screen.getByText('bar')).toBeInTheDocument()
    expect(screen.getByText('foo2')).toBeInTheDocument()
    expect(screen.getByText('bar2')).toBeInTheDocument()
  })

  it('calls batchRename with collapsed per-track items carrying only changed fields', async () => {
    const t1 = makeTrack({ id: 't1', title: 'foo title', artist: 'foo artist', album: 'keep', albumId: 'al1', artistId: 'ar1' })
    const t2 = makeTrack({ id: 't2', title: 'foo title2', artist: 'other', album: 'keep2', albumId: 'al2', artistId: 'ar2' })
    const onClose = vi.fn()
    renderDialog({ kind: 'tracks', items: [t1, t2] }, onClose)

    // enable Artist field (Title already enabled by default)
    fireEvent.click(screen.getByRole('checkbox', { name: 'Artist' }))

    fireEvent.change(screen.getByLabelText(/^Find$/i), { target: { value: 'foo' } })
    fireEvent.change(screen.getByLabelText(/Replace with/i), { target: { value: 'bar' } })

    // t1: Title + Artist change (2), t2: only Title (1) => 3 changes total
    expect(await screen.findByText('Preview — 3 changes')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Apply 3 changes/i })).toBeEnabled()

    fireEvent.click(screen.getByRole('button', { name: /Apply 3 changes/i }))

    await waitFor(() => expect(batchRename).toHaveBeenCalledTimes(1))
    const req = vi.mocked(batchRename).mock.calls[0][0] as { tracks: { id: string; title?: string; artist?: string; album?: string }[] }
    expect(req.tracks).toHaveLength(2)

    // A track with two changed fields is ONE item carrying both. Untouched
    // fields are left out rather than sent as they stand: the server keeps
    // them, and sending the on-screen name would pin an album or artist rename
    // cascading down as a per-track override that a later edit could not reach.
    const gotT1 = req.tracks.find((t) => t.id === 't1')!
    expect(gotT1).toEqual({ id: 't1', title: 'bar title', artist: 'bar artist' })

    const gotT2 = req.tracks.find((t) => t.id === 't2')!
    expect(gotT2).toEqual({ id: 't2', title: 'bar title2' })

    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('does not submit when preview is empty (button disabled)', async () => {
    const tracks = [makeTrack({ id: 't1', title: 'hello', artist: 'world', album: 'test' })]
    renderDialog({ kind: 'tracks', items: tracks })

    const applyBtn = screen.getByRole('button', { name: /Apply 0 changes/i })
    expect(applyBtn).toBeDisabled()

    fireEvent.click(applyBtn)
    expect(batchRename).not.toHaveBeenCalled()

    // typing a find that matches nothing still leaves button disabled
    fireEvent.change(screen.getByLabelText(/^Find$/i), { target: { value: 'xyz' } })
    fireEvent.change(screen.getByLabelText(/Replace with/i), { target: { value: 'abc' } })
    expect(await screen.findByText('Preview — 0 changes')).toBeInTheDocument()
    const stillDisabled = screen.getByRole('button', { name: /Apply 0 changes/i })
    expect(stillDisabled).toBeDisabled()
    fireEvent.click(stillDisabled)
    expect(batchRename).not.toHaveBeenCalled()
  })
})

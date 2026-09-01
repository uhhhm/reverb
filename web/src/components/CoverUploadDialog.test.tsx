import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CoverUploadDialog } from './CoverUploadDialog'
import type { CoverTarget } from '../lib/libraryEditApi'

vi.mock('../lib/libraryEditApi', () => ({
  uploadCovers: vi.fn().mockResolvedValue({ applied: 1, coverArtId: 'custom:abc.png' }),
  clearCovers: vi.fn().mockResolvedValue({ applied: 1 }),
}))

import { uploadCovers, clearCovers } from '../lib/libraryEditApi'

const targets: CoverTarget[] = [
  { kind: 'album', id: 'a1' },
  { kind: 'track', id: 't1' },
]

function renderDialog(t = targets, onClose = vi.fn(), onApplied = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <CoverUploadDialog targets={t} onClose={onClose} onApplied={onApplied} />
    </QueryClientProvider>,
  )
  return { onClose, onApplied }
}

describe('CoverUploadDialog', () => {
  let origCreateObjectURL: typeof URL.createObjectURL
  let origRevokeObjectURL: typeof URL.revokeObjectURL

  beforeEach(() => {
    vi.clearAllMocks()
    origCreateObjectURL = URL.createObjectURL
    origRevokeObjectURL = URL.revokeObjectURL
    URL.createObjectURL = vi.fn(() => 'blob:fake') as unknown as typeof URL.createObjectURL
    URL.revokeObjectURL = vi.fn() as unknown as typeof URL.revokeObjectURL
  })

  afterEach(() => {
    URL.createObjectURL = origCreateObjectURL
    URL.revokeObjectURL = origRevokeObjectURL
  })

  it('shows error for non-image file and does not upload', async () => {
    const { onClose } = renderDialog()
    const input = screen.getByLabelText(/Image/i) as HTMLInputElement

    const badFile = new File(['abc'], 'a.txt', { type: 'text/plain' })
    fireEvent.change(input, { target: { files: [badFile] } })

    expect(await screen.findByRole('alert')).toHaveTextContent(/not a JPEG, PNG, or WebP/i)
    // Apply stays disabled
    expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled()

    fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
    expect(uploadCovers).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  })

  it('enables Apply for valid PNG and calls uploadCovers with file and targets', async () => {
    const onClose = vi.fn()
    renderDialog(targets, onClose)

    const input = screen.getByLabelText(/Image/i) as HTMLInputElement
    const file = new File(['imgdata'], 'cover.png', { type: 'image/png' })

    // initially disabled without file
    expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled()

    fireEvent.change(input, { target: { files: [file] } })

    // preview should be attempted via createObjectURL
    expect(URL.createObjectURL).toHaveBeenCalled()

    const applyBtn = await screen.findByRole('button', { name: 'Apply' })
    expect(applyBtn).toBeEnabled()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()

    fireEvent.click(applyBtn)

    await waitFor(() => expect(uploadCovers).toHaveBeenCalledTimes(1))
    expect(vi.mocked(uploadCovers).mock.calls[0][0]).toBe(file)
    expect(vi.mocked(uploadCovers).mock.calls[0][1]).toEqual(targets)
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('Remove cover calls clearCovers with the targets', async () => {
    const onClose = vi.fn()
    renderDialog(targets, onClose)

    fireEvent.click(screen.getByRole('button', { name: /Remove cover/i }))

    await waitFor(() => expect(clearCovers).toHaveBeenCalledTimes(1))
    expect(vi.mocked(clearCovers).mock.calls[0][0]).toEqual(targets)
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })
})

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { UploadTracksButton } from './UploadTracksButton'

const mockUpload = vi.fn()
const mockPush = vi.fn()

vi.mock('../lib/uploadApi', () => ({
  uploadTracks: (...args: unknown[]) => mockUpload(...args),
  UPLOAD_EXTENSIONS: ['.mp3', '.flac'],
}))
vi.mock('../lib/toastStore', () => ({
  useToastStore: (sel: (s: unknown) => unknown) => sel({ push: mockPush }),
}))

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <UploadTracksButton />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  mockUpload.mockResolvedValue({ uploaded: [{ name: 'a.mp3', bytes: 3 }], scanning: true })
})

describe('UploadTracksButton', () => {
  it('uploads the chosen files', async () => {
    wrap()
    const input = screen.getByLabelText(/audio files to upload/i)
    const file = new File(['abc'], 'a.mp3', { type: 'audio/mpeg' })
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => expect(mockUpload).toHaveBeenCalled())
    expect((mockUpload.mock.calls[0][0] as File[])[0].name).toBe('a.mp3')
  })

  // The accept list is the first line of "wrong format" feedback — the server
  // refuses anything else, but the picker should not offer it.
  it('restricts the picker to the supported formats', () => {
    wrap()
    expect(screen.getByLabelText(/audio files to upload/i)).toHaveAttribute('accept', '.mp3,.flac')
  })

  it('reports a failed upload instead of failing silently', async () => {
    mockUpload.mockRejectedValue(new Error('uploads need the built-in library'))
    wrap()
    fireEvent.change(screen.getByLabelText(/audio files to upload/i), {
      target: { files: [new File(['x'], 'a.mp3')] },
    })
    await waitFor(() =>
      expect(mockPush).toHaveBeenCalledWith('uploads need the built-in library', 'error'),
    )
  })

  it('says how many files were skipped', async () => {
    mockUpload.mockResolvedValue({
      uploaded: [{ name: 'a.mp3', bytes: 3 }],
      rejected: { 'b.txt': 'unsupported format' },
      scanning: true,
    })
    wrap()
    fireEvent.change(screen.getByLabelText(/audio files to upload/i), {
      target: { files: [new File(['x'], 'a.mp3')] },
    })
    await waitFor(() => expect(mockPush).toHaveBeenCalledWith('Added 1 file, skipped 1', 'error'))
  })
})

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { useState } from 'react'
import { LinkOptionsPanel } from './LinkOptionsPanel'
import type { LinkOptions } from '../lib/linkApi'

const mockListChapters = vi.fn()
vi.mock('../lib/linkApi', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/linkApi')>()
  return { ...actual, listChapters: (...a: unknown[]) => mockListChapters(...a) }
})

const URL_ = 'https://www.youtube.com/watch?v=abc'

/** Harness that holds the options, so the panel's controlled inputs behave. */
function Harness({ initial = {} }: { initial?: LinkOptions }) {
  const [value, setValue] = useState<LinkOptions>(initial)
  return <LinkOptionsPanel url={URL_} value={value} onChange={setValue} />
}

function openPanel() {
  fireEvent.click(screen.getByRole('button', { name: /advanced options/i }))
}

describe('LinkOptionsPanel', () => {
  beforeEach(() => {
    mockListChapters.mockReset()
  })

  it('starts collapsed and summarises as the whole video', () => {
    render(<Harness />)
    expect(screen.getByText(/whole video/i)).toBeInTheDocument()
    expect(screen.queryByLabelText(/start time/i)).not.toBeInTheDocument()
  })

  it('records a time range and shows it in the summary', () => {
    render(<Harness />)
    openPanel()
    fireEvent.change(screen.getByLabelText(/start time/i), { target: { value: '1:30' } })
    fireEvent.change(screen.getByLabelText(/end time/i), { target: { value: '4:00' } })
    expect(screen.getByText(/1:30 → 4:00/)).toBeInTheDocument()
  })

  it('disables chapter splitting once a range is set', () => {
    render(<Harness initial={{ startTime: '1:30' }} />)
    openPanel()
    expect(screen.getByLabelText(/split into chapters/i)).toBeDisabled()
    expect(screen.getByText(/clear the time range first/i)).toBeInTheDocument()
  })

  it('clears the range when chapter splitting is chosen', () => {
    render(<Harness />)
    openPanel()
    fireEvent.change(screen.getByLabelText(/start time/i), { target: { value: '1:30' } })
    // Range set, so the checkbox is disabled — clear the range, then split.
    fireEvent.click(screen.getByRole('button', { name: /clear time range/i }))
    fireEvent.click(screen.getByLabelText(/split into chapters/i))
    expect(screen.getByLabelText(/split into chapters/i)).toBeChecked()
    expect(screen.getByLabelText(/start time/i)).toHaveValue('')
  })

  it('previews chapters on demand', async () => {
    mockListChapters.mockResolvedValue([
      { title: 'Intro', startSec: 0, endSec: 30 },
      { title: 'Verse', startSec: 90, endSec: 150 },
    ])
    render(<Harness initial={{ splitChapters: true }} />)
    openPanel()
    fireEvent.click(screen.getByRole('button', { name: /preview chapters/i }))

    await waitFor(() => expect(screen.getByTestId('chapter-list')).toBeInTheDocument())
    expect(screen.getByText('Intro')).toBeInTheDocument()
    expect(screen.getByText('Verse')).toBeInTheDocument()
    expect(screen.getByText('1:30')).toBeInTheDocument()
    expect(mockListChapters).toHaveBeenCalledWith(URL_)
  })

  it('says so when the video has no chapters', async () => {
    mockListChapters.mockResolvedValue([])
    render(<Harness initial={{ splitChapters: true }} />)
    openPanel()
    fireEvent.click(screen.getByRole('button', { name: /preview chapters/i }))
    expect(await screen.findByText(/no chapters to split on/i)).toBeInTheDocument()
  })

  it('surfaces a chapter-read failure', async () => {
    mockListChapters.mockRejectedValue(new Error('yt-dlp exploded'))
    render(<Harness initial={{ splitChapters: true }} />)
    openPanel()
    fireEvent.click(screen.getByRole('button', { name: /preview chapters/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/yt-dlp exploded/i)
  })
})

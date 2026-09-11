import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { NotInterestedSection } from './NotInterestedSection'
import type { NotInterestedMark } from '../../lib/notInterestedApi'

const mockUndo = vi.fn()
vi.mock('../../lib/notInterestedApi', () => ({
  useNotInterested: vi.fn(),
  useUndoNotInterested: () => ({ mutate: mockUndo, isPending: false }),
}))

import { useNotInterested } from '../../lib/notInterestedApi'

function marks(list: NotInterestedMark[] | undefined, isLoading = false) {
  vi.mocked(useNotInterested).mockReturnValue({ data: list ? { marks: list } : undefined, isLoading } as ReturnType<typeof useNotInterested>)
}

beforeEach(() => {
  mockUndo.mockClear()
})

describe('NotInterestedSection', () => {
  it('lists marked tracks and artists', () => {
    marks([
      { key: 'track:1', kind: 'track', title: 'D.A.N.C.E.', artist: 'Justice', source: 'deezer', externalId: '1', markedAt: 2000 },
      { key: 'artist:stardust', kind: 'artist', artist: 'Stardust', markedAt: 1000 },
    ])
    render(<NotInterestedSection />)
    expect(screen.getByText('D.A.N.C.E.')).toBeInTheDocument()
    expect(screen.getByText('Stardust')).toBeInTheDocument()
  })

  it('undoes a mark', () => {
    marks([{ key: 'artist:stardust', kind: 'artist', artist: 'Stardust', markedAt: 1000 }])
    render(<NotInterestedSection />)
    fireEvent.click(screen.getByRole('button', { name: 'Undo not interested in Stardust' }))
    expect(mockUndo).toHaveBeenCalledWith('artist:stardust')
  })

  it('says when nothing is marked', () => {
    marks([])
    render(<NotInterestedSection />)
    expect(screen.getByText(/nothing marked/i)).toBeInTheDocument()
  })
})

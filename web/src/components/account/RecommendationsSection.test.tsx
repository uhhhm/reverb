import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { RecommendationsSection } from './RecommendationsSection'

const mockUpdate = vi.fn()
vi.mock('../../lib/recommendationsApi', () => ({
  useRecommendationSettings: () => ({ data: { adventurousness: 50, onlineRecommendations: true } }),
  useUpdateRecommendationSettings: () => ({ mutate: mockUpdate }),
}))

beforeEach(() => {
  mockUpdate.mockClear()
})

describe('RecommendationsSection', () => {
  it('saves Adventurousness when the slider is let go', () => {
    render(<RecommendationsSection />)
    const slider = screen.getByRole('slider', { name: 'Adventurousness' })
    fireEvent.change(slider, { target: { value: '80' } })
    expect(mockUpdate).not.toHaveBeenCalled()
    fireEvent.pointerUp(slider)
    expect(mockUpdate).toHaveBeenCalledWith({ adventurousness: 80 }, expect.anything())
  })

  it('switches online recommendations off', () => {
    render(<RecommendationsSection />)
    fireEvent.click(screen.getByRole('switch', { name: 'Online recommendations' }))
    expect(mockUpdate).toHaveBeenCalledWith({ onlineRecommendations: false })
  })

  it('says what is sent while online recommendations are on', () => {
    render(<RecommendationsSection />)
    expect(screen.getByText(/sends the artist and title of each track or artist/)).toHaveTextContent(
      /Last\.fm, ListenBrainz and Deezer/,
    )
  })
})

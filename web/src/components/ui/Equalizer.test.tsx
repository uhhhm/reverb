import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { Equalizer } from './Equalizer'

describe('Equalizer', () => {
  it('freezes its bars only while playback is paused', () => {
    const paused = (playing?: boolean) =>
      [...render(<Equalizer playing={playing} />).container.querySelectorAll('[data-testid="eq-bar"]')]
        .map((bar) => /animation-play-state:paused/.test(bar.className))
    expect(paused(false)).toEqual([true, true, true, true])
    expect(paused(true)).toEqual([false, false, false, false])
    expect(paused(undefined)).toEqual([false, false, false, false])
  })
})

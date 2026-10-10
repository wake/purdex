import { describe, it, expect } from 'vitest'
import { forgetFoldsOfPane } from './fold-memory'
import { forgetRevealsOfPane, isRevealed, markRevealed } from './deck-reveal-memory'

describe('deck reveal memory', () => {
  it('remembers per deck key, and forgets a key', () => {
    markRevealed('rv-a\0s', 't1')
    expect(isRevealed('rv-a\0s', 't1')).toBe(true)
    expect(isRevealed('rv-a\0s', 't2')).toBe(false)
    expect(isRevealed('rv-a\0s2', 't1')).toBe(false)
    forgetRevealsOfPane('rv-a')
    expect(isRevealed('rv-a\0s', 't1')).toBe(false)
  })

  it('goes with the pane when its folds are swept, sparing the key that is kept', () => {
    markRevealed('rv-b\0s', 't1')
    markRevealed('rv-b\0s2', 't1')
    forgetFoldsOfPane('rv-b', (key) => key === 'rv-b\0s2')
    expect(isRevealed('rv-b\0s', 't1')).toBe(false)
    expect(isRevealed('rv-b\0s2', 't1')).toBe(true)
    forgetFoldsOfPane('rv-b')
    expect(isRevealed('rv-b\0s2', 't1')).toBe(false)
  })
})

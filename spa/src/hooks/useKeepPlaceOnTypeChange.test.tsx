// c9d9c7-60: a type change re-flows the deck; the reader keeps their place. jsdom has no layout, so the anchor functions are replaced by ones
// whose answers the test controls: what matters is WHEN the place is captured (before the new size is drawn) and that the box is moved by
// how far it went — and only then.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, cleanup, render } from '@testing-library/react'
import { useKeepPlaceOnTypeChange } from './useKeepPlaceOnTypeChange'
import { useDeckTypeStore } from '../stores/useDeckTypeStore'

const calls: string[] = []
let moved = 0
vi.mock('../components/deck/deck-reveal', () => ({
  captureViewAnchor: () => { calls.push('capture'); return { el: document.body, top: 0, text: null } },
  viewAnchorMoved: () => { calls.push('moved'); return moved },
}))

const shiftBy = vi.fn()
function Probe({ box }: { box: HTMLElement }) {
  useKeepPlaceOnTypeChange(box, shiftBy)
  return null
}

beforeEach(() => {
  cleanup()
  calls.length = 0
  shiftBy.mockReset()
  moved = 0
  useDeckTypeStore.setState({ fontSize: null, lineHeight: null })
})

describe('useKeepPlaceOnTypeChange', () => {
  it('captures when the setting changes, then moves the box by how far the place went', () => {
    const box = document.createElement('div')
    render(<Probe box={box} />)
    moved = 40
    act(() => { useDeckTypeStore.getState().setFontSize(16) })
    expect(calls).toEqual(['capture', 'moved'])
    expect(shiftBy).toHaveBeenCalledWith(40)
  })
  it('a line-height change counts too, and a move upwards scrolls up', () => {
    const box = document.createElement('div')
    render(<Probe box={box} />)
    moved = -25
    act(() => { useDeckTypeStore.getState().setLineHeight(1.9) })
    expect(shiftBy).toHaveBeenCalledWith(-25)
  })
  it('does nothing when the place did not move', () => {
    const box = document.createElement('div')
    render(<Probe box={box} />)
    act(() => { useDeckTypeStore.getState().setFontSize(14) })
    expect(shiftBy).not.toHaveBeenCalled()
  })
  it('captures nothing without a change of the setting (an unrelated render)', () => {
    const box = document.createElement('div')
    const { rerender } = render(<Probe box={box} />)
    rerender(<Probe box={box} />)
    expect(calls).toEqual([])
  })
  it('a value that clamps to the same number is no change', () => {
    const box = document.createElement('div')
    useDeckTypeStore.getState().setFontSize(18)
    render(<Probe box={box} />)
    calls.length = 0
    act(() => { useDeckTypeStore.getState().setFontSize(40) })
    expect(calls).toEqual([])
  })
})

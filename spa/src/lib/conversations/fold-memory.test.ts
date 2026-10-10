import { describe, it, expect, beforeEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { forgetFolds, isFolded, setOpen, toggleOpen, usePaneFoldStore } from './fold-memory'

beforeEach(() => { forgetFolds('p1'); forgetFolds('p2') })

describe('fold memory', () => {
  it('remembers per pane and per key', () => {
    toggleOpen('p1', 'a')
    expect(isFolded('p1', 'a')).toBe(true)
    expect(isFolded('p1', 'b')).toBe(false)
    expect(isFolded('p2', 'a')).toBe(false)
    toggleOpen('p1', 'a')
    expect(isFolded('p1', 'a')).toBe(false)
  })

  it('outlives the component that read it (a tab switch unmounts the deck)', () => {
    const first = renderHook(() => usePaneFoldStore('p1'))
    act(() => first.result.current.toggle('out-1'))
    expect(first.result.current.isExpanded('out-1')).toBe(true)
    first.unmount()
    const second = renderHook(() => usePaneFoldStore('p1'))
    expect(second.result.current.isExpanded('out-1')).toBe(true)
  })

  it('re-renders readers when a key changes, and not for another pane', () => {
    let renders = 0
    const { result } = renderHook(() => { renders++; return usePaneFoldStore('p1') })
    const before = renders
    act(() => setOpen('p2', 'x', true))
    expect(renders).toBe(before)
    act(() => setOpen('p1', 'x', true))
    expect(result.current.isExpanded('x')).toBe(true)
    expect(renders).toBeGreaterThan(before)
  })

  it('opens keys with expand and forgets a closed pane', () => {
    const { result } = renderHook(() => usePaneFoldStore('p1'))
    act(() => result.current.expand(['a', 'b']))
    expect(result.current.isExpanded('b')).toBe(true)
    act(() => forgetFolds('p1'))
    expect(result.current.isExpanded('a')).toBe(false)
  })
})

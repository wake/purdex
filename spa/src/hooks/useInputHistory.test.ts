import { describe, it, expect } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useInputHistory, shouldNavigate } from './useInputHistory'

const hist = (entries: string[]) => renderHook(({ e }) => useInputHistory<string, string>(e), { initialProps: { e: entries } })

describe('useInputHistory', () => {
  it('walks back one entry per Up and stops at the oldest', () => {
    const { result } = hist(['a', 'b', 'c'])
    const h = result.current
    expect(h.up(() => 'draft')).toEqual({ kind: 'entry', entry: 'c' })
    expect(h.up(() => 'draft')).toEqual({ kind: 'entry', entry: 'b' })
    expect(h.up(() => 'draft')).toEqual({ kind: 'entry', entry: 'a' })
    expect(h.up(() => 'draft')).toBeNull()
  })

  it('walks forward and passing the newest returns the stashed draft (stashed once, on the first Up)', () => {
    const { result } = hist(['a', 'b'])
    const h = result.current
    let captured = 0
    const cap = () => { captured++; return 'A' }
    h.up(cap); h.up(cap)
    expect(captured).toBe(1)
    expect(h.down()).toEqual({ kind: 'entry', entry: 'b' })
    expect(h.down()).toEqual({ kind: 'draft', draft: 'A' })
    expect(h.isNavigating()).toBe(false)
    expect(h.down()).toBeNull()
  })

  it('an empty history does nothing and stashes nothing', () => {
    const { result } = hist([])
    expect(result.current.up(() => 'x')).toBeNull()
    expect(result.current.isNavigating()).toBe(false)
  })

  it('Down without having gone Up does nothing', () => {
    const { result } = hist(['a'])
    expect(result.current.down()).toBeNull()
  })

  it('reset drops the position and the stash', () => {
    const { result } = hist(['a', 'b'])
    const h = result.current
    h.up(() => 'A')
    h.reset()
    expect(h.isNavigating()).toBe(false)
    expect(h.up(() => 'B')).toEqual({ kind: 'entry', entry: 'b' })
    h.down()
    expect(h.down()).toBeNull()
  })

  it('an entry appended while walking does not move the position', () => {
    const { result, rerender } = hist(['a', 'b'])
    result.current.up(() => 'A')
    result.current.up(() => 'A') // at 'a'
    rerender({ e: ['a', 'b', 'c'] })
    expect(result.current.down()).toEqual({ kind: 'entry', entry: 'b' })
  })
})

describe('shouldNavigate', () => {
  const box = (value: string, s: number, e = s) => ({ value, selectionStart: s, selectionEnd: e })
  it('Up needs the caret at position 0; Down at the very end', () => {
    expect(shouldNavigate('up', box('hi', 0), false, null)).toBe(true)
    expect(shouldNavigate('up', box('hi', 1), false, null)).toBe(false)
    expect(shouldNavigate('down', box('hi', 2), false, null)).toBe(true)
    expect(shouldNavigate('down', box('hi', 1), false, null)).toBe(false)
  })
  it('the first line / last line edge is not enough', () => {
    expect(shouldNavigate('up', box('one\ntwo', 2), false, null)).toBe(false)
    expect(shouldNavigate('down', box('one\ntwo', 5), false, null)).toBe(false)
    expect(shouldNavigate('up', box('one\ntwo', 4), false, null)).toBe(false) // start of line 2
  })
  it('an empty box qualifies both ways', () => {
    expect(shouldNavigate('up', box('', 0), false, null)).toBe(true)
    expect(shouldNavigate('down', box('', 0), false, null)).toBe(true)
  })
  it('a selection or an IME composition blocks it', () => {
    expect(shouldNavigate('up', box('hi', 0, 2), false, null)).toBe(false)
    expect(shouldNavigate('up', box('hi', 0), true, null)).toBe(false)
    expect(shouldNavigate('down', box('hi', 2), true, null)).toBe(false)
  })
  it('right after a recall both arrows walk from the parked caret, until the text is edited or the caret moves', () => {
    const mark = { text: 'one\ntwo', pos: 0 }
    expect(shouldNavigate('down', box('one\ntwo', 0), false, mark)).toBe(true)
    expect(shouldNavigate('down', box('one\ntwo', 1), false, mark)).toBe(false)
    expect(shouldNavigate('down', box('one\ntwo!', 0), false, mark)).toBe(false)
  })
})

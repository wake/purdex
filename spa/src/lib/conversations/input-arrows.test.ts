// #2687: Up / Down in the deck's input as a state table — whether the key is ours (take back / history / caret), where the caret is, and what a history
// step does to the walk. The component tests (SessionInput.history*.test, take-back) pin the wiring; these pin the decisions themselves.
import { describe, it, expect, vi } from 'vitest'
import { arrowDir, caretAtEdge, decideArrow, isEmptyBox, planHistoryStep, type ArrowFacts, type HistoryEntry, type WalkState } from './input-arrows'

const key = (k: string, over: Partial<{ shiftKey: boolean; altKey: boolean; metaKey: boolean; ctrlKey: boolean }> = {}) =>
  ({ key: k, shiftKey: false, altKey: false, metaKey: false, ctrlKey: false, ...over })

describe('arrowDir', () => {
  it('a bare ArrowUp / ArrowDown has a direction', () => {
    expect(arrowDir(key('ArrowUp'))).toBe('up')
    expect(arrowDir(key('ArrowDown'))).toBe('down')
  })
  it.each(['shiftKey', 'altKey', 'metaKey', 'ctrlKey'] as const)('with %s held it is the caret\'s / the browser\'s', (mod) => {
    expect(arrowDir(key('ArrowUp', { [mod]: true }))).toBeNull()
    expect(arrowDir(key('ArrowDown', { [mod]: true }))).toBeNull()
  })
  it('any other key has none', () => {
    for (const k of ['ArrowLeft', 'ArrowRight', 'Enter', 'a', 'PageUp']) expect(arrowDir(key(k))).toBeNull()
  })
})

describe('isEmptyBox', () => {
  const base = { draft: '', shownChips: 0, uploading: 0, hasCleared: false }
  it('empty only when nothing is typed, no chip is shown, no upload is on its way and no cleared draft waits for its undo', () => {
    expect(isEmptyBox(base)).toBe(true)
    expect(isEmptyBox({ ...base, draft: 'x' })).toBe(false)
    expect(isEmptyBox({ ...base, shownChips: 1 })).toBe(false)
    expect(isEmptyBox({ ...base, uploading: 1 })).toBe(false)
    expect(isEmptyBox({ ...base, hasCleared: true })).toBe(false)
  })
})

describe('caretAtEdge', () => {
  it('Up: only a collapsed caret on the first line', () => {
    expect(caretAtEdge('up', 'one\ntwo', 2, 2)).toBe(true)
    expect(caretAtEdge('up', 'one\ntwo', 5, 5)).toBe(false) // on the second line
    expect(caretAtEdge('up', 'one\ntwo', 3, 3)).toBe(true) // at the end of the first line, before the break
    expect(caretAtEdge('up', 'one\ntwo', 4, 4)).toBe(false) // just after the break
  })
  it('Down: only a collapsed caret on the last line', () => {
    expect(caretAtEdge('down', 'one\ntwo', 5, 5)).toBe(true)
    expect(caretAtEdge('down', 'one\ntwo', 1, 1)).toBe(false)
    expect(caretAtEdge('down', 'one\ntwo', 3, 3)).toBe(false) // at the end of the first line: the break is still ahead
    expect(caretAtEdge('down', 'one\ntwo', 7, 7)).toBe(true)
  })
  it('a selection is the caret\'s own business', () => {
    expect(caretAtEdge('up', 'abc', 0, 2)).toBe(false)
    expect(caretAtEdge('down', 'abc', 1, 3)).toBe(false)
  })
  it('a one-line draft is the first and the last line', () => {
    expect(caretAtEdge('up', 'abc', 1, 1)).toBe(true)
    expect(caretAtEdge('down', 'abc', 1, 1)).toBe(true)
    expect(caretAtEdge('up', '', 0, 0)).toBe(true)
  })
})

describe('decideArrow', () => {
  const facts = (over: Partial<ArrowFacts> = {}): ArrowFacts => ({
    dir: 'up', recall: true, emptyBox: true, walking: false, atEdge: true, canTakeBackAll: () => true, ...over,
  })

  it('Up in an empty box with something held: take it all back — before any history', () => {
    expect(decideArrow(facts())).toBe('take_back_all')
    expect(decideArrow(facts({ walking: true }))).toBe('take_back_all') // (a walk cannot be on in an empty box in practice; the order is the rule)
  })

  it('nothing held (or an adapter that cannot take back): Up in an empty box starts the history', () => {
    expect(decideArrow(facts({ canTakeBackAll: () => false }))).toBe('history')
    expect(decideArrow(facts({ recall: false }))).toBe('history')
  })

  it('canTakeBackAll is asked only when the cheaper facts already say yes', () => {
    const ask = vi.fn(() => true)
    decideArrow(facts({ dir: 'down', canTakeBackAll: ask }))
    decideArrow(facts({ recall: false, canTakeBackAll: ask }))
    decideArrow(facts({ emptyBox: false, atEdge: false, canTakeBackAll: ask }))
    expect(ask).not.toHaveBeenCalled()
    decideArrow(facts({ canTakeBackAll: ask }))
    expect(ask).toHaveBeenCalledTimes(1)
  })

  it('Down in an empty box is not a start: the caret\'s', () => {
    expect(decideArrow(facts({ dir: 'down' }))).toBe('caret')
  })

  it('a draft that is not a walk: Up / Down are the caret\'s, whatever the caret edge — unless Up on the first line has a queue to take back (MS2-4, L5)', () => {
    for (const dir of ['up', 'down'] as const) {
      for (const atEdge of [true, false]) expect(decideArrow(facts({ dir, emptyBox: false, walking: false, atEdge, canTakeBackAll: () => false }))).toBe('caret')
    }
    expect(decideArrow(facts({ dir: 'down', emptyBox: false, walking: false, atEdge: true }))).toBe('caret')
    expect(decideArrow(facts({ dir: 'up', emptyBox: false, walking: false, atEdge: false }))).toBe('caret') // not on the first line
  })

  it('Up on the first line of a box with text takes the queue back first, before the history; with nothing to take back it is as before (MS2-4, L5)', () => {
    expect(decideArrow(facts({ emptyBox: false, walking: false, atEdge: true }))).toBe('take_back_all')
    expect(decideArrow(facts({ emptyBox: false, walking: true, atEdge: true }))).toBe('take_back_all')
    expect(decideArrow(facts({ emptyBox: false, walking: true, atEdge: true, canTakeBackAll: () => false }))).toBe('history')
    expect(decideArrow(facts({ emptyBox: false, walking: false, atEdge: true, recall: false }))).toBe('caret')
  })

  it('a walk in progress: a step only at the matching edge', () => {
    for (const dir of ['up', 'down'] as const) {
      expect(decideArrow(facts({ dir, emptyBox: false, walking: true, atEdge: true, canTakeBackAll: () => false }))).toBe('history')
      expect(decideArrow(facts({ dir, emptyBox: false, walking: true, atEdge: false, canTakeBackAll: () => false }))).toBe('caret')
    }
  })
})

describe('planHistoryStep', () => {
  const list: HistoryEntry[] = [{ ids: ['a'] }, { ids: ['b', 'b2'] }, { ids: ['c'] }]
  const walk = (ids: string[], shown = 'shown', chipIds: string[] = []): WalkState => ({ ids, shown, chipIds })

  describe('no walk is on', () => {
    it('Up with canStart starts at the newest', () => {
      expect(planHistoryStep(list, undefined, '', 'up', true)).toEqual({ kind: 'show', pos: 2 })
    })
    it('Down, no start, or an empty history is not a step — and forgets what was there', () => {
      const gone = { kind: 'none', forgetWalk: true, dropChips: true }
      expect(planHistoryStep(list, undefined, '', 'down', true)).toEqual(gone)
      expect(planHistoryStep(list, undefined, '', 'up', false)).toEqual(gone)
      expect(planHistoryStep([], undefined, '', 'up', true)).toEqual(gone)
    })
    it('an edited walk (the box no longer shows what it put there) is no walk: it is let go of, and Up may start a new one', () => {
      const edited = walk(['b'], 'old text', ['hist-1'])
      expect(planHistoryStep(list, edited, 'old text, edited', 'up', true)).toEqual({ kind: 'show', pos: 2 })
      expect(planHistoryStep(list, edited, 'old text, edited', 'up', false)).toEqual({ kind: 'none', forgetWalk: true, dropChips: true })
    })
  })

  describe('a walk is on', () => {
    it('Up goes to the older entry; at the oldest it is not a step and the walk stays', () => {
      expect(planHistoryStep(list, walk(['c']), 'shown', 'up', false)).toEqual({ kind: 'show', pos: 1 })
      expect(planHistoryStep(list, walk(['b']), 'shown', 'up', false)).toEqual({ kind: 'show', pos: 0 })
      expect(planHistoryStep(list, walk(['a']), 'shown', 'up', false)).toEqual({ kind: 'none', forgetWalk: false, dropChips: false })
    })
    it('Down goes to the newer entry; past the newest the walk ends in an empty box', () => {
      expect(planHistoryStep(list, walk(['a']), 'shown', 'down', false)).toEqual({ kind: 'show', pos: 1 })
      expect(planHistoryStep(list, walk(['c']), 'shown', 'down', false)).toEqual({ kind: 'end' })
    })
    it('the entry is found by any of its member ids (a collapsed run keeps every id)', () => {
      expect(planHistoryStep(list, walk(['b2']), 'shown', 'up', false)).toEqual({ kind: 'show', pos: 0 })
      expect(planHistoryStep(list, walk(['zzz', 'b']), 'shown', 'down', false)).toEqual({ kind: 'show', pos: 2 })
    })
    it('the entry is gone from the list: the walk ends, the box stays as it is (its chips too)', () => {
      expect(planHistoryStep(list, walk(['gone']), 'shown', 'up', false)).toEqual({ kind: 'none', forgetWalk: true, dropChips: false })
      expect(planHistoryStep(list, walk(['gone']), 'shown', 'down', false)).toEqual({ kind: 'none', forgetWalk: true, dropChips: false })
    })
    it('the list shifting under the walk (a page landed above) moves the position, not the entry', () => {
      const shifted: HistoryEntry[] = [{ ids: ['x'] }, { ids: ['y'] }, ...list]
      expect(planHistoryStep(shifted, walk(['b']), 'shown', 'up', false)).toEqual({ kind: 'show', pos: 2 })
    })
  })
})

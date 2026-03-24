import { describe, it, expect } from 'vitest'
import { getPrimaryPane, findPane, updatePaneInLayout, getLayoutKey } from './pane-tree'
import type { PaneLayout, Pane } from '../types/tab'

const paneA: Pane = { id: 'aaaaaa', content: { kind: 'session', sessionCode: 'abc123', mode: 'terminal' } }
const paneB: Pane = { id: 'bbbbbb', content: { kind: 'dashboard' } }

const leaf: PaneLayout = { type: 'leaf', pane: paneA }
const split: PaneLayout = {
  type: 'split', id: 'ssssss', direction: 'h',
  children: [{ type: 'leaf', pane: paneA }, { type: 'leaf', pane: paneB }],
  sizes: [50, 50],
}

describe('getPrimaryPane', () => {
  it('returns pane from leaf layout', () => {
    expect(getPrimaryPane(leaf)).toBe(paneA)
  })

  it('returns first leaf pane from split layout', () => {
    expect(getPrimaryPane(split)).toBe(paneA)
  })
})

describe('findPane', () => {
  it('finds pane by id in leaf', () => {
    expect(findPane(leaf, 'aaaaaa')).toBe(paneA)
  })

  it('finds pane by id in split', () => {
    expect(findPane(split, 'bbbbbb')).toBe(paneB)
  })

  it('returns undefined for unknown id', () => {
    expect(findPane(leaf, 'zzzzzz')).toBeUndefined()
  })
})

describe('updatePaneInLayout', () => {
  it('updates pane content in leaf', () => {
    const updated = updatePaneInLayout(leaf, 'aaaaaa', { kind: 'history' })
    expect(updated.type).toBe('leaf')
    if (updated.type === 'leaf') {
      expect(updated.pane.content).toEqual({ kind: 'history' })
      expect(updated.pane.id).toBe('aaaaaa')
    }
  })

  it('updates pane content in nested split', () => {
    const updated = updatePaneInLayout(split, 'bbbbbb', { kind: 'history' })
    if (updated.type === 'split') {
      const secondChild = updated.children[1]
      if (secondChild.type === 'leaf') {
        expect(secondChild.pane.content).toEqual({ kind: 'history' })
      }
    }
  })

  it('returns same layout if pane not found', () => {
    const updated = updatePaneInLayout(leaf, 'zzzzzz', { kind: 'history' })
    expect(updated).toBe(leaf) // same reference — no change
  })
})

describe('getLayoutKey', () => {
  it('returns pane id for leaf', () => {
    expect(getLayoutKey(leaf)).toBe('aaaaaa')
  })

  it('returns split id for split', () => {
    expect(getLayoutKey(split)).toBe('ssssss')
  })
})

import { describe, it, expect, vi } from 'vitest'
import { getPrimaryPane, findPane, updatePaneInLayout, getLayoutKey, findTabBySessionCode, scanPaneTree, splitAtPane, removePane, countLeaves, collectLeaves, applyLayoutPattern } from './pane-tree'
import type { PaneLayout, Pane, PaneContent } from '../types/tab'

// ── helpers for new tests ──────────────────────────────────────────────────
const mkLeaf = (id: string, kind: string = 'dashboard'): PaneLayout => ({ type: 'leaf', pane: { id, content: { kind } as PaneContent } })
const mkSplit = (id: string, dir: 'h' | 'v', children: PaneLayout[], sizes?: number[]): PaneLayout => ({ type: 'split', id, direction: dir, children, sizes: sizes ?? children.map(() => 100 / children.length) })

const paneA: Pane = { id: 'aaaaaa', content: { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'abc123', mode: 'terminal', cachedName: '', tmuxInstance: '' } }
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

  it('returns placeholder pane for corrupted split with empty children', () => {
    const corrupted: PaneLayout = {
      type: 'split', id: 'broken', direction: 'h',
      children: [], sizes: [],
    }
    const result = getPrimaryPane(corrupted)
    expect(result.id).toBe('corrupted')
    expect(result.content).toEqual({ kind: 'new-tab' })
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

describe('findTabBySessionCode', () => {
  it('returns undefined when tabs is empty', () => {
    expect(findTabBySessionCode({}, 'abc123')).toBeUndefined()
  })

  it('returns tabId when session code matches', () => {
    const tabs = {
      tab1: { layout: { type: 'leaf', pane: paneA } as PaneLayout },
    }
    expect(findTabBySessionCode(tabs, 'abc123')).toBe('tab1')
  })

  it('returns undefined when no session code matches', () => {
    const tabs = {
      tab1: { layout: { type: 'leaf', pane: paneA } as PaneLayout },
    }
    expect(findTabBySessionCode(tabs, 'zzz999')).toBeUndefined()
  })

  it('returns first matching tabId when multiple tabs have different sessions', () => {
    const paneC: Pane = { id: 'cccccc', content: { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'xyz789', mode: 'terminal', cachedName: '', tmuxInstance: '' } }
    const tabs = {
      tab1: { layout: { type: 'leaf', pane: paneA } as PaneLayout },
      tab2: { layout: { type: 'leaf', pane: paneC } as PaneLayout },
    }
    expect(findTabBySessionCode(tabs, 'xyz789')).toBe('tab2')
  })

  it('returns undefined for non-session pane kinds', () => {
    const paneSettings: Pane = { id: 'dddddd', content: { kind: 'settings', scope: 'global' } }
    const paneDashboard: Pane = { id: 'eeeeee', content: { kind: 'dashboard' } }
    const tabs = {
      tab1: { layout: { type: 'leaf', pane: paneSettings } as PaneLayout },
      tab2: { layout: { type: 'leaf', pane: paneDashboard } as PaneLayout },
    }
    expect(findTabBySessionCode(tabs, 'abc123')).toBeUndefined()
  })
})

describe('scanPaneTree', () => {
  it('calls fn for leaf pane', () => {
    const fn = vi.fn()
    scanPaneTree(leaf, fn)
    expect(fn).toHaveBeenCalledTimes(1)
    expect(fn).toHaveBeenCalledWith(paneA)
  })

  it('calls fn for all panes in split layout', () => {
    const fn = vi.fn()
    scanPaneTree(split, fn)
    expect(fn).toHaveBeenCalledTimes(2)
    expect(fn).toHaveBeenCalledWith(paneA)
    expect(fn).toHaveBeenCalledWith(paneB)
  })

  it('calls fn for nested split layouts', () => {
    const paneC: Pane = { id: 'cccccc', content: { kind: 'history' } }
    const nested: PaneLayout = {
      type: 'split', id: 'outer', direction: 'v',
      children: [
        split,
        { type: 'leaf', pane: paneC },
      ],
      sizes: [70, 30],
    }
    const fn = vi.fn()
    scanPaneTree(nested, fn)
    expect(fn).toHaveBeenCalledTimes(3)
    expect(fn).toHaveBeenCalledWith(paneC)
  })
})

// ── splitAtPane ─────────────────────────────────────────────────────────────
describe('splitAtPane', () => {
  it('splits a leaf pane into horizontal split', () => {
    const layout = mkLeaf('p1')
    const result = splitAtPane(layout, 'p1', 'h', { kind: 'dashboard' })
    expect(result.type).toBe('split')
    if (result.type === 'split') {
      expect(result.direction).toBe('h')
      expect(result.children).toHaveLength(2)
      expect(result.children[0]).toBe(layout)
      expect(result.children[1].type).toBe('leaf')
      expect(result.sizes).toEqual([50, 50])
    }
  })

  it('splits nested pane by traversing tree', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2')])
    const result = splitAtPane(layout, 'p2', 'v', { kind: 'history' })
    expect(result.type).toBe('split')
    if (result.type === 'split') {
      const second = result.children[1]
      expect(second.type).toBe('split')
      if (second.type === 'split') {
        expect(second.direction).toBe('v')
        expect(second.children).toHaveLength(2)
      }
    }
  })

  it('returns layout unchanged when paneId not found', () => {
    const layout = mkLeaf('p1')
    const result = splitAtPane(layout, 'notexist', 'h', { kind: 'dashboard' })
    expect(result).toBe(layout)
  })
})

// ── removePane ───────────────────────────────────────────────────────────────
describe('removePane', () => {
  it('returns null when removing the only leaf', () => {
    expect(removePane(mkLeaf('p1'), 'p1')).toBeNull()
  })

  it('promotes sibling when one child is removed from a 2-child split', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2')])
    const result = removePane(layout, 'p1')
    expect(result).toEqual(mkLeaf('p2'))
  })

  it('redistributes sizes for 3-child split after removal', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2'), mkLeaf('p3')], [20, 40, 40])
    const result = removePane(layout, 'p1')
    expect(result?.type).toBe('split')
    if (result?.type === 'split') {
      expect(result.children).toHaveLength(2)
      // sizes should be normalized to sum 100
      const total = result.sizes.reduce((a, b) => a + b, 0)
      expect(Math.round(total)).toBe(100)
    }
  })

  it('returns layout unchanged when paneId not found', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2')])
    const result = removePane(layout, 'notexist')
    expect(result).toBe(layout)
  })

  it('removes a pane deep in nested split', () => {
    const layout = mkSplit('s1', 'h', [
      mkLeaf('p1'),
      mkSplit('s2', 'v', [mkLeaf('p2'), mkLeaf('p3')]),
    ])
    const result = removePane(layout, 'p3')
    expect(result).not.toBeNull()
    if (!result || result.type !== 'split') throw new Error('expected split')
    expect(result.children).toHaveLength(2)
    // p1 untouched, s2 collapsed to just p2
    expect(result.children[0]).toEqual(mkLeaf('p1'))
    expect(result.children[1]).toEqual(mkLeaf('p2'))
  })
})

// ── countLeaves ──────────────────────────────────────────────────────────────
describe('countLeaves', () => {
  it('returns 1 for a leaf', () => {
    expect(countLeaves(mkLeaf('p1'))).toBe(1)
  })

  it('counts leaves in nested splits', () => {
    const layout = mkSplit('s1', 'v', [
      mkSplit('s2', 'h', [mkLeaf('p1'), mkLeaf('p2')]),
      mkLeaf('p3'),
    ])
    expect(countLeaves(layout)).toBe(3)
  })
})

// ── collectLeaves ─────────────────────────────────────────────────────────────
describe('collectLeaves', () => {
  it('collects all leaf panes in order', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2'), mkLeaf('p3')])
    const panes = collectLeaves(layout)
    expect(panes.map((p) => p.id)).toEqual(['p1', 'p2', 'p3'])
  })
})

// ── applyLayoutPattern ────────────────────────────────────────────────────────
describe('applyLayoutPattern', () => {
  it('single flattens to first leaf', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2')])
    const result = applyLayoutPattern(layout, 'single')
    expect(result.type).toBe('leaf')
    if (result.type === 'leaf') {
      expect(result.pane.id).toBe('p1')
    }
  })

  it('split-h creates a 2-child horizontal split', () => {
    const result = applyLayoutPattern(mkLeaf('p1'), 'split-h')
    expect(result.type).toBe('split')
    if (result.type === 'split') {
      expect(result.direction).toBe('h')
      expect(result.children).toHaveLength(2)
      expect(result.sizes).toEqual([50, 50])
    }
  })

  it('split-h preserves existing pane ids', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2')])
    const result = applyLayoutPattern(layout, 'split-h')
    if (result.type === 'split') {
      const ids = result.children.map((c) => c.type === 'leaf' ? c.pane.id : null)
      expect(ids).toEqual(['p1', 'p2'])
    }
  })

  it('grid-4 creates a 2x2 layout', () => {
    const layout = mkSplit('s1', 'h', [mkLeaf('p1'), mkLeaf('p2'), mkLeaf('p3'), mkLeaf('p4')])
    const result = applyLayoutPattern(layout, 'grid-4')
    expect(result.type).toBe('split')
    if (result.type === 'split') {
      expect(result.direction).toBe('v')
      expect(result.children).toHaveLength(2)
      result.children.forEach((row) => {
        expect(row.type).toBe('split')
        if (row.type === 'split') {
          expect(row.direction).toBe('h')
          expect(row.children).toHaveLength(2)
        }
      })
    }
  })
})
